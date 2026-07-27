// Package servicenetworking implements the GCP Service Networking REST API
// (servicenetworking.googleapis.com).
//
// Private services access is how a VPC reaches Google-managed services over
// internal addresses — a managed database peered into the caller's network,
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
)

// basePrefix identifies a Service Networking request.
const basePrefix = "/v1/services/"

// connectionsSegment is the sub-collection this handler serves.
const connectionsSegment = "/connections"

// Handler serves the Service Networking REST surface.
type Handler struct {
	mu sync.RWMutex
	// connections is keyed by the network the caller named, so a delete
	// removes what a create added rather than clearing everything.
	connections map[string]json.RawMessage
}

// New returns a Service Networking handler.
func New() *Handler {
	return &Handler{connections: map[string]json.RawMessage{}}
}

// Matches claims /v1/services/{service}/connections... requests.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, basePrefix) &&
		strings.Contains(r.URL.Path, connectionsSegment)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// A connection removal sends no body; that is not an error.
		body = json.RawMessage(`{}`)
	}

	h.mu.Lock()
	h.connections[network(r)] = body
	h.mu.Unlock()

	writeDoneOperation(w)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if n := network(r); n == "-" {
		h.connections = map[string]json.RawMessage{}
	} else {
		delete(h.connections, n)
	}
	h.mu.Unlock()

	writeDoneOperation(w)
}

// writeDoneOperation answers with an already-finished long-running operation.
// Callers poll until done; there is nothing asynchronous here to wait for.
func writeDoneOperation(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name": "operations/servicenetworking-done",
		"done": true,
	})
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
