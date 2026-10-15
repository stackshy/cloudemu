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
//	POST   /2020-05-31/distribution[?WithTags]          CreateDistribution[WithTags]
//	GET    /2020-05-31/distribution                     ListDistributions
//	GET    /2020-05-31/distribution/{Id}                GetDistribution
//	DELETE /2020-05-31/distribution/{Id}                DeleteDistribution (If-Match)
//	GET    /2020-05-31/distribution/{Id}/config         GetDistributionConfig
//	PUT    /2020-05-31/distribution/{Id}/config         UpdateDistribution (If-Match)
//	POST   /2020-05-31/distribution/{Id}/invalidation   CreateInvalidation
//	GET    /2020-05-31/distribution/{Id}/invalidation   ListInvalidations
//	GET    /2020-05-31/distribution/{Id}/invalidation/{InvId}   GetInvalidation
//	GET    /2020-05-31/tagging?Resource=<arn>           ListTagsForResource
//	POST   /2020-05-31/tagging?Operation=Tag&Resource=<arn>     TagResource
//	POST   /2020-05-31/tagging?Operation=Untag&Resource=<arn>   UntagResource
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
	// accountID is the account this handler serves; a tagging ARN of another
	// account names no resource here. Empty skips that check.
	accountID string
}

// Option configures a Handler.
type Option func(*Handler)

// WithAccount sets the account this handler serves.
func WithAccount(accountID string) Option {
	return func(h *Handler) { h.accountID = accountID }
}

// New returns a CloudFront handler backed by d.
func New(d cfdriver.CloudFront, opts ...Option) *Handler {
	h := &Handler{cf: d}
	for _, o := range opts {
		o(h)
	}

	return h
}

// Matches claims CloudFront's own REST path space, which is disjoint from every
// other AWS handler. Registered before the S3 REST fallback so those paths aren't
// swallowed by the catch-all.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return p == distPrefix ||
		strings.HasPrefix(p, distPrefix+"/") ||
		p == taggingPrefix ||
		strings.HasPrefix(p, taggingPrefix+"/")
}

// ServeHTTP runs the operation classify names, or writes the error of a
// request it cannot name.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op, a := classify(r)

	switch op {
	case opCreateDistribution:
		h.createDistribution(w, r)
	case opCreateDistributionWithTags:
		h.createDistributionWithTags(w, r)
	case opListDistributions:
		h.listDistributions(w, r)
	case opGetDistribution:
		h.getDistribution(w, r, a.id)
	case opDeleteDistribution:
		h.deleteDistribution(w, r, a.id)
	case opGetDistributionConfig:
		h.getDistributionConfig(w, r, a.id)
	case opUpdateDistribution:
		h.updateDistribution(w, r, a.id)
	default:
		if !h.serveSubOp(w, r, op, &a) {
			writeUnknown(w, &a)
		}
	}
}

// serveSubOp runs op when it is an invalidation or tagging operation.
func (h *Handler) serveSubOp(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) bool {
	switch op {
	case opCreateInvalidation:
		h.createInvalidation(w, r, a.id)
	case opListInvalidations:
		h.listInvalidations(w, r, a.id)
	case opGetInvalidation:
		h.getInvalidation(w, r, a.id, a.invalidationID)
	case opListTagsForResource, opTagResource, opUntagResource:
		h.serveTagging(w, r, op, a)
	default:
		return false
	}

	return true
}

// writeUnknown writes the error of a request classify cannot name. None of
// these branches reads or changes state.
func writeUnknown(w http.ResponseWriter, a *opArgs) {
	switch a.fail {
	case failPath:
		writeError(w, http.StatusNotFound, "NoSuchResource", "the specified resource does not exist")
	case failTagging:
		writeError(w, http.StatusBadRequest, "InvalidArgument", "unsupported tagging operation")
	case failMethod:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return serviceName }

// WriteAccessDenied writes the 403 this service returns when IAM denies a
// call, in its own XML error shape.
func (*Handler) WriteAccessDenied(w http.ResponseWriter, _ *http.Request, msg string) {
	writeError(w, http.StatusForbidden, "AccessDenied", msg)
}
