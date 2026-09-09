// Package transfer implements the AWS Transfer Family JSON 1.1 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/transfer client (or the
// `aws transfer` CLI, or the Terraform AWS provider) at a Server registered
// with this handler and server, user, SSH-key, and tag control-plane operations
// run against an in-memory Transfer driver.
//
// Transfer uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "TransferService.", API version 2018-11-05).
// This is a control-plane emulator only: there is no real SFTP/FTPS data plane.
package transfer

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	transferdriver "github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

const targetPrefix = "TransferService."

// Handler serves Transfer Family JSON-RPC requests against a Transfer driver.
type Handler struct {
	transfer transferdriver.Transfer
	routes   map[string]http.HandlerFunc
}

// New returns a Transfer handler backed by d.
func New(d transferdriver.Transfer) *Handler {
	h := &Handler{transfer: d}
	h.routes = map[string]http.HandlerFunc{
		"CreateServer":        h.createServer,
		"DescribeServer":      h.describeServer,
		"UpdateServer":        h.updateServer,
		"DeleteServer":        h.deleteServer,
		"ListServers":         h.listServers,
		"StartServer":         h.startServer,
		"StopServer":          h.stopServer,
		"CreateUser":          h.createUser,
		"DescribeUser":        h.describeUser,
		"UpdateUser":          h.updateUser,
		"DeleteUser":          h.deleteUser,
		"ListUsers":           h.listUsers,
		"ImportSshPublicKey":  h.importSSHPublicKey,
		"DeleteSshPublicKey":  h.deleteSSHPublicKey,
		"TagResource":         h.tagResource,
		"UntagResource":       h.untagResource,
		"ListTagsForResource": h.listTagsForResource,
	}

	return h
}

// Matches returns true for Transfer-shaped requests (X-Amz-Target of
// "TransferService.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Transfer operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Transfer operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver error to the closest Transfer JSON error type. Errors
// tagged with a specific exception (via driver.APIError) take precedence so a
// NotFound surfaces as ResourceNotFoundException and never leaks a cerrors code
// prefix.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *transferdriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, transferdriver.ExResourceNotFound, msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, transferdriver.ExResourceExists, msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, transferdriver.ExInvalidRequest, msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, transferdriver.ExThrottling, msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, transferdriver.ExInternalService, msg)
	}
}
