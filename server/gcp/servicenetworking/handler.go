// Package servicenetworking implements the GCP Service Networking REST API
// (servicenetworking.googleapis.com).
//
// Private services access is how a VPC reaches Google-managed services over
// internal addresses, a managed database peered into the caller's network,
// for instance. A caller sets a connection up while building the network and
// removes it while tearing the network down, so an unimplemented API blocks
// the teardown rather than just the feature.
//
// Connections are stored per network. The peering itself is not modeled:
// nothing in the emulator routes packets, so a connection is a record that
// exists or does not.
package servicenetworking

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/gcp/opmeta"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// basePrefix identifies a Service Networking request.
const basePrefix = "/v1/services/"

// connectionsSegment is the sub-collection this handler serves.
const connectionsSegment = "/connections"

// opPrefix starts every operation path this handler mints. Cloud Functions
// gen1 also serves /v1/operations/{op}, so the "sn-" id prefix is what lets
// this handler claim only its own polls.
const opPrefix = "/v1/operations/sn-"

// connectionTypeURL is the Any type of a create or patch operation's response.
const connectionTypeURL = "type.googleapis.com/google.cloud.servicenetworking.v1.Connection"

// deleteVerb is an accepted alias suffix on a deleteConnection call.
const deleteVerb = ":deleteConnection"

// peeringName is the VPC peering every Service Networking connection reports.
const peeringName = "servicenetworking-googleapis-com"

// Handler serves the Service Networking REST surface.
type Handler struct {
	mu sync.RWMutex
	// connections is keyed by the network the caller named, so a delete
	// removes what a create added rather than clearing everything.
	connections map[string]json.RawMessage
	// ops records every operation this handler mints so a poll replays it and
	// an unknown name is 404 NOT_FOUND.
	ops *lro.Registry
}

// New returns a Service Networking handler.
func New() *Handler {
	return &Handler{connections: map[string]json.RawMessage{}, ops: lro.NewRegistry()}
}

// SetOperationRegistry records this handler's operations in the server-wide
// registry instead of its own.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// Matches claims /v1/services/{service}/connections... requests and GET polls
// of the operations this handler mints.
func (*Handler) Matches(r *http.Request) bool {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, opPrefix) {
		return true
	}

	return strings.HasPrefix(r.URL.Path, basePrefix) &&
		strings.Contains(r.URL.Path, connectionsSegment)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, opPrefix) {
		lro.ServeGet(w, h.ops, strings.TrimPrefix(r.URL.Path, "/v1/"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost, http.MethodPatch, http.MethodPut:
		h.upsert(w, r)
	case http.MethodDelete:
		h.remove(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// network reads the network a request refers to. Callers pass it as a query
// parameter; "-" in the path means "whichever connections exist", which is how
// a teardown addresses a connection it did not record the name of.
func network(r *http.Request) string {
	if n := r.URL.Query().Get("network"); n != "" {
		return n
	}

	return "-"
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]json.RawMessage, 0, len(h.connections))

	if n := network(r); n != "-" {
		if c, ok := h.connections[n]; ok {
			out = append(out, c)
		}
	} else {
		for _, c := range h.connections {
			out = append(out, c)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func (h *Handler) upsert(w http.ResponseWriter, r *http.Request) {
	// Capped like every sibling handler. The decode stays tolerant, a
	// connection removal legitimately sends no body, but an unbounded read
	// is not the way to accept that.
	r.Body = http.MaxBytesReader(w, r.Body, gcprest.MaxBodyBytes)

	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// A connection removal sends no body; that is not an error.
		body = json.RawMessage(`{}`)
	}

	key := bodyNetwork(r, body)

	if isDeleteConnection(r) {
		h.mu.Lock()
		delete(h.connections, key)
		h.mu.Unlock()

		h.writeDoneOperation(w, opmeta.Empty())

		return
	}

	body = withServiceAndPeering(r, body)

	h.mu.Lock()
	h.connections[key] = body
	h.mu.Unlock()

	h.writeDoneOperation(w, opmeta.Response(body, connectionTypeURL))
}

// isDeleteConnection reports a connections.deleteConnection call: a POST to a
// single connection, POST /v1/services/{s}/connections/{id}. Real clients send
// it without a verb, since the API maps it to POST /v1/{name=services/*/connections/*};
// a trailing :deleteConnection is accepted as an alias. A POST to the
// collection is a create and a PATCH is an update, whatever their body holds.
func isDeleteConnection(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}

	i := strings.Index(r.URL.Path, connectionsSegment+"/")
	if i < 0 {
		return false
	}

	id := strings.TrimSuffix(r.URL.Path[i+len(connectionsSegment)+1:], deleteVerb)

	return id != "" && !strings.Contains(id, "/")
}

// bodyNetwork picks the network a create, patch or deleteConnection names: the
// body's network (a Connection) or consumerNetwork (a DeleteConnectionRequest),
// otherwise the network query parameter, so a later list filtered by that
// network finds the connection.
func bodyNetwork(r *http.Request, body json.RawMessage) string {
	var named struct {
		Network         string `json:"network"`
		ConsumerNetwork string `json:"consumerNetwork"`
	}

	_ = json.Unmarshal(body, &named)

	switch {
	case named.Network != "":
		return named.Network
	case named.ConsumerNetwork != "":
		return named.ConsumerNetwork
	default:
		return network(r)
	}
}

// withServiceAndPeering fills the output-only service and peering fields a
// real Connection carries, leaving the body unchanged when it is not a JSON
// object.
func withServiceAndPeering(r *http.Request, body json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return body
	}

	service := strings.TrimPrefix(r.URL.Path, basePrefix)
	if i := strings.Index(service, "/"); i >= 0 {
		service = service[:i]
	}

	set := func(k, v string) {
		if _, ok := fields[k]; !ok {
			fields[k], _ = json.Marshal(v)
		}
	}

	set("service", "services/"+service)
	set("peering", peeringName)

	out, err := json.Marshal(fields)
	if err != nil {
		return body
	}

	return out
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	// A delete has to name the network it targets. Treating an absent
	// parameter as "all of them" means a request with no query string destroys
	// every connection in the process, including ones belonging to networks
	// the caller has nothing to do with.
	n := r.URL.Query().Get("network")
	if n == "" || n == "-" {
		writeError(w, http.StatusBadRequest, "required",
			"the network query parameter is required to delete a connection")

		return
	}

	h.mu.Lock()
	delete(h.connections, n)
	h.mu.Unlock()

	h.writeDoneOperation(w, opmeta.Empty())
}

// writeDoneOperation answers with an already-finished long-running operation
// carrying response, recorded under a unique name so a poll replays it.
func (h *Handler) writeDoneOperation(w http.ResponseWriter, response json.RawMessage) {
	name := "operations/sn-" + opmeta.NewID(time.Now())
	h.ops.Register(name, response)

	op := map[string]any{"name": name, "done": true}
	if response != nil {
		op["response"] = response
	}

	writeJSON(w, http.StatusOK, op)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, reason, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": message,
			"errors":  []map[string]string{{"reason": reason, "message": message}},
		},
	})
}
