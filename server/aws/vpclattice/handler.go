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

// Matches claims a request only when its path root, HTTP method, and segment
// shape all correspond to a real VPC Lattice route. This is deliberately strict
// so that path-style S3 requests against a bucket literally named like a Lattice
// root (e.g. `PUT /services/key`, `GET /tags`) are NOT hijacked and instead fall
// through to the S3 catch-all handler registered after this one.
//
// A residual ambiguity remains for verbs Lattice and S3 share on an identical
// path (e.g. `GET /services` — list-services vs. S3 list-bucket-"services"),
// which is unavoidable for two REST services co-located on one endpoint.
func (h *Handler) Matches(r *http.Request) bool {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		return false
	}

	if _, ok := h.routes[segs[0]]; !ok {
		return false
	}

	return latticeClaims(r.Method, segs)
}

// latticeClaims reports whether (method, segments) maps to a served route.
//
// The resource-scoped forms additionally require the identifier segment to look
// like a VPC Lattice identifier (a known ID prefix or a vpc-lattice ARN). This
// is what lets a path-style S3 object op on a bucket named exactly like a
// Lattice root — e.g. `GET /services/mykey`, `DELETE /targetgroups/mykey` — fall
// through to the S3 catch-all instead of being mis-claimed here.
func latticeClaims(method string, segs []string) bool {
	rest := segs[1:]

	switch segs[0] {
	case "authpolicy", "resourcepolicy":
		return claimsPolicyRoot(method, rest)
	case "tags":
		return claimsTagsRoot(method, rest)
	default:
		return claimsResourceRoot(method, rest)
	}
}

// claimsPolicyRoot: /authpolicy|/resourcepolicy/{resourceIdentifier} —
// PUT/GET/DELETE, identifier required and Lattice-shaped.
func claimsPolicyRoot(method string, rest []string) bool {
	return len(rest) >= 1 && isLatticeIdentifier(strings.Join(rest, "/")) &&
		(method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete)
}

// claimsTagsRoot: /tags/{resourceArn} — POST/GET/DELETE, ARN required.
func claimsTagsRoot(method string, rest []string) bool {
	return len(rest) >= 1 && isLatticeIdentifier(strings.Join(rest, "/")) &&
		(method == http.MethodGet || method == http.MethodPost || method == http.MethodDelete)
}

// claimsResourceRoot handles the collection/resource roots. A bare collection
// path is POST create / GET list — `GET /<root>` still overlaps an S3
// list-bucket on a like-named bucket, the one unavoidable residual for two REST
// services sharing an endpoint. A resource-scoped path is claimed only when the
// id segment is Lattice-shaped, so an S3 object key (e.g. "mykey") falls
// through to the S3 catch-all.
func claimsResourceRoot(method string, rest []string) bool {
	if len(rest) == 0 {
		return method == http.MethodPost || method == http.MethodGet
	}

	return isLatticeMethod(method) && isLatticeIdentifier(rest[0])
}

// isLatticeMethod reports whether m is one of the verbs the collection/resource
// routes use (PUT is used only by the policy roots, handled separately).
func isLatticeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodPost || m == http.MethodPatch || m == http.MethodDelete
}

// isLatticeIdentifier reports whether s looks like a VPC Lattice resource
// identifier — a generated ID prefix or a vpc-lattice ARN — rather than an
// arbitrary S3 object key. Used to keep resource-scoped routes from claiming
// path-style S3 requests on buckets named like a Lattice root.
func isLatticeIdentifier(s string) bool {
	if strings.Contains(s, "arn:aws:vpc-lattice") {
		return true
	}

	for _, p := range []string{
		"sn-", "svc-", "listener-", "rule-", "tg-",
		"snva-", "snsa-", "snra-", "rcfg-", "rgw-", "als-", "dv-", "rea-",
	} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}

	return false
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

// splitPath splits a URL path into its non-empty segments.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}
