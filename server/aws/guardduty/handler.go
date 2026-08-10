// Package guardduty implements the Amazon GuardDuty control-plane API
// (REST-JSON, awsRestjson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/guardduty client (or the `aws guardduty` CLI) at a
// Server registered with this handler and the operations work end-to-end
// against an in-memory driver.
//
// GuardDuty uses path + HTTP-method routing with NO API-version path prefix, so
// its Matches predicate gates on the first path segment being a known GuardDuty
// collection root. It MUST register before the S3 catch-all (see the doc on
// Matches). ServeHTTP dispatches on the path shape below the root.
package guardduty

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// Path roots GuardDuty owns. Matches claims a request only when its first path
// segment is one of these.
const (
	rootDetector              = "detector"
	rootAdmin                 = "admin"
	rootInvitation            = "invitation"
	rootTags                  = "tags"
	rootMalwareScan           = "malware-scan"
	rootMalwareScans          = "malware-scans"
	rootMalwareProtectionPlan = "malware-protection-plan"
	rootObjectMalwareScan     = "object-malware-scan"
	rootOrganization          = "organization"
)

// Sub-resource path segments shared across the GuardDuty route tables.
const (
	segGet          = "get"
	segDelete       = "delete"
	segStart        = "start"
	segStatistics   = "statistics"
	segDisassociate = "disassociate"
)

// Handler serves GuardDuty requests against a driver.
type Handler struct {
	gd driver.GuardDuty
}

// New returns a GuardDuty handler backed by d.
func New(d driver.GuardDuty) *Handler {
	return &Handler{gd: d}
}

// guarddutyARNPrefix is the service-qualified prefix of every GuardDuty ARN. It
// disambiguates the shared REST tag endpoint (/tags/{ResourceArn}) from other
// services (e.g. EKS) that use the same path with their own ARNs.
const guarddutyARNPrefix = "arn:aws:guardduty:"

// Matches claims requests whose first path segment is a known GuardDuty root.
//
// GuardDuty has no API-version path prefix, so this predicate is what gates
// GuardDuty ahead of the S3 REST catch-all: it MUST be registered before S3.
// Consequently an S3 bucket literally named one of the GuardDuty roots
// ("detector", "admin", "invitation", "malware-scan", "malware-scans",
// "malware-protection-plan", "object-malware-scan", "organization") would be
// shadowed by this handler. That collision is accepted and documented — these
// are unusual bucket names and full GuardDuty parity requires owning these
// paths.
//
// The /tags/{ResourceArn} endpoint is shared with other services (EKS uses the
// same shape), so for the "tags" root this only claims requests whose ARN is a
// GuardDuty ARN; other services' tag requests fall through to their handlers.
func (*Handler) Matches(r *http.Request) bool {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootTags:
		return len(segs) == 2 && strings.HasPrefix(segs[1], guarddutyARNPrefix)
	case rootDetector, rootAdmin, rootInvitation, rootMalwareScan,
		rootMalwareScans, rootMalwareProtectionPlan, rootObjectMalwareScan, rootOrganization:
		return true
	default:
		return false
	}
}

// ServeHTTP dispatches a GuardDuty request on its path shape.
//
//nolint:gocyclo // one dispatch arm per GuardDuty path root; the surface is large by API design.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootDetector:
		h.serveDetectorRoot(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	case rootMalwareProtectionPlan:
		h.serveMalwareProtectionPlan(w, r, segs[1:])
	case rootMalwareScan:
		h.serveMalwareScan(w, r, segs[1:])
	case rootMalwareScans:
		h.serveMalwareScans(w, r)
	case rootObjectMalwareScan:
		h.serveObjectMalwareScan(w, r, segs[1:])
	case rootAdmin:
		h.serveAdmin(w, r, segs[1:])
	case rootInvitation:
		h.serveInvitation(w, r, segs[1:])
	case rootOrganization:
		h.serveOrganization(w, r, segs[1:])
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

// splitPath splits a URL path into its non-empty segments, percent-decoding each
// segment so an ARN is delivered whole to a handler.
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

// readAll reads r fully. Named to keep the errors.go dependency explicit.
func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// pageFromQuery reads maxResults/nextToken from the query string.
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
