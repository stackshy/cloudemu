// Package spanner implements the Google Cloud Spanner admin REST API
// (spanner.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/spanner/v1 admin clients, gcloud, and the Terraform
// google provider configured with a custom endpoint (option.WithEndpoint /
// spanner_custom_endpoint) hit this handler unchanged.
//
// Coverage (instance + database control plane only):
//
//	POST   /v1/projects/{p}/instances                             — CreateInstance (LRO)
//	GET    /v1/projects/{p}/instances                             — ListInstances
//	GET    /v1/projects/{p}/instances/{i}                         — GetInstance
//	PATCH  /v1/projects/{p}/instances/{i}                         — UpdateInstance (LRO, body fieldMask)
//	DELETE /v1/projects/{p}/instances/{i}                         — DeleteInstance (sync)
//	POST   /v1/projects/{p}/instances/{i}/databases              — CreateDatabase (LRO)
//	GET    /v1/projects/{p}/instances/{i}/databases              — ListDatabases
//	GET    /v1/projects/{p}/instances/{i}/databases/{d}          — GetDatabase
//	DELETE /v1/projects/{p}/instances/{i}/databases/{d}          — DropDatabase (sync)
//	GET    /v1/projects/{p}/instances/{i}/databases/{d}/ddl      — GetDatabaseDdl
//	PATCH  /v1/projects/{p}/instances/{i}/databases/{d}/ddl      — UpdateDatabaseDdl (LRO)
//	GET    .../instances/{i}[/databases/{d}]/operations/{op}     — poll (always done)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource in `response`, and a created resource is READY
// immediately, so an SDK or Terraform LRO wait terminates on the first poll
// instead of hanging.
//
// Path collision with Cloud SQL: Spanner shares the /v1/projects/{p}/instances
// URL space with Cloud SQL (both real services live on the same collapsed host).
// Matches keeps the two disjoint by content, not URL alone — a create POST is
// claimed only when its body carries the Spanner CreateInstanceRequest shape
// ({instanceId, instance}); an item/sub-resource request is claimed only when
// this Spanner store owns the instance (the state-aware pattern GKE uses for its
// operations). The one path that cannot be told apart by content is the bare
// GET /v1/projects/{p}/instances list, which Spanner claims — Cloud SQL's
// Terraform/gcloud traffic uses the /sql/v1beta4 and bare /projects prefixes and
// is unaffected; only a raw sqladmin/v1 SDK client's ListInstances is superseded
// on a server that also runs Spanner.
package spanner

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	sp "google.golang.org/api/spanner/v1"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	spdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
)

const (
	pathPrefix   = "/v1/projects/"
	maxBodyBytes = 1 << 20

	segInstances  = "instances"
	segDatabases  = "databases"
	segOperations = "operations"
	segDdl        = "ddl"

	// Path-segment counts below the /v1/projects/ prefix.
	partsCollection = 2 // {p}/instances
	partsItem       = 3 // {p}/instances/{i}
	partsOperation  = 5 // {p}/instances/{i}/operations/{op}
	idxDatabases    = 3 // parts[3] == "databases" for a database-scoped path
)

// mapWire converts a slice of driver values to their wire form.
func mapWire[T any, W any](items []T, conv func(*T) W) []W {
	out := make([]W, 0, len(items))
	for i := range items {
		out = append(out, conv(&items[i]))
	}

	return out
}

// Handler serves Spanner admin requests against a spanner driver.
type Handler struct {
	db spdriver.Spanner
}

// New returns a Spanner admin handler backed by db.
func New(db spdriver.Spanner) *Handler { return &Handler{db: db} }

// trimParts splits the path below the /v1/projects/ prefix into its segments.
func trimParts(urlPath string) []string {
	return strings.Split(strings.TrimPrefix(urlPath, pathPrefix), "/")
}

