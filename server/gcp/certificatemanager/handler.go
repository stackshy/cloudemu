// Package certificatemanager implements the Google Certificate Manager control
// plane (certificatemanager.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/certificatemanager/v1 clients, gcloud, and the Terraform
// google provider's google_certificate_manager_certificate,
// google_certificate_manager_certificate_map, and
// google_certificate_manager_dns_authorization resources hit this handler
// unchanged.
//
// Coverage (certificate + certificate-map + dns-authorization control plane):
//
//	POST   /v1/…/certificates?certificateId=            — CreateCertificate (LRO)
//	GET    /v1/…/certificates                           — ListCertificates
//	GET    /v1/…/certificates/{id}                      — GetCertificate
//	PATCH  /v1/…/certificates/{id}?updateMask=          — PatchCertificate (LRO)
//	DELETE /v1/…/certificates/{id}                      — DeleteCertificate (LRO)
//	POST   /v1/…/certificateMaps?certificateMapId=      — CreateCertificateMap (LRO)
//	…                                                    — Get/List/Patch/Delete (as above)
//	POST   /v1/…/dnsAuthorizations?dnsAuthorizationId=  — CreateDNSAuthorization (LRO)
//	…                                                    — Get/List/Patch/Delete (as above)
//	GET    /v1/…/operations/{op}                        — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response`, so an SDK or Terraform LRO wait
// terminates on the first poll instead of hanging.
//
// Location-scoped operations: Certificate Manager's operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The certificates/certificateMaps/dnsAuthorizations
// resource-type guard keeps this handler disjoint from every other
// /v1/projects/ handler (Composer's environments, Cloud Deploy's pipelines/
// targets, Datastream's streams, Scheduler's jobs, …).
package certificatemanager

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	cmdriver "github.com/stackshy/cloudemu/v2/services/certificatemanager/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	certificatesColl = "certificates"
	certMapsColl     = "certificateMaps"
	dnsAuthsColl     = "dnsAuthorizations"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, createTime, updateTime).
	minComputedFields = 3

	certificateTypeURL = "type.googleapis.com/google.cloud.certificatemanager.v1.Certificate"
	certMapTypeURL     = "type.googleapis.com/google.cloud.certificatemanager.v1.CertificateMap"
	dnsAuthTypeURL     = "type.googleapis.com/google.cloud.certificatemanager.v1.DnsAuthorization"
)

// collection describes one Certificate Manager resource collection, binding its
// wire identity to the driver methods that back it. The three collections share
// every CRUD code path, differing only in these bound values and a small set of
// computed-field hooks.
type collection struct {
	seg     string // "certificates" | "certificateMaps" | "dnsAuthorizations"
	idParam string // "certificateId" | "certificateMapId" | "dnsAuthorizationId"
	typeURL string

	// validate rejects a create body that violates a collection invariant (a
	// certificate's managed/self_managed oneof). Nil where none applies.
	validate func(map[string]json.RawMessage) error
	// seed injects computed body values minted once at create so a later GET
	// reports them stably (a dnsAuthorization's dnsResourceRecord, a managed
	// certificate's state). Nil where none applies.
	seed func(map[string]json.RawMessage)
	// stripOutput removes write-only body values from a rendered resource (a
	// self-managed certificate's pemPrivateKey), matching the real API, which
	// stores but never echoes them. Nil where none applies.
	stripOutput func(map[string]json.RawMessage)

	create func(context.Context, *cmdriver.Config) (*cmdriver.Resource, *cmdriver.Operation, error)
	get    func(context.Context, string, string, string) (*cmdriver.Resource, error)
	list   func(context.Context, string, string) ([]cmdriver.Resource, error)
	patch  func(context.Context, *cmdriver.Config, []string) (*cmdriver.Resource, *cmdriver.Operation, error)
	del    func(context.Context, string, string, string) (*cmdriver.Operation, error)
}

// Handler serves certificatemanager.googleapis.com v1 requests against a
// CertificateManager driver.
type Handler struct {
	db cmdriver.CertificateManager

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Certificate Manager handler backed by db.
func New(db cmdriver.CertificateManager) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// collections binds the three resource collections to h's driver method values,
// keyed by their path segment.
func (h *Handler) collections() map[string]*collection {
	return map[string]*collection{
		certificatesColl: {
			seg: certificatesColl, idParam: "certificateId", typeURL: certificateTypeURL,
			validate: validateCertificate, seed: seedCertificate, stripOutput: stripCertificate,
			create: h.db.CreateCertificate, get: h.db.GetCertificate, list: h.db.ListCertificates,
			patch: h.db.PatchCertificate, del: h.db.DeleteCertificate,
		},
		certMapsColl: {
			seg: certMapsColl, idParam: "certificateMapId", typeURL: certMapTypeURL,
			create: h.db.CreateCertificateMap, get: h.db.GetCertificateMap, list: h.db.ListCertificateMaps,
			patch: h.db.PatchCertificateMap, del: h.db.DeleteCertificateMap,
		},
		dnsAuthsColl: {
			seg: dnsAuthsColl, idParam: "dnsAuthorizationId", typeURL: dnsAuthTypeURL,
			seed:   seedDNSAuthorization,
			create: h.db.CreateDNSAuthorization, get: h.db.GetDNSAuthorization, list: h.db.ListDNSAuthorizations,
			patch: h.db.PatchDNSAuthorization, del: h.db.DeleteDNSAuthorization,
		},
	}
}

// route holds the parsed components of a Certificate Manager v1 path.
type route struct {
	project  string
	location string
	resource string // "certificates" | "certificateMaps" | "dnsAuthorizations" | "operations"
	name     string // resource id or operation id; empty for the collection
}

// parseRoute extracts the components of a Certificate Manager v1 path. It
// recognizes only the certificates, certificateMaps, dnsAuthorizations, and
// operations resources under a locations scope.
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
	return seg == certificatesColl || seg == certMapsColl || seg == dnsAuthsColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{certificates|certificateMaps|
// dnsAuthorizations|operations}[/…] paths. The resource-segment guard keeps it
// disjoint from the other /v1/projects/ handlers. An operations path is claimed
// only when this handler has no shared LRO registry (a standalone package
// server); in an assembled server the shared poller owns it.
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
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Certificate Manager path")
		return
	}

	if rt.resource == operationsSeg {
		h.serveOperation(w, r)
		return
	}

	col := h.collections()[rt.resource]
	if col == nil {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported resource: "+rt.resource)
		return
	}

	if rt.name == "" {
		h.serveCollection(w, r, rt, col)
		return
	}

	h.serveItem(w, r, rt, col)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	switch r.Method {
	case http.MethodPost:
		h.createResource(w, r, rt, col)
	case http.MethodGet:
		h.listResources(w, r, rt, col)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem dispatches item-level requests (get, patch, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	switch r.Method {
	case http.MethodGet:
		h.getResource(w, r, rt, col)
	case http.MethodPatch:
		h.patchResource(w, r, rt, col)
	case http.MethodDelete:
		h.deleteResource(w, r, rt, col)
	default:
		writeMethodNotAllowed(w)
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
