// Package opensearch implements the Amazon OpenSearch Service control-plane API
// (REST-JSON, awsRestjson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/opensearch client (or the `aws opensearch` CLI) at a
// Server registered with this handler and the operations work end-to-end
// against an in-memory driver.
//
// OpenSearch uses path + HTTP-method routing under the fixed API-version prefix
// /2021-01-01/. Matches claims only that prefix, so it never shadows the S3
// catch-all (no real bucket path begins with /2021-01-01/); ServeHTTP
// dispatches on the path shape below the prefix.
package opensearch

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// apiPrefix is the OpenSearch API-version path prefix every operation lives under.
const apiPrefix = "/2021-01-01/"

// Path roots below the version prefix.
const (
	rootOpenSearch  = "opensearch"
	rootTags        = "tags"
	rootTagsRemoval = "tags-removal"
	rootPackages    = "packages"
	rootDomain      = "domain"
)

// Repeated sub-path segment literals shared across route files.
const (
	segSearch       = "search"
	segDescribe     = "describe"
	segUpdate       = "update"
	segHistory      = "history"
	segVpcEndpoints = "vpcEndpoints"
)

// Handler serves OpenSearch requests against a driver.
type Handler struct {
	os driver.OpenSearch
}

// New returns an OpenSearch handler backed by d.
func New(d driver.OpenSearch) *Handler {
	return &Handler{os: d}
}

// Matches claims requests under the OpenSearch API-version prefix whose first
// segment is a known OpenSearch collection root. The version prefix makes this
// unambiguous with the S3 catch-all.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		return false
	}

	segs := splitPath(strings.TrimPrefix(escapedPath(r), apiPrefix))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootOpenSearch, rootTags, rootTagsRemoval, rootPackages, rootDomain:
		return true
	default:
		return false
	}
}

// ServeHTTP dispatches an OpenSearch request on its path shape below the prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(strings.TrimPrefix(escapedPath(r), apiPrefix))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootOpenSearch:
		h.serveOpenSearch(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r)
	case rootTagsRemoval:
		h.serveTagsRemoval(w, r)
	case rootPackages:
		h.servePackages(w, r, segs[1:])
	case rootDomain:
		h.serveDomainRoot(w, r, segs[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// escapedPath returns the request path preserving percent-encoding so path
// labels that contain a slash (e.g. an ARN) survive as one segment.
func escapedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}

	return r.URL.EscapedPath()
}

// splitPath splits a URL path into its non-empty segments, percent-decoding
// each segment so an ARN or version label is delivered whole to a handler.
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

// pageFromQuery reads NextToken/MaxResults from the query string.
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

	return int32(n) //nolint:gosec // bounded by request query length; overflow not reachable in practice.
}
