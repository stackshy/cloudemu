// Package privateca implements the Google Certificate Authority Service control
// plane (privateca.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/privateca/v1 clients, gcloud, and the Terraform google
// provider's google_privateca_ca_pool, google_privateca_certificate_authority,
// google_privateca_certificate_template, and google_privateca_certificate
// resources — all GA in the stable hashicorp/google provider at the /v1/ base
// path — hit this handler unchanged.
//
// Coverage (CA-pool + certificate-authority + certificate-template + certificate
// control plane):
//
//	POST   /v1/…/caPools?caPoolId=                                   — CreateCaPool (LRO)
//	GET/PATCH/DELETE /v1/…/caPools/{id}                              — Get/Patch/Delete (LRO)
//	POST   /v1/…/caPools/{p}/certificateAuthorities?…AuthorityId=    — Create (LRO)
//	…/certificateAuthorities/{ca}:enable|:disable|:undelete|:activate — CA state machine (LRO)
//	…/certificateAuthorities/{ca}:fetch                             — FetchCsr (synchronous)
//	POST   /v1/…/certificateTemplates?certificateTemplateId=        — Create (LRO)
//	POST   /v1/…/caPools/{p}/certificates?certificateId=            — Create (synchronous)
//	…/certificates/{c}:revoke                                       — Revoke (synchronous)
//	GET    /v1/…/operations/{op}                                    — Operations.Get (shared poller)
//
// Every mutating caPool / certificateAuthority / certificateTemplate RPC returns a
// google.longrunning.Operation with done=true and the resulting resource embedded
// in `response`; certificate create/patch/revoke and CA :fetch complete
// synchronously and return the resource directly, matching the real API.
//
// Location-scoped operations live under /v1/projects/{p}/locations/{l}/operations
// — the SAME space the shared GCP LRO poller owns. Matches returns false for
// operation paths when a shared registry is wired, letting that poller win; a
// standalone package server (no registry) serves its own polls. The
// caPools/certificateTemplates/operations resource-type guard keeps this handler
// disjoint from every other /v1/projects/ handler, including certificatemanager's
// location-level certificates (this service's certificates are nested under a
// caPool and never collide).
package privateca

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	pcadriver "github.com/stackshy/cloudemu/v2/services/privateca/driver"
)

const (
	pathPrefix    = "/v1/projects/"
	projectsSeg   = "projects"
	locationsSeg  = "locations"
	operationsSeg = "operations"

	caPoolsColl      = "caPools"
	authoritiesColl  = "certificateAuthorities"
	templatesColl    = "certificateTemplates"
	certificatesColl = "certificates"

	minResourceParts = 4 // [projects, {p}, locations, {l}]

	// Rest-segment counts under the locations scope, used by the route parsers.
	opNameMinParts  = 2 // [operations, {op}]
	flatItemParts   = 2 // [templates, {id}]
	poolItemParts   = 2 // [caPools, {pool}]
	nestedCollParts = 3 // [caPools, {pool}, {nestedColl}]
	nestedItemParts = 4 // [caPools, {pool}, {nestedColl}, {id}[:verb]]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, createTime, updateTime).
	minComputedFields = 3

	verbFetch = "fetch"

	caPoolTypeURL    = "type.googleapis.com/google.cloud.security.privateca.v1.CaPool"
	authorityTypeURL = "type.googleapis.com/google.cloud.security.privateca.v1.CertificateAuthority"
	templateTypeURL  = "type.googleapis.com/google.cloud.security.privateca.v1.CertificateTemplate"
	certTypeURL      = "type.googleapis.com/google.cloud.security.privateca.v1.Certificate"
)

// meta binds a resource collection's wire identity (path segment, create id query
// param, LRO response @type) to its shape flags.
type meta struct {
	coll       string
	idParam    string // camelCase create id query param
	idParamAlt string // snake_case create id query param (the Terraform provider sends this)
	typeURL    string
	nested     bool // addressed under a caPool
}

// metas returns the collection descriptors keyed by path segment.
func metas() map[string]meta {
	return map[string]meta{
		caPoolsColl:      {caPoolsColl, "caPoolId", "ca_pool_id", caPoolTypeURL, false},
		authoritiesColl:  {authoritiesColl, "certificateAuthorityId", "certificate_authority_id", authorityTypeURL, true},
		templatesColl:    {templatesColl, "certificateTemplateId", "certificate_template_id", templateTypeURL, false},
		certificatesColl: {certificatesColl, "certificateId", "certificate_id", certTypeURL, true},
	}
}