// Matches claims the Spanner slice of /v1/projects/{p}/instances traffic while
// leaving Cloud SQL's untouched (see the package doc for the disambiguation).
func (h *Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	parts := trimParts(r.URL.Path)

	const idxResource = 1
	if len(parts) <= idxResource || parts[idxResource] != segInstances {
		return false
	}

	// Collection: /v1/projects/{p}/instances — list is Spanner's; a create POST
	// is Spanner's only when the body carries the CreateInstanceRequest shape.
	if len(parts) == idxResource+1 {
		switch r.Method {
		case http.MethodGet:
			return true
		case http.MethodPost:
			return bodyLooksLikeSpanner(r)
		default:
			return false
		}
	}

	// Item or sub-resource: claimed only when this Spanner store owns the
	// instance, so Cloud SQL's own instance traffic falls through.
	const idxInstanceID = 2

	instanceID := parts[idxInstanceID]
	if instanceID == "" {
		return false
	}

	return h.ownsInstance(r, parts[0], instanceID)
}

// ownsInstance reports whether this Spanner store holds the named instance.
func (h *Handler) ownsInstance(r *http.Request, project, instanceID string) bool {
	_, err := h.db.GetInstance(r.Context(), instanceName(project, instanceID))

	return err == nil
}

// bodyLooksLikeSpanner reports whether a POST /instances body is a Spanner
// CreateInstanceRequest (has instanceId or instance) rather than a Cloud SQL
// DatabaseInstance. It reads and restores the body so a fall-through to Cloud SQL
// still sees the full request.
func bodyLooksLikeSpanner(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return false
	}

	var probe struct {
		InstanceID string          `json:"instanceId"`
		Instance   json.RawMessage `json:"instance"`
	}

	if json.Unmarshal(raw, &probe) != nil {
		return false
	}

	return probe.InstanceID != "" || len(probe.Instance) > 0
}

// ServeHTTP routes a matched Spanner request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := trimParts(r.URL.Path)

	const idxResource = 1
	if len(parts) <= idxResource || parts[idxResource] != segInstances {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Spanner path")
		return
	}

	project := parts[0]

	switch {
	case len(parts) == partsCollection:
		h.serveInstanceCollection(w, r, project)
	case len(parts) == partsItem:
		h.serveInstanceItem(w, r, instanceName(project, parts[2]))
	case len(parts) == partsOperation && parts[idxDatabases] == segOperations:
		h.serveOperation(w, r)
	case parts[idxDatabases] == segDatabases:
		h.serveDatabases(w, r, instanceName(project, parts[2]), parts[partsItem+1:])
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Spanner path")
	}
}

// serveOperation resolves a (done) long-running operation poll. The operation
// resource name is the request path without the /v1/ version prefix.
//
// The completed operation re-embeds the affected resource in `response` (fetched
// live from the store), because clients such as the Terraform google provider
// re-poll the operation by name after create and read that response to populate
// the resource — a done operation with an empty response makes them fail with
// "`resource` not set in operation response".
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/v1/")

	op, err := h.db.GetOperation(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, h.operationResponse(r, name)))
}

// operationResponse re-fetches the resource an operation acted on so a poll can
// replay it. The operation name is ".../operations/{op}"; the segment before
// "/operations/" is the resource name — a database when it contains
// "/databases/", otherwise an instance. A resource already deleted yields nil.
func (h *Handler) operationResponse(r *http.Request, opName string) any {
	idx := strings.Index(opName, "/"+segOperations+"/")
	if idx < 0 {
		return nil
	}

	resource := opName[:idx]

	if strings.Contains(resource, "/"+segDatabases+"/") {
		if db, err := h.db.GetDatabase(r.Context(), resource); err == nil {
			return toWireDatabase(db)
		}

		return nil
	}

	if inst, err := h.db.GetInstance(r.Context(), resource); err == nil {
		return toWireInstance(inst)
	}

	return nil
}

// instanceName builds the full instance resource name.
func instanceName(project, instanceID string) string {
	return "projects/" + project + "/instances/" + instanceID
}

// doneOperation builds a completed LRO envelope carrying response as its typed
// result, so an SDK or Terraform caller observes a terminal operation at once.
func doneOperation(name string, response any) *sp.Operation {
	op := &sp.Operation{Name: name, Done: true}

	if response != nil {
		if raw, err := json.Marshal(response); err == nil {
			op.Response = raw
		}
	}

	return op
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
