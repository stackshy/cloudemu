// Package servicequotas implements the AWS Service Quotas JSON 1.1 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/servicequotas client (or
// the `aws service-quotas` CLI) at a Server registered with this handler and
// quota queries run against a provider-agnostic quota registry (features/quota):
// GetServiceQuota / ListServiceQuotas return applied quotas,
// GetAWSDefaultServiceQuota returns the AWS default, and
// RequestServiceQuotaIncrease / ListRequestedServiceQuotaChangeHistory model the
// increase-request lifecycle.
//
// Service Quotas uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on
// the X-Amz-Target header, prefix "ServiceQuotasV20190624."). This handler serves
// the quota values and increase-request history only; it does not enforce quotas
// against live resource usage.
package servicequotas

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/features/quota"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

const targetPrefix = "ServiceQuotasV20190624."

// Handler serves Service Quotas JSON-RPC requests backed by a quota registry.
type Handler struct {
	reg       *quota.Registry
	accountID string
	region    string
	routes    map[string]http.HandlerFunc
}

// New returns a Service Quotas handler backed by reg. accountID and region shape
// the QuotaArns returned to clients.
func New(reg *quota.Registry, accountID, region string) *Handler {
	h := &Handler{reg: reg, accountID: accountID, region: region}
	h.routes = map[string]http.HandlerFunc{
		"GetServiceQuota":                        h.getServiceQuota,
		"GetAWSDefaultServiceQuota":              h.getDefaultServiceQuota,
		"ListServiceQuotas":                      h.listServiceQuotas,
		"ListAWSDefaultServiceQuotas":            h.listServiceQuotas,
		"RequestServiceQuotaIncrease":            h.requestIncrease,
		"ListRequestedServiceQuotaChangeHistory": h.listHistory,
	}

	return h
}

// Matches returns true for Service Quotas-shaped requests (X-Amz-Target of
// "ServiceQuotasV20190624.<Operation>"). The prefix is disjoint from every other
// AWS JSON-RPC service, so registration order is unconstrained.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Service Quotas operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Service Quotas operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a registry/validation error to the closest Service Quotas JSON
// error type.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "NoSuchResourceException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "IllegalArgumentException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "ServiceException", msg)
	}
}
