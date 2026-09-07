// Package vpcaccess implements the Google Serverless VPC Access control plane
// (vpcaccess.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/vpcaccess/v1 clients, gcloud, and the Terraform google
// provider's google_vpc_access_connector resource hit this handler unchanged.
//
// Coverage (connector control plane):
//
//	POST   /v1/…/connectors?connectorId=   — CreateConnector (LRO)
//	GET    /v1/…/connectors                — ListConnectors
//	GET    /v1/…/connectors/{id}           — GetConnector
//	PATCH  /v1/…/connectors/{id}?updateMask= — PatchConnector (LRO)
//	DELETE /v1/…/connectors/{id}           — DeleteConnector (LRO)
//	GET    /v1/…/operations/{op}           — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting connector embedded in `response` as an Any typed
// type.googleapis.com/google.cloud.vpcaccess.v1.Connector, so an SDK or Terraform
// LRO wait terminates on the first poll instead of hanging.
//
// Location-scoped operations: a connector's operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The connectors resource-type guard keeps this handler
// disjoint from every other /v1/projects/ handler (Composer's environments,
// Datastream's streams, Certificate Manager's certificates, Service Directory's
// namespaces, …).
package vpcaccess

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	vpcdriver "github.com/stackshy/cloudemu/v2/services/vpcaccess/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	connectorsColl   = "connectors"
	connectorIDParam = "connectorId"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output field (name).
	minComputedFields = 1

	connectorTypeURL = "type.googleapis.com/google.cloud.vpcaccess.v1.Connector"
)

// Handler serves vpcaccess.googleapis.com v1 requests against a VPCAccess driver.
type Handler struct {
	db vpcdriver.VPCAccess

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Serverless VPC Access handler backed by db.
func New(db vpcdriver.VPCAccess) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of a Serverless VPC Access v1 path.
type route struct {
	project  string
	location string
	resource string // "connectors" | "operations"
	name     string // connector id or operation id; empty for the collection
}

// parseRoute extracts the components of a Serverless VPC Access v1 path. It
// recognizes only the connectors and operations resources under a locations
// scope.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rest := parts[minResourceParts:]
	if len(rest) == 0 || len(rest) > itemParts || !knownResource(rest[0]) {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3], resource: rest[0]}
	if len(rest) == itemParts {
		rt.name = rest[1]
	}

	return rt, true
}

// knownResource reports whether seg is a resource collection this handler
// serves.
func knownResource(seg string) bool {
	return seg == connectorsColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{connectors|operations}[/…]
// paths. The resource-segment guard keeps it disjoint from the other
// /v1/projects/ handlers. An operations path is claimed only when this handler
// has no shared LRO registry (a standalone package server); in an assembled
// server the shared poller owns it.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	if rt.resource == operationsSeg && h.ops != nil {
		return false
	}

	return true
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Serverless VPC Access path")
		return
	}

	if rt.resource == operationsSeg {
		h.serveOperation(w, r)
		return
	}

	if rt.name == "" {
		h.serveCollection(w, r, rt)
		return
	}

	h.serveItem(w, r, rt)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.createConnector(w, r, rt)
	case http.MethodGet:
		h.listConnectors(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem dispatches item-level requests (get, patch, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getConnector(w, r, rt)
	case http.MethodPatch:
		h.patchConnector(w, r, rt)
	case http.MethodDelete:
		h.deleteConnector(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// resourceName builds the full connector resource name.
func resourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + connectorsColl + "/" + id
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
