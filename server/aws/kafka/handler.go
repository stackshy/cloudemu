// Package kafka implements the Amazon MSK (Managed Streaming for Apache Kafka)
// control-plane API (REST-JSON, awsRestjson1) as a server.Handler. Point the
// real aws-sdk-go-v2/service/kafka client (or the `aws kafka` CLI) at a Server
// registered with this handler and the operations work end-to-end against an
// in-memory driver.
//
// MSK uses path + HTTP-method routing under three version-path prefixes:
// /v1/, /api/v2/, and /replication/v1/. Matches claims a request only when its
// path starts with one of those prefixes AND the next segment is a known MSK
// collection root, so it is disjoint from every other handler and from the S3
// catch-all. (A hypothetical S3 bucket literally named "v1", "api", or
// "replication" whose first key segment collided with an MSK root would be
// shadowed; this is an accepted, documented limitation — such bucket names are
// not used by real workloads and MSK must register before S3.)
package kafka

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// Version-path prefixes every MSK operation lives under.
const (
	prefixV1          = "/v1/"
	prefixV2          = "/api/v2/"
	prefixReplication = "/replication/v1/"
)

// Collection roots referenced from more than one dispatch arm.
const (
	rootClusters   = "clusters"
	rootOperations = "operations"
)

// mskRoots returns the known MSK collection roots below a version prefix.
// Matches gates on these so the handler never claims an unrelated REST path.
func mskRoots() map[string]struct{} {
	return map[string]struct{}{
		rootClusters:                {},
		"configurations":            {},
		"vpc-connection":            {},
		"vpc-connections":           {},
		"tags":                      {},
		"kafka-versions":            {},
		"compatible-kafka-versions": {},
		rootOperations:              {},
		"replicators":               {},
	}
}

// Handler serves MSK requests against a driver.
type Handler struct {
	k driver.Kafka
}

// New returns an MSK handler backed by d.
func New(d driver.Kafka) *Handler {
	return &Handler{k: d}
}

// prefixOf returns the version prefix a path lives under, or "".
func prefixOf(path string) string {
	switch {
	case strings.HasPrefix(path, prefixReplication):
		return prefixReplication
	case strings.HasPrefix(path, prefixV2):
		return prefixV2
	case strings.HasPrefix(path, prefixV1):
		return prefixV1
	default:
		return ""
	}
}

// Matches claims requests under an MSK version prefix whose first segment below
// the prefix is a known MSK collection root.
func (*Handler) Matches(r *http.Request) bool {
	prefix := prefixOf(r.URL.Path)
	if prefix == "" {
		return false
	}

	segs := splitPath(strings.TrimPrefix(escapedPath(r), prefix))
	if len(segs) == 0 {
		return false
	}

	_, ok := mskRoots()[segs[0]]

	return ok
}

// ServeHTTP dispatches an MSK request on its version prefix and path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := escapedPath(r)

	prefix := prefixOf(path)
	if prefix == "" {
		notFoundPath(w, r.URL.Path)

		return
	}

	segs := splitPath(strings.TrimPrefix(path, prefix))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch prefix {
	case prefixReplication:
		h.routeReplication(w, r, segs)
	case prefixV2:
		h.routeV2(w, r, segs)
	default:
		h.routeV1(w, r, segs)
	}
}

// escapedPath returns the request path preserving percent-encoding so a path
// label containing a slash (e.g. an ARN) survives as one segment.
func escapedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}

	return r.URL.EscapedPath()
}

// splitPath splits a URL path into its non-empty segments, percent-decoding
// each so an ARN is delivered whole to a handler.
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

	return int32(n) //nolint:gosec // bounded by request query length; overflow not reachable.
}
