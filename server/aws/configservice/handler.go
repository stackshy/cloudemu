// Package configservice implements the AWS Config (configservice) JSON 1.1
// protocol as a server.Handler. Point the real
// aws-sdk-go-v2/service/configservice client (or the `aws configservice` CLI) at
// a Server registered with this handler and Config operations run against an
// in-memory Config driver.
//
// Config uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "StarlingDoveService.").
package configservice

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	cfgdriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

const targetPrefix = "StarlingDoveService."

// invalidRequest builds a ValidationException-tagged error for malformed
// operation inputs the wire layer rejects before reaching the driver.
func invalidRequest(msg string) error {
	return &cfgdriver.APIError{
		Exception: cfgdriver.ExValidation,
		Err:       cerrors.Newf(cerrors.InvalidArgument, "%s", msg),
	}
}

// Driver type aliases for terse use in op handlers.
type (
	cfgResourceKey          = cfgdriver.ResourceKey
	cfgRemediationConfig    = cfgdriver.RemediationConfiguration
	cfgRemediationException = cfgdriver.RemediationException
)

// Handler serves AWS Config JSON-RPC requests against a Config driver. AccountID
// and Region are used only to stamp synthesized aggregate-query results with a
// plausible source account/region.
type Handler struct {
	cfg       cfgdriver.Config
	accountID string
	region    string
	routes    map[string]http.HandlerFunc
}

// New returns a Config handler backed by d, stamping synthesized aggregate
// results with accountID/region.
func New(d cfgdriver.Config, accountID, region string) *Handler {
	h := &Handler{cfg: d, accountID: accountID, region: region}
	h.routes = h.buildRoutes()

	return h
}

// Matches returns true for Config-shaped requests (X-Amz-Target of
// "StarlingDoveService.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Config operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Config operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver error to the closest Config JSON error type. Errors
// tagged with a specific Config exception (via driver.APIError) take precedence
// via errors.As so distinct exceptions surface as themselves rather than a
// generic code map.
func writeErr(w http.ResponseWriter, err error) {
	var apiErr *cfgdriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, err.Error())

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceInUseException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
	}
}
