// Package sesv2 implements the AWS SES v2 (Simple Email Service v2)
// control/data-plane API (REST-JSON, awsRestjson1) as a server.Handler. Point
// the real aws-sdk-go-v2/service/sesv2 client (or the `aws sesv2` CLI) at a
// Server registered with this handler and the operations work end-to-end
// against an in-memory driver.
//
// SES v2 uses path + HTTP-method routing under a fixed API-version prefix
// (/v2/email/...). Matches claims only that prefix, so it never shadows the S3
// catch-all; ServeHTTP dispatches on the path shape below the prefix.
package sesv2

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// apiPrefix is the SES v2 API-version path prefix every operation lives under.
const apiPrefix = "/v2/email/"

// twoSegments is the sub-path length for two-segment routes such as
// /templates/{name}/render and /suppression/addresses/{addr}.
const twoSegments = 2

// Path roots below the version prefix.
const (
	rootIdentities  = "identities"
	rootConfigSets  = "configuration-sets"
	rootTemplates   = "templates"
	rootOutbound    = "outbound-emails"
	rootAccount     = "account"
	rootSuppression = "suppression"
	rootTags        = "tags"
)

// Handler serves SES v2 requests against a driver.
type Handler struct {
	ses driver.SESV2
}

// New returns an SES v2 handler backed by d.
func New(d driver.SESV2) *Handler {
	return &Handler{ses: d}
}

// Matches claims requests under the SES v2 API-version prefix whose first
// segment is a known SES collection root. The version prefix makes this
// unambiguous with the S3 catch-all (no real bucket path begins with /v2/email/).
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		return false
	}

	segs := splitPath(strings.TrimPrefix(r.URL.Path, apiPrefix))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootIdentities, rootConfigSets, rootTemplates, rootOutbound,
		rootAccount, rootSuppression, rootTags:
		return true
	default:
		return false
	}
}

// ServeHTTP dispatches an SES v2 request on its path shape below the version prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(strings.TrimPrefix(r.URL.Path, apiPrefix))
	if len(segs) == 0 {
		notFound(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootIdentities:
		h.serveIdentities(w, r, segs[1:])
	case rootConfigSets:
		h.serveConfigSets(w, r, segs[1:])
	case rootTemplates:
		h.serveTemplates(w, r, segs[1:])
	case rootOutbound:
		h.serveOutbound(w, r, segs[1:])
	case rootAccount:
		h.serveAccount(w, r, segs[1:])
	case rootSuppression:
		h.serveSuppression(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	default:
		notFound(w, r.URL.Path)
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
