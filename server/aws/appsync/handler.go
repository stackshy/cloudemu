// Package appsync implements the AWS AppSync control-plane API (REST-JSON,
// awsRestjson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/appsync client (or the `aws appsync` CLI, or the
// aws_appsync_* Terraform resources) at a Server registered with this handler
// and the GraphQL-API, data-source, and API-key operations work end-to-end
// against an in-memory driver.
//
// AppSync routes by HTTP verb + path under the /v1/ prefix (e.g.
// POST /v1/apis, GET /v1/apis/{apiId}/datasources/{name}); there is no
// X-Amz-Target header. Matches claims only /v1/apis paths and /v1/tags paths
// carrying an AppSync ARN, so it never shadows the S3 catch-all and is disjoint
// from the other /v1/ handlers (Batch, MSK).
package appsync

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// apiPrefix is the version path prefix every AppSync operation lives under.
const apiPrefix = "/v1/"

// Path roots below the version prefix.
const (
	rootAPIs = "apis"
	rootTags = "tags"
)

// Sub-resource segment literals.
const (
	segDataSources = "datasources"
	segAPIKeys     = "apikeys"
)

// Handler serves AWS AppSync requests against a driver.
type Handler struct {
	as driver.AppSync
}

// New returns an AppSync handler backed by d.
func New(d driver.AppSync) *Handler {
	return &Handler{as: d}
}

// Matches claims /v1/apis paths and /v1/tags paths whose ARN is an AppSync ARN.
// The /v1/tags root is shared with other restJson1 services (Batch, MSK), so it
// is claimed only for AppSync ARNs; a non-AppSync ARN falls through.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(escapedPath(r), apiPrefix) {
		return false
	}

	segs := splitPath(strings.TrimPrefix(escapedPath(r), apiPrefix))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootAPIs:
		return true
	case rootTags:
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), ":appsync:")
	default:
		return false
	}
}

// ServeHTTP dispatches an AppSync request on its path shape below the prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(strings.TrimPrefix(escapedPath(r), apiPrefix))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootAPIs:
		h.serveAPIs(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveAPIs routes /v1/apis and its nested sub-trees.
func (h *Handler) serveAPIs(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		h.serveAPICollection(w, r)

		return
	}

	apiID := rest[0]

	if len(rest) == 1 {
		h.serveAPIItem(w, r, apiID)

		return
	}

	switch rest[1] {
	case segDataSources:
		h.serveDataSources(w, r, apiID, rest[2:])
	case segAPIKeys:
		h.serveAPIKeys(w, r, apiID, rest[2:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveAPICollection handles POST (create) and GET (list) on /v1/apis.
func (h *Handler) serveAPICollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createGraphqlAPI(w, r)
	case http.MethodGet:
		h.listGraphqlAPIs(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveAPIItem handles GET/POST/DELETE on /v1/apis/{apiId}.
func (h *Handler) serveAPIItem(w http.ResponseWriter, r *http.Request, apiID string) {
	switch r.Method {
	case http.MethodGet:
		h.getGraphqlAPI(w, r, apiID)
	case http.MethodPost:
		h.updateGraphqlAPI(w, r, apiID)
	case http.MethodDelete:
		h.deleteGraphqlAPI(w, r, apiID)
	default:
		methodNotAllowed(w)
	}
}

// escapedPath returns the request path preserving percent-encoding so a path
// label that contains a slash (an ARN) survives as one segment.
func escapedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}

	return r.URL.EscapedPath()
}

// splitPath splits a URL path into its non-empty segments, percent-decoding
// each segment so an ARN or name label is delivered whole to a handler.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))

	for _, seg := range raw {
		if dec, err := url.PathUnescape(seg); err == nil {
			out = append(out, dec)
		} else {
			out = append(out, seg)
		}
	}

	return out
}

// pageFromQuery reads nextToken/maxResults from the query string.
func pageFromQuery(r *http.Request) driver.Page {
	q := r.URL.Query()

	return driver.Page{
		NextToken:  q.Get("nextToken"),
		MaxResults: atoiDefault(q.Get("maxResults"), 0),
	}
}

// atoiDefault parses s as an int32, returning def when s is empty or invalid.
func atoiDefault(s string, def int32) int32 {
	if s == "" {
		return def
	}

	n := 0

	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}

		n = n*10 + int(c-'0')
	}

	return int32(n)
}
