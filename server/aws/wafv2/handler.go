// Package wafv2 implements the AWS WAFv2 JSON 1.1 protocol as a server.Handler.
// Point the real aws-sdk-go-v2/service/wafv2 client (or the `aws wafv2` CLI) at
// a Server registered with this handler and web-ACL, IP-set, rule-group and
// regex-pattern-set operations run against an in-memory WAFv2 driver.
//
// WAFv2 uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "AWSWAF_20190729.").
package wafv2

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	wafdriver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

const targetPrefix = "AWSWAF_20190729."

// Handler serves WAFv2 JSON-RPC requests against a WAFv2 driver.
type Handler struct {
	waf    wafdriver.WAFV2
	routes map[string]http.HandlerFunc
}

// New returns a WAFv2 handler backed by d.
func New(d wafdriver.WAFV2) *Handler {
	h := &Handler{waf: d}
	h.routes = map[string]http.HandlerFunc{
		"CreateWebACL": h.createWebACL,
		"GetWebACL":    h.getWebACL,
		"UpdateWebACL": h.updateWebACL,
		"DeleteWebACL": h.deleteWebACL,
		"ListWebACLs":  h.listWebACLs,

		"CreateIPSet": h.createIPSet,
		"GetIPSet":    h.getIPSet,
		"UpdateIPSet": h.updateIPSet,
		"DeleteIPSet": h.deleteIPSet,
		"ListIPSets":  h.listIPSets,

		"CreateRuleGroup": h.createRuleGroup,
		"GetRuleGroup":    h.getRuleGroup,
		"UpdateRuleGroup": h.updateRuleGroup,
		"DeleteRuleGroup": h.deleteRuleGroup,
		"ListRuleGroups":  h.listRuleGroups,

		"CreateRegexPatternSet": h.createRegexSet,
		"GetRegexPatternSet":    h.getRegexSet,
		"UpdateRegexPatternSet": h.updateRegexSet,
		"DeleteRegexPatternSet": h.deleteRegexSet,
		"ListRegexPatternSets":  h.listRegexSets,

		"AssociateWebACL":        h.associateWebACL,
		"DisassociateWebACL":     h.disassociateWebACL,
		"GetWebACLForResource":   h.getWebACLForResource,
		"ListResourcesForWebACL": h.listResourcesForWebACL,

		"TagResource":         h.tagResource,
		"UntagResource":       h.untagResource,
		"ListTagsForResource": h.listTagsForResource,
	}

	return h
}

// Matches returns true for WAFv2-shaped requests (X-Amz-Target of
// "AWSWAF_20190729.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches WAFv2 operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported WAFv2 operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error).
func dispatch[Req any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	call func(*Handler, context.Context, *Req) (any, error),
) {
	var req Req
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := call(h, r.Context(), &req)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, out)
}

// writeErr maps a driver error to the closest WAFv2 JSON error type. Errors
// tagged with a specific WAFv2 exception (via driver.APIError) take precedence
// so distinct exceptions surface as themselves rather than a generic code map.
func writeErr(w http.ResponseWriter, err error) {
	var apiErr *wafdriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, err.Error())

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "WAFNonexistentItemException", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "WAFDuplicateItemException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "WAFInvalidParameterException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "WAFOptimisticLockException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "WAFInternalErrorException", err.Error())
	}
}
