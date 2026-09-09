// Package aoss implements the Amazon OpenSearch Serverless control-plane API
// (AWS JSON 1.0) as a server.Handler. Point the real
// aws-sdk-go-v2/service/opensearchserverless client (or the `aws opensearchserverless`
// CLI, or the aws_opensearchserverless_* Terraform resources) at a Server
// registered with this handler and collection, security-policy, access-policy
// and tagging operations run against an in-memory aoss driver.
//
// OpenSearch Serverless uses the AWS JSON 1.0 wire shape: POST with a JSON body
// dispatched on the X-Amz-Target header, prefix "OpenSearchServerless.". This is
// distinct from the provisioned-domain `opensearch` service, which uses restJson1.
package aoss

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	aossdriver "github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

// TargetPrefix is the X-Amz-Target prefix every OpenSearch Serverless operation
// carries. It is exported so the wire-server registration and the authzgate
// JSON-RPC service map can reference the single source of truth.
const TargetPrefix = "OpenSearchServerless."

// Handler serves OpenSearch Serverless JSON-RPC requests against an aoss driver.
type Handler struct {
	aoss   aossdriver.AOSS
	routes map[string]http.HandlerFunc
}

// New returns an OpenSearch Serverless handler backed by d.
func New(d aossdriver.AOSS) *Handler {
	h := &Handler{aoss: d}
	h.routes = map[string]http.HandlerFunc{}
	h.registerCollectionRoutes()
	h.registerPolicyRoutes()
	h.registerTagRoutes()

	return h
}

// Matches returns true for OpenSearch Serverless requests (X-Amz-Target of
// "OpenSearchServerless.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)
}

// ServeHTTP dispatches an OpenSearch Serverless operation based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported OpenSearch Serverless operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver error to the closest OpenSearch Serverless JSON error
// type. Errors tagged with a specific exception (via driver.APIError) take
// precedence so ValidationException / ResourceNotFoundException /
// ConflictException surface as themselves.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *aossdriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, statusFor(apiErr.Exception), apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, aossdriver.ExResourceNotFound, msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, aossdriver.ExConflict, msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, aossdriver.ExValidation, msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, aossdriver.ExInternalServer, msg)
	}
}

// statusFor returns the HTTP status the real API pairs with an exception name.
// Every documented aoss client exception is a 400 except InternalServerException.
func statusFor(exception string) int {
	if exception == aossdriver.ExInternalServer {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}
