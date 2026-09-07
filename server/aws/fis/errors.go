package fis

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// errorBody is the restJson1 error body. The SDK reads the X-Amzn-Errortype
// header to select a typed exception.
type errorBody struct {
	Message string `json:"message"`
}

// writeError writes a restJson1 error response with the given exception type.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Message: msg})
}

// statusForException returns the HTTP status for a FIS exception name.
func statusForException(exception string) int {
	switch exception {
	case driver.ExNotFound:
		return http.StatusNotFound
	case driver.ExConflict:
		return http.StatusConflict
	case driver.ExValidation:
		return http.StatusBadRequest
	case driver.ExQuota:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// exceptionForCode maps a canonical cloudemu code to a FIS exception name for
// errors that did not carry an explicit driver.APIError tag.
func exceptionForCode(err error) (status int, errType string) {
	switch {
	case cerrors.IsNotFound(err):
		return http.StatusNotFound, driver.ExNotFound
	case cerrors.IsAlreadyExists(err):
		return http.StatusConflict, driver.ExConflict
	case cerrors.IsFailedPrecondition(err):
		return http.StatusConflict, driver.ExConflict
	case cerrors.IsInvalidArgument(err):
		return http.StatusBadRequest, driver.ExValidation
	default:
		return http.StatusInternalServerError, "InternalServerException"
	}
}

// writeErr maps a driver error to the precise FIS exception. Tagged
// driver.APIError values are honored first so the exact exception name is
// preserved; untagged errors fall back to the canonical-code mapping.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *driver.APIError
	if errors.As(err, &apiErr) {
		writeError(w, statusForException(apiErr.Exception), apiErr.Exception, msg)

		return
	}

	status, errType := exceptionForCode(err)
	writeError(w, status, errType, msg)
}

func notFoundPath(w http.ResponseWriter, path string) {
	writeError(w, http.StatusNotFound, driver.ExNotFound, "unsupported path: "+path)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, driver.ExValidation, "method not allowed")
}

// decodeBody unmarshals the request body into v. An empty body is accepted and
// leaves v at its zero value.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}

		writeError(w, http.StatusBadRequest, driver.ExValidation, "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// writeJSON writes a 200 restJson1 success body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(v)
}
