// Package ocirest provides shared HTTP wire helpers for OCI service handlers:
// JSON encoding, OCI's error shape, opc-* headers, and pagination.
package ocirest

import (
	"encoding/json"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// OCI request/response headers.
const (
	HeaderRequestID     = "opc-request-id"
	HeaderNextPage      = "opc-next-page"
	HeaderWorkRequestID = "opc-work-request-id"
	HeaderRetryToken    = "opc-retry-token" //nolint:gosec // header name, not a credential
)

// codeInternalServerError is OCI's error code for an unmapped failure.
const codeInternalServerError = "InternalServerError"

// DefaultLimit is the page size used when a list request omits limit.
const DefaultLimit = 100

// ErrorBody is OCI's error response shape.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON writes a JSON response with the given status code, stamping an
// opc-request-id.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	setRequestID(w)
	w.WriteHeader(status)

	if v == nil {
		return
	}

	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}

// WriteError writes an OCI error response.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	setRequestID(w)
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(ErrorBody{ //nolint:errcheck // best-effort response
		Code:    code,
		Message: message,
	})
}

// WriteDriverError maps a driver error onto OCI's status and error code.
func WriteDriverError(w http.ResponseWriter, err error) {
	status, code := statusFor(cerrors.GetCode(err))
	WriteError(w, status, code, err.Error())
}

// statusFor maps a canonical error code onto OCI's HTTP status and error code.
//
// NotFound and PermissionDenied both yield 404 NotAuthorizedOrNotFound: OCI
// does not distinguish them, so a caller cannot probe for a resource's
// existence across a compartment boundary.
func statusFor(code cerrors.Code) (status int, ociCode string) {
	switch code {
	case cerrors.OK:
		return http.StatusOK, ""
	case cerrors.NotFound, cerrors.PermissionDenied:
		return http.StatusNotFound, "NotAuthorizedOrNotFound"
	case cerrors.AlreadyExists:
		return http.StatusConflict, "Conflict"
	case cerrors.InvalidArgument:
		return http.StatusBadRequest, "InvalidParameter"
	case cerrors.FailedPrecondition:
		return http.StatusConflict, "IncorrectState"
	case cerrors.Throttled, cerrors.ResourceExhausted:
		return http.StatusTooManyRequests, "TooManyRequests"
	case cerrors.Unimplemented:
		return http.StatusNotImplemented, "NotImplemented"
	case cerrors.Unavailable:
		return http.StatusServiceUnavailable, "ServiceUnavailable"
	case cerrors.Internal:
		return http.StatusInternalServerError, codeInternalServerError
	default:
		return http.StatusInternalServerError, codeInternalServerError
	}
}

// DecodeJSON reads a JSON request body into v, writing an error response and
// returning false if it cannot be decoded.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "InvalidParameter", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

// CompartmentID returns the compartmentId query parameter, which OCI requires
// on nearly every list call.
func CompartmentID(r *http.Request) string {
	return r.URL.Query().Get("compartmentId")
}

// RequireCompartmentID returns the compartmentId query parameter, writing
// OCI's error response and returning false when it is absent.
func RequireCompartmentID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := CompartmentID(r)
	if id == "" {
		WriteError(w, http.StatusBadRequest, "InvalidParameter", "compartmentId is required")
		return "", false
	}

	return id, true
}

// Limit returns the limit query parameter, falling back to DefaultLimit when
// it is absent or unparseable.
func Limit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return DefaultLimit
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultLimit
	}

	return n
}

// Page returns the page query parameter, OCI's opaque pagination cursor.
func Page(r *http.Request) string {
	return r.URL.Query().Get("page")
}

// SetNextPage stamps the cursor a client passes back as page. An empty token
// sets no header, which is how OCI signals the last page.
func SetNextPage(w http.ResponseWriter, token string) {
	if token != "" {
		w.Header().Set(HeaderNextPage, token)
	}
}

// SetWorkRequestID stamps the work request a client polls for completion.
func SetWorkRequestID(w http.ResponseWriter, id string) {
	w.Header().Set(HeaderWorkRequestID, id)
}

// setRequestID echoes the caller's opc-request-id, or mints one.
func setRequestID(w http.ResponseWriter) {
	if w.Header().Get(HeaderRequestID) == "" {
		w.Header().Set(HeaderRequestID, idgen.GenerateID("cloudemu"))
	}
}
