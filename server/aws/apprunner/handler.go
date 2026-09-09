// Package apprunner implements the AWS App Runner control-plane API (AWS JSON
// 1.0) as a server.Handler. Point the real aws-sdk-go-v2/service/apprunner
// client (or the `aws apprunner` CLI, or the aws_apprunner_service Terraform
// resource) at a Server registered with this handler and the service, auto
// scaling configuration, connection, VPC connector, observability configuration
// and tagging operations run against an in-memory apprunner driver.
//
// App Runner uses the AWS JSON 1.0 wire shape: POST with a JSON body dispatched
// on the X-Amz-Target header, prefix "AppRunner.". This is a control-plane-only
// surface: it runs no container runtime. A service is created directly into the
// terminal RUNNING state and its pause/resume/delete transitions are applied
// synchronously.
package apprunner

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ardriver "github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// TargetPrefix is the X-Amz-Target prefix every App Runner operation carries. It
// is exported so the wire-server registration and the authzgate JSON-RPC service
// map can reference the single source of truth.
const TargetPrefix = "AppRunner."

// Handler serves App Runner JSON-RPC requests against an apprunner driver.
type Handler struct {
	apprunner ardriver.AppRunner
	routes    map[string]http.HandlerFunc
}

// New returns an App Runner handler backed by d.
func New(d ardriver.AppRunner) *Handler {
	h := &Handler{apprunner: d, routes: map[string]http.HandlerFunc{}}
	h.registerServiceRoutes()
	h.registerAutoScalingRoutes()
	h.registerConnectionRoutes()
	h.registerVpcConnectorRoutes()
	h.registerObservabilityRoutes()
	h.registerTagRoutes()

	return h
}

// Matches returns true for App Runner requests (X-Amz-Target of
// "AppRunner.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)
}

// ServeHTTP dispatches an App Runner operation based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported App Runner operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error), collapsing the decode/call/respond
// boilerplate every operation would otherwise repeat.
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

// writeErr maps a driver error to the closest App Runner JSON error type. Errors
// tagged with a specific exception (via driver.APIError) take precedence so
// InvalidStateException / ResourceNotFoundException surface as themselves.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *ardriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, statusFor(apiErr.Exception), apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, ardriver.ExResourceNotFound, msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, ardriver.ExInvalidState, msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, ardriver.ExInvalidRequest, msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, ardriver.ExInternalServerError, msg)
	}
}

// statusFor returns the HTTP status the real API pairs with an exception name.
// Every documented App Runner client exception is a 400 except the internal
// service error.
func statusFor(exception string) int {
	if exception == ardriver.ExInternalServerError {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}

// pageFromWire builds a driver pagination cursor from the common wire members.
func pageFromWire(maxResults int32, nextToken string) ardriver.Page {
	return ardriver.Page{MaxResults: maxResults, NextToken: nextToken}
}

// mapWire renders a slice of driver values as their wire shapes via conv.
func mapWire[T any, W any](items []T, conv func(T) W) []W {
	out := make([]W, 0, len(items))
	for _, it := range items {
		out = append(out, conv(it))
	}

	return out
}
