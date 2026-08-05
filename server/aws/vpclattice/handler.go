// Package vpclattice implements the AWS VPC Lattice control-plane API
// (REST-JSON, awsRestjson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/vpclattice client at a Server registered with this
// handler and the operations work end-to-end against an in-memory driver.
//
// VPC Lattice uses path + HTTP-method routing (no X-Amz-Target). The Matches
// predicate claims the service's top-level collection prefixes so it does not
// shadow other handlers; ServeHTTP dispatches on the first path segment.
package vpclattice

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

// Handler serves VPC Lattice requests against a driver.
type Handler struct {
	lattice driver.VPCLattice
	routes  map[string]segmentHandler
}

// segmentHandler serves a request whose first path segment has been consumed;
// rest holds the remaining segments.
type segmentHandler func(w http.ResponseWriter, r *http.Request, rest []string)

// New returns a VPC Lattice handler backed by d.
func New(d driver.VPCLattice) *Handler {
	h := &Handler{lattice: d}
	h.routes = map[string]segmentHandler{
		"servicenetworks":                       h.serveServiceNetworks,
		"services":                              h.serveServices,
		"targetgroups":                          h.serveTargetGroups,
		"servicenetworkvpcassociations":         h.serveSNVpcAssociations,
		"servicenetworkvpcendpointassociations": h.serveSNVpcEndpointAssociations,
		"servicenetworkserviceassociations":     h.serveSNServiceAssociations,
		"servicenetworkresourceassociations":    h.serveSNResourceAssociations,
		"resourceconfigurations":                h.serveResourceConfigurations,
		"resourcegateways":                      h.serveResourceGateways,
		"resourceendpointassociations":          h.serveResourceEndpointAssociations,
		"accesslogsubscriptions":                h.serveAccessLogSubs,
		"authpolicy":                            h.serveAuthPolicy,
		"resourcepolicy":                        h.serveResourcePolicy,
		"domainverifications":                   h.serveDomainVerifications,
		"tags":                                  h.serveTags,
	}

	return h
}

// Matches claims requests whose first path segment belongs to VPC Lattice.
func (h *Handler) Matches(r *http.Request) bool {
	_, ok := h.routes[firstSegment(r.URL.Path)]

	return ok
}

// ServeHTTP dispatches on the first path segment via the routes table.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		notFound(w, r.URL.Path)

		return
	}

	if serve, ok := h.routes[segs[0]]; ok {
		serve(w, r, segs[1:])

		return
	}

	notFound(w, r.URL.Path)
}

// idHandler handles a request scoped to a single resource identifier.
type idHandler func(http.ResponseWriter, *http.Request, string)

// plainHandler handles a request with no path identifier.
type plainHandler func(http.ResponseWriter, *http.Request)

// routeCollection dispatches a collection path: POST→create, GET→list. A nil
// handler means the method is unsupported for that collection.
func routeCollection(w http.ResponseWriter, r *http.Request, create, list plainHandler) {
	switch {
	case r.Method == http.MethodPost && create != nil:
		create(w, r)
	case r.Method == http.MethodGet && list != nil:
		list(w, r)
	default:
		methodNotAllowed(w)
	}
}

// routeByID dispatches a resource path: GET→get, PATCH→update, DELETE→delete.
// A nil handler means the method is unsupported for that resource.
func routeByID(w http.ResponseWriter, r *http.Request, id string, get, update, del idHandler) {
	switch {
	case r.Method == http.MethodGet && get != nil:
		get(w, r, id)
	case r.Method == http.MethodPatch && update != nil:
		update(w, r, id)
	case r.Method == http.MethodDelete && del != nil:
		del(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// firstSegment returns the first non-empty path segment, or "".
func firstSegment(p string) string {
	segs := splitPath(p)
	if len(segs) == 0 {
		return ""
	}

	return segs[0]
}

// splitPath splits a URL path into its non-empty segments.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}
