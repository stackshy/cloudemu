// Package timestreamwrite implements the Amazon Timestream Write control-plane
// API (AWS JSON 1.0) as a server.Handler. Point the real
// aws-sdk-go-v2/service/timestreamwrite client (or the `aws timestream-write`
// CLI, or the aws_timestreamwrite_database / aws_timestreamwrite_table
// Terraform resources) at a Server registered with this handler and database
// and table operations run against an in-memory timestreamwrite driver.
//
// Timestream uses the AWS JSON 1.0 wire shape: POST with a JSON body dispatched
// on the X-Amz-Target header, prefix "Timestream_20181101.".
//
// Endpoint discovery: Timestream normally requires the SDK to first call
// DescribeEndpoints and route subsequent operations at the returned address.
// This handler serves DescribeEndpoints (returning the request's own host) so a
// discovering client can proceed. In practice the aws-sdk-go-v2 client and
// terraform-provider-aws skip discovery entirely when a custom endpoint is
// configured (the emulator's endpoint override), so operations reach this
// handler directly — DescribeEndpoints is served for completeness and for
// clients that do discover.
package timestreamwrite

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	tsdriver "github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

// TargetPrefix is the X-Amz-Target prefix every Timestream Write operation
// carries. It is exported so the wire-server registration and the authzgate
// JSON-RPC service map can reference the single source of truth.
const TargetPrefix = "Timestream_20181101."

// Handler serves Timestream Write JSON-RPC requests against a timestreamwrite
// driver.
type Handler struct {
	timestream tsdriver.Timestream
	routes     map[string]http.HandlerFunc
}

// New returns a Timestream Write handler backed by d.
func New(d tsdriver.Timestream) *Handler {
	h := &Handler{timestream: d}
	h.routes = map[string]http.HandlerFunc{}
	h.registerEndpointRoutes()
	h.registerDatabaseRoutes()
	h.registerTableRoutes()
	h.registerTagRoutes()

	return h
}

// Matches returns true for Timestream Write requests (X-Amz-Target of
// "Timestream_20181101.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)
}

// ServeHTTP dispatches a Timestream Write operation based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Timestream Write operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver error to the closest Timestream JSON error type.
// Errors tagged with a specific exception (via driver.APIError) take precedence
// so ValidationException / ResourceNotFoundException / ConflictException surface
// as themselves.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *tsdriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, statusFor(apiErr.Exception), apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, tsdriver.ExResourceNotFound, msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, tsdriver.ExConflict, msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, tsdriver.ExValidation, msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, tsdriver.ExInternalServer, msg)
	}
}

// statusFor returns the HTTP status the real API pairs with an exception name.
// Every documented Timestream client exception is a 400 except
// InternalServerException.
func statusFor(exception string) int {
	if exception == tsdriver.ExInternalServer {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}
