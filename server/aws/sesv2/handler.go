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
	"net/url"
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

	rootContactLists = "contact-lists"
	rootCVTemplates  = "custom-verification-email-templates"
	rootIPPools      = "dedicated-ip-pools"
	rootDedicatedIps = "dedicated-ips"
	rootDashboard    = "deliverability-dashboard"
	rootImportJobs   = "import-jobs"
	rootExportJobs   = "export-jobs"
	rootListExport   = "list-export-jobs"
	rootMetrics      = "metrics"
	rootInsights     = "insights"
	rootAddrInsights = "email-address-insights"
	rootEndpoints    = "multi-region-endpoints"
	rootReputation   = "reputation"
	rootTenants      = "tenants"
	rootTenant       = "tenant"
	rootResources    = "resources"
	rootVDM          = "vdm"
	rootBulkOutbound = "outbound-bulk-emails"
	rootCVOutbound   = "outbound-custom-verification-emails"
)

// Repeated sub-path segment literals.
const (
	segList      = "list"
	segDelete    = "delete"
	segSending   = "sending"
	segWarmup    = "warmup"
	segCampaigns = "campaigns"
	segDkim      = "dkim"
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
func (h *Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		return false
	}

	segs := splitPath(strings.TrimPrefix(escapedPath(r), apiPrefix))
	if len(segs) == 0 {
		return false
	}

	_, ok := h.routes()[segs[0]]

	return ok
}

// ServeHTTP dispatches an SES v2 request on its path shape below the version prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(strings.TrimPrefix(escapedPath(r), apiPrefix))
	if len(segs) == 0 {
		notFound(w, r.URL.Path)

		return
	}

	if route, ok := h.routes()[segs[0]]; ok {
		route(w, r, segs[1:])

		return
	}

	notFound(w, r.URL.Path)
}

// subHandler serves a request against the path segments below a known root.
type subHandler func(w http.ResponseWriter, r *http.Request, rest []string)

// routes maps each known collection root to its sub-handler.
func (h *Handler) routes() map[string]subHandler {
	return map[string]subHandler{
		rootIdentities:   h.serveIdentities,
		rootConfigSets:   h.serveConfigSets,
		rootTemplates:    h.serveTemplates,
		rootOutbound:     h.serveOutbound,
		rootAccount:      h.serveAccount,
		rootSuppression:  h.serveSuppression,
		rootTags:         h.serveTags,
		rootContactLists: h.serveContactLists,
		rootCVTemplates:  h.serveCVTemplates,
		rootIPPools:      h.serveIPPools,
		rootDedicatedIps: h.serveDedicatedIps,
		rootDashboard:    h.serveDashboard,
		rootImportJobs:   h.serveImportJobs,
		rootExportJobs:   h.serveExportJobs,
		rootListExport:   h.serveListExportJobs,
		rootMetrics:      h.serveMetrics,
		rootInsights:     h.serveInsights,
		rootAddrInsights: h.serveAddrInsights,
		rootEndpoints:    h.serveEndpoints,
		rootReputation:   h.serveReputation,
		rootTenants:      h.serveTenants,
		rootTenant:       h.serveTenant,
		rootResources:    h.serveResources,
		rootVDM:          h.serveVDM,
		rootBulkOutbound: h.serveBulkOutbound,
		rootCVOutbound:   h.serveCVOutbound,
	}
}

// escapedPath returns the request path preserving percent-encoding so path
// labels that contain a slash (e.g. an ARN reference) survive as one segment.
func escapedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}

	return r.URL.EscapedPath()
}

// splitPath splits a URL path into its non-empty segments, percent-decoding
// each segment so a label like an ARN is delivered whole to a handler.
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
