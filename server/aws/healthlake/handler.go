// Package healthlake implements the AWS HealthLake control-plane API (AWS JSON
// 1.0) as a server.Handler. Point the real aws-sdk-go-v2/service/healthlake
// client (or the `aws healthlake` CLI) at a Server registered with this handler
// and FHIR data-store operations run against an in-memory healthlake driver.
//
// HealthLake uses the AWS JSON 1.0 wire shape: POST with a JSON body dispatched
// on the X-Amz-Target header, prefix "HealthLake.". Import/export jobs are the
// data plane and are out of scope; this handler serves the data-store
// control plane (create/describe/delete/list) and resource tagging.
package healthlake

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	hldriver "github.com/stackshy/cloudemu/v2/services/healthlake/driver"
)

// TargetPrefix is the X-Amz-Target prefix every HealthLake operation carries. It
// is exported so the wire-server registration and the authzgate JSON-RPC service
// map can reference the single source of truth.
const TargetPrefix = "HealthLake."

// Handler serves HealthLake JSON-RPC requests against a healthlake driver.
type Handler struct {
	healthlake hldriver.HealthLake
	routes     map[string]http.HandlerFunc
}

// New returns a HealthLake handler backed by d.
func New(d hldriver.HealthLake) *Handler {
	h := &Handler{healthlake: d}
	h.routes = map[string]http.HandlerFunc{}
	h.registerDatastoreRoutes()
	h.registerTagRoutes()

	return h
}

// Matches returns true for HealthLake requests (X-Amz-Target of
// "HealthLake.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)
}

// ServeHTTP dispatches a HealthLake operation based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported HealthLake operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver error to the closest HealthLake JSON error type. Errors
// tagged with a specific exception (via driver.APIError) take precedence so
// ValidationException / ResourceNotFoundException surface as themselves.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *hldriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, statusFor(apiErr.Exception), apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, hldriver.ExResourceNotFound, msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, hldriver.ExConflict, msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, hldriver.ExValidation, msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, hldriver.ExInternalServer, msg)
	}
}

// statusFor returns the HTTP status the real API pairs with an exception name.
// Every documented HealthLake client exception is a 400 except
// InternalServerException.
func statusFor(exception string) int {
	if exception == hldriver.ExInternalServer {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}
