// Package costexplorer implements the AWS Cost Explorer JSON 1.1 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/costexplorer client (or
// the `aws ce` CLI) at a Server registered with this handler and cost queries
// run against the live in-memory inventory: it prices the resources the
// resource-discovery engine walks with the shared services/pricing model and
// shapes the result into Cost Explorer's ResultsByTime / forecast responses.
//
// Cost Explorer uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on
// the X-Amz-Target header, prefix "AWSInsightsIndexService."). It is a
// read-only, wire-only handler: there is no Cost Explorer provider mock, and no
// cost model of its own — the numbers come entirely from services/cost over the
// resource-discovery inventory.
package costexplorer

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

const targetPrefix = "AWSInsightsIndexService."

// Handler serves Cost Explorer JSON-RPC requests backed by a cost inventory.
type Handler struct {
	inv    cost.Inventory
	routes map[string]http.HandlerFunc
}

// New returns a Cost Explorer handler backed by inv, the resource inventory the
// cost estimate is derived from (a *resourcediscovery.Engine satisfies it).
func New(inv cost.Inventory) *Handler {
	h := &Handler{inv: inv}
	h.routes = map[string]http.HandlerFunc{
		"GetCostAndUsage":    h.getCostAndUsage,
		"GetCostForecast":    h.getCostForecast,
		"GetDimensionValues": h.getDimensionValues,
		"GetTags":            h.getTags,
	}

	return h
}

// Matches returns true for Cost Explorer-shaped requests (X-Amz-Target of
// "AWSInsightsIndexService.<Operation>"). The prefix is disjoint from every
// other AWS JSON-RPC service, so registration order is unconstrained.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Cost Explorer operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Cost Explorer operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver/validation error to the closest Cost Explorer JSON
// error type.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalFailure", msg)
	}
}
