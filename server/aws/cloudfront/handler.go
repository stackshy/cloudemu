// Package cloudfront implements the AWS CloudFront REST/XML protocol (API
// version 2020-05-31) as a server.Handler. Point the real aws-sdk-go-v2
// CloudFront client, the aws CLI, or Terraform's aws_cloudfront_distribution at
// a Server registered with this handler and the distribution control plane works
// against the cloudfront driver.
//
// CloudFront is a REST/XML service rooted at /2020-05-31/distribution and
// /2020-05-31/tagging. Its path space is disjoint from every other AWS handler,
// but it must register before the permissive S3 REST fallback so its URLs aren't
// swallowed.
//
// Coverage (2020-05-31 REST):
//
//	POST   /2020-05-31/distribution[?WithTags]        — CreateDistribution[WithTags]
//	GET    /2020-05-31/distribution                   — ListDistributions
//	GET    /2020-05-31/distribution/{Id}              — GetDistribution
//	DELETE /2020-05-31/distribution/{Id}              — DeleteDistribution (If-Match)
//	GET    /2020-05-31/distribution/{Id}/config       — GetDistributionConfig
//	PUT    /2020-05-31/distribution/{Id}/config       — UpdateDistribution (If-Match)
//	POST   /2020-05-31/distribution/{Id}/invalidation — CreateInvalidation
//	GET    /2020-05-31/distribution/{Id}/invalidation — ListInvalidations
//	GET    /2020-05-31/distribution/{Id}/invalidation/{InvId} — GetInvalidation
//	GET    /2020-05-31/tagging?Resource=<arn>         — ListTagsForResource
//	POST   /2020-05-31/tagging?Operation=Tag&Resource=<arn>   — TagResource
//	POST   /2020-05-31/tagging?Operation=Untag&Resource=<arn> — UntagResource
package cloudfront

import (
	"net/http"
	"strings"

	cfdriver "github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

// distPrefix roots every CloudFront distribution REST URL.
const distPrefix = "/2020-05-31/distribution"

// taggingPrefix roots the CloudFront tagging API.
const taggingPrefix = "/2020-05-31/tagging"

// configSeg and invalidationSeg are the sub-resource path segments under a
// distribution.
const (
	configSeg       = "config"
	invalidationSeg = "invalidation"
)

// Handler serves CloudFront REST requests against a cloudfront driver.
type Handler struct {
	cf cfdriver.CloudFront
}

// New returns a CloudFront handler backed by d.
func New(d cfdriver.CloudFront) *Handler {
	return &Handler{cf: d}
}

// Matches claims CloudFront's own REST path space — disjoint from every other
// AWS handler. Registered before the S3 REST fallback so those paths aren't
// swallowed by the catch-all.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return p == distPrefix ||
		strings.HasPrefix(p, distPrefix+"/") ||
		p == taggingPrefix ||
		strings.HasPrefix(p, taggingPrefix+"/")
}

// ServeHTTP routes on the path tail and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == taggingPrefix || strings.HasPrefix(r.URL.Path, taggingPrefix+"/") {
		h.serveTagging(w, r)
		return
	}

	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, distPrefix), "/")
	if tail == "" {
		h.serveCollection(w, r)
		return
	}

	segs := strings.Split(tail, "/")
	h.serveDistribution(w, r, segs)
}

// serveCollection handles /2020-05-31/distribution (create + list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if r.URL.Query().Has("WithTags") {
			h.createDistributionWithTags(w, r)
			return
		}

		h.createDistribution(w, r)
	case http.MethodGet:
		h.listDistributions(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// serveDistribution handles /2020-05-31/distribution/{Id}[/config|/invalidation[/{InvId}]].
func (h *Handler) serveDistribution(w http.ResponseWriter, r *http.Request, segs []string) {
	id := segs[0]

	switch {
	case len(segs) == 1:
		h.serveDistributionRoot(w, r, id)
	case len(segs) == 2 && segs[1] == configSeg:
		h.serveDistributionConfig(w, r, id)
	case len(segs) == 2 && segs[1] == invalidationSeg:
		h.serveInvalidationCollection(w, r, id)
	case len(segs) == 3 && segs[1] == invalidationSeg:
		h.serveInvalidationItem(w, r, id, segs[2])
	default:
		writeError(w, http.StatusNotFound, "NoSuchResource", "the specified resource does not exist")
	}
}

func (h *Handler) serveDistributionRoot(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getDistribution(w, r, id)
	case http.MethodDelete:
		h.deleteDistribution(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) serveDistributionConfig(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getDistributionConfig(w, r, id)
	case http.MethodPut:
		h.updateDistribution(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}
