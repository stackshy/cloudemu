// Package cognito implements the AWS Cognito user-pools (cognito-idp) JSON 1.1
// protocol as a server.Handler. Point the real
// aws-sdk-go-v2/service/cognitoidentityprovider client (or the `aws cognito-idp`
// CLI, or the Terraform AWS provider) at a Server registered with this handler
// and the user-pool, app-client, and hosted-UI-domain control-plane operations
// run against an in-memory Cognito driver.
//
// Cognito uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "AWSCognitoIdentityProviderService.").
package cognito

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	cognitodriver "github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

const targetPrefix = "AWSCognitoIdentityProviderService."

// Handler serves Cognito user-pools JSON-RPC requests against a Cognito driver.
type Handler struct {
	cognito cognitodriver.Cognito
	routes  map[string]http.HandlerFunc
}

// New returns a Cognito handler backed by d.
func New(d cognitodriver.Cognito) *Handler {
	h := &Handler{cognito: d}
	h.routes = map[string]http.HandlerFunc{
		"CreateUserPool":         h.createUserPool,
		"DescribeUserPool":       h.describeUserPool,
		"UpdateUserPool":         h.updateUserPool,
		"DeleteUserPool":         h.deleteUserPool,
		"ListUserPools":          h.listUserPools,
		"GetUserPoolMfaConfig":   h.getUserPoolMfaConfig,
		"SetUserPoolMfaConfig":   h.setUserPoolMfaConfig,
		"CreateUserPoolClient":   h.createUserPoolClient,
		"DescribeUserPoolClient": h.describeUserPoolClient,
		"UpdateUserPoolClient":   h.updateUserPoolClient,
		"DeleteUserPoolClient":   h.deleteUserPoolClient,
		"ListUserPoolClients":    h.listUserPoolClients,
		"CreateUserPoolDomain":   h.createUserPoolDomain,
		"DescribeUserPoolDomain": h.describeUserPoolDomain,
		"DeleteUserPoolDomain":   h.deleteUserPoolDomain,
		"TagResource":            h.tagResource,
		"UntagResource":          h.untagResource,
		"ListTagsForResource":    h.listTagsForResource,
	}

	return h
}

// Matches returns true for Cognito-shaped requests (X-Amz-Target of
// "AWSCognitoIdentityProviderService.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Cognito operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Cognito operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver error to the closest Cognito JSON error type. Errors
// tagged with a specific Cognito exception (via driver.APIError) take precedence
// so ResourceNotFoundException / InvalidParameterException surface as
// themselves.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *cognitodriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, cognitodriver.ExResourceNotFound, msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, cognitodriver.ExInvalidParameter, msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, cognitodriver.ExTooManyRequests, msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, cognitodriver.ExInternalError, msg)
	}
}
