// Package athena implements the AWS Athena JSON 1.1 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/athena client (or the
// `aws athena` CLI, or the Terraform AWS provider) at a Server registered with
// this handler and workgroup, named-query, query-execution, and Data Catalog
// read operations run against an in-memory Athena driver.
//
// Athena uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "AmazonAthena.").
package athena

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	athenadriver "github.com/stackshy/cloudemu/v2/services/athena/driver"
)

const targetPrefix = "AmazonAthena."

// Handler serves Athena JSON-RPC requests against an Athena driver.
type Handler struct {
	athena athenadriver.Athena
	routes map[string]http.HandlerFunc
}

// New returns an Athena handler backed by d.
func New(d athenadriver.Athena) *Handler {
	h := &Handler{athena: d}
	h.routes = map[string]http.HandlerFunc{
		"CreateWorkGroup":     h.createWorkGroup,
		"GetWorkGroup":        h.getWorkGroup,
		"UpdateWorkGroup":     h.updateWorkGroup,
		"DeleteWorkGroup":     h.deleteWorkGroup,
		"ListWorkGroups":      h.listWorkGroups,
		"CreateNamedQuery":    h.createNamedQuery,
		"GetNamedQuery":       h.getNamedQuery,
		"DeleteNamedQuery":    h.deleteNamedQuery,
		"ListNamedQueries":    h.listNamedQueries,
		"StartQueryExecution": h.startQueryExecution,
		"GetQueryExecution":   h.getQueryExecution,
		"GetQueryResults":     h.getQueryResults,
		"StopQueryExecution":  h.stopQueryExecution,
		"ListQueryExecutions": h.listQueryExecutions,
		"GetDatabase":         h.getDatabase,
		"ListDatabases":       h.listDatabases,
		"GetDataCatalog":      h.getDataCatalog,
		"ListDataCatalogs":    h.listDataCatalogs,
		"TagResource":         h.tagResource,
		"UntagResource":       h.untagResource,
		"ListTagsForResource": h.listTagsForResource,
	}

	return h
}

// Matches returns true for Athena-shaped requests (X-Amz-Target of
// "AmazonAthena.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Athena operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Athena operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver error to the closest Athena JSON error type. Errors
// tagged with a specific Athena exception (via driver.APIError) take precedence
// so InvalidRequestException / ResourceNotFoundException surface as themselves.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *athenadriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, athenadriver.ExResourceNotFound, msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, athenadriver.ExInvalidRequest, msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, athenadriver.ExTooManyRequests, msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, athenadriver.ExInternalServer, msg)
	}
}
