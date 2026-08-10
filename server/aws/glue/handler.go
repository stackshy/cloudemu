// Package glue implements the AWS Glue JSON 1.1 protocol as a server.Handler.
// Point the real aws-sdk-go-v2/service/glue client (or the `aws glue` CLI) at a
// Server registered with this handler and Data Catalog, crawler, job, trigger,
// workflow, blueprint, schema-registry, dev-endpoint, and tag operations run
// against an in-memory Glue driver.
//
// Glue uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "AWSGlue.").
package glue

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
)

const targetPrefix = "AWSGlue."

// Handler serves Glue JSON-RPC requests against a Glue driver.
type Handler struct {
	glue   gluedriver.Glue
	routes map[string]http.HandlerFunc
}

// New returns a Glue handler backed by d.
func New(d gluedriver.Glue) *Handler {
	h := &Handler{glue: d}
	h.routes = map[string]http.HandlerFunc{}
	h.registerCatalogRoutes()
	h.registerComputeRoutes()
	h.registerRegistryRoutes()
	h.registerSynthesizedRoutes()

	return h
}

// Matches returns true for Glue-shaped requests (X-Amz-Target of
// "AWSGlue.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Glue operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Glue operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error). It collapses the identical
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

// writeErr maps a driver error to the closest Glue JSON error type. Errors
// tagged with a specific Glue exception (via driver.APIError) take precedence so
// distinct exceptions like EntityNotFoundException / AlreadyExistsException /
// InvalidInputException surface as themselves rather than a generic code map.
func writeErr(w http.ResponseWriter, err error) {
	var apiErr *gluedriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, err.Error())

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, gluedriver.ExEntityNotFound, err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, gluedriver.ExAlreadyExists, err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, gluedriver.ExInvalidInput, err.Error())
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, gluedriver.ExResourceNumberLimit, err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, gluedriver.ExConcurrentModification, err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, gluedriver.ExInternalService, err.Error())
	}
}