// Handler serves privateca.googleapis.com v1 requests against a PrivateCA driver.
type Handler struct {
	db pcadriver.PrivateCA

	// ops records created operations with the shared poller so a client that polls
	// the returned operation name gets the typed response (and unknown names 404).
	// Nil in a standalone package server, where this handler serves its own
	// /operations/ poll.
	ops *lro.Registry
}

// New returns a Certificate Authority Service handler backed by db.
func New(db pcadriver.PrivateCA) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of a privateca v1 path.
type route struct {
	project  string
	location string
	coll     string // caPools | certificateAuthorities | certificateTemplates | certificates | operations
	pool     string // parent pool id for a nested collection
	name     string // resource id (verb stripped) or operation name tail
	verb     string // enable | disable | undelete | fetch | activate | revoke | ""
}

// parseRoute extracts the components of a privateca v1 path. It recognizes only
// the caPools subtree, certificateTemplates, and operations under a locations
// scope.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}
	rest := parts[minResourceParts:]

	if len(rest) == 0 {
		return route{}, false
	}

	var ok bool

	switch rest[0] {
	case operationsSeg:
		ok = parseOperations(&rt, rest)
	case caPoolsColl:
		ok = parsePoolSubtree(&rt, rest)
	case templatesColl:
		ok = parseFlat(&rt, rest, templatesColl)
	}

	if !ok {
		return route{}, false
	}

	return rt, true
}

// parseOperations parses /operations/{op...}. The operation name may contain
// slashes, so everything after the segment is the name.
func parseOperations(rt *route, rest []string) bool {
	if len(rest) < opNameMinParts {
		return false
	}

	rt.coll = operationsSeg
	rt.name = strings.Join(rest[1:], "/")

	return true
}

// parseFlat parses a location-scoped collection with no verbs (certificateTemplates):
// [coll] is the collection, [coll, {id}] an item.
func parseFlat(rt *route, rest []string, coll string) bool {
	rt.coll = coll

	switch len(rest) {
	case 1:
		return true
	case flatItemParts:
		if strings.ContainsRune(rest[1], ':') {
			return false
		}

		rt.name = rest[1]

		return true
	default:
		return false
	}
}

// parsePoolSubtree parses the caPools subtree: the pool collection/item and the
// nested certificateAuthorities / certificates collections/items (with optional
// trailing :verb on an item).
func parsePoolSubtree(rt *route, rest []string) bool {
	switch len(rest) {
	case 1:
		rt.coll = caPoolsColl

		return true
	case poolItemParts:
		if strings.ContainsRune(rest[1], ':') {
			return false
		}

		rt.coll, rt.name = caPoolsColl, rest[1]

		return true
	case nestedCollParts:
		if !isNestedColl(rest[2]) {
			return false
		}

		rt.coll, rt.pool = rest[2], rest[1]

		return true
	case nestedItemParts:
		if !isNestedColl(rest[2]) {
			return false
		}

		rt.coll, rt.pool = rest[2], rest[1]
		rt.name, rt.verb = splitVerb(rest[3])

		return true
	default:
		return false
	}
}

// isNestedColl reports whether seg names a collection nested under a caPool.
func isNestedColl(seg string) bool {
	return seg == authoritiesColl || seg == certificatesColl
}

// splitVerb separates a trailing ":verb" from a resource id (…/{ca}:enable).
func splitVerb(seg string) (name, verb string) {
	if i := strings.IndexByte(seg, ':'); i >= 0 {
		return seg[:i], seg[i+1:]
	}

	return seg, ""
}

// Matches claims /v1/projects/{p}/locations/{l}/{caPools|certificateTemplates|
// operations}[/…] paths. The resource-segment guard keeps it disjoint from every
// other /v1/projects/ handler. An operations path is claimed only when this
// handler has no shared LRO registry (a standalone package server); in an
// assembled server the shared poller owns it.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	if rt.coll == operationsSeg && h.ops != nil {
		return false
	}

	return true
}

// ServeHTTP routes on the parsed path, verb, and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Certificate Authority Service path")
		return
	}

	if rt.coll == operationsSeg {
		h.serveOperation(w, r)
		return
	}

	if rt.verb != "" {
		h.serveVerb(w, r, &rt)
		return
	}

	if rt.name == "" {
		h.serveCollection(w, r, &rt)
		return
	}

	h.serveItem(w, r, &rt)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodPost:
		h.createResource(w, r, rt)
	case http.MethodGet:
		h.listResources(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem dispatches item-level requests (get, patch, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.getResource(w, r, rt)
	case http.MethodPatch:
		h.patchResource(w, r, rt)
	case http.MethodDelete:
		h.deleteResource(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
