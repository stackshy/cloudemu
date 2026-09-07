// Package kendra implements the Amazon Kendra control-plane API (AWS JSON 1.1)
// as a server.Handler. Point the real aws-sdk-go-v2/service/kendra client (or
// the `aws kendra` CLI, or the aws_kendra_index / aws_kendra_data_source
// Terraform resources) at a Server registered with this handler and index and
// data source operations run against an in-memory kendra driver.
//
// Kendra uses the AWS JSON 1.1 wire shape: POST with a JSON body dispatched on
// the X-Amz-Target header, prefix "AWSKendraFrontendService.".
package kendra

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	kendradriver "github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// TargetPrefix is the X-Amz-Target prefix every Kendra operation carries. It is
// exported so the wire-server registration and the authzgate JSON-RPC service
// map can reference the single source of truth.
const TargetPrefix = "AWSKendraFrontendService."

// Handler serves Kendra JSON-RPC requests against a kendra driver.
type Handler struct {
	kendra kendradriver.Kendra
	routes map[string]http.HandlerFunc
}

// New returns a Kendra handler backed by d.
func New(d kendradriver.Kendra) *Handler {
	h := &Handler{kendra: d}
	h.routes = map[string]http.HandlerFunc{}
	h.registerIndexRoutes()
	h.registerDataSourceRoutes()
	h.registerTagRoutes()

	return h
}

// Matches returns true for Kendra requests (X-Amz-Target of
// "AWSKendraFrontendService.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)
}

// ServeHTTP dispatches a Kendra operation based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Kendra operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error), collapsing the identical
// decode/call/respond boilerplate every operation would otherwise repeat.
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

// writeErr maps a driver error to the closest Kendra JSON error type. Errors
// tagged with a specific exception (via driver.APIError) take precedence so
// ValidationException / ResourceNotFoundException / ConflictException surface as
// themselves.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *kendradriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, statusFor(apiErr.Exception), apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, kendradriver.ExResourceNotFound, msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, kendradriver.ExResourceAlreadyExist, msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, kendradriver.ExValidation, msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, kendradriver.ExInternalServer, msg)
	}
}

// statusFor returns the HTTP status the real API pairs with an exception name.
// Every documented Kendra client exception is a 400 except InternalServerException.
func statusFor(exception string) int {
	if exception == kendradriver.ExInternalServer {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}
