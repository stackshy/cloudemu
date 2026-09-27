package backup

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// errorBody is the restJson1 error body. The SDK reads the X-Amzn-Errortype
// header to select a typed exception.
type errorBody struct {
	Message string `json:"Message"`
}

// writeError writes a restJson1 error response with the given exception type.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Message: msg})
}

// statusForException returns the HTTP status for an AWS Backup exception name.
// Backup returns 400 for its typed client exceptions (including NotFound).
func statusForException(exception string) int {
	switch exception {
	case driver.ExServiceUnavail:
		return http.StatusInternalServerError
	case driver.ExResourceNotFound, driver.ExAlreadyExists, driver.ExInvalidParameter,
		driver.ExInvalidRequest, driver.ExMissingParameter, driver.ExLimitExceeded:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// exceptionForCode maps a canonical cloudemu code to a Backup exception name for
// errors that did not carry an explicit driver.APIError tag.
func exceptionForCode(err error) string {
	switch {
	case cerrors.IsNotFound(err):
		return driver.ExResourceNotFound
	case cerrors.IsAlreadyExists(err):
		return driver.ExAlreadyExists
	case cerrors.IsFailedPrecondition(err):
		return driver.ExInvalidRequest
	case cerrors.IsInvalidArgument(err):
		return driver.ExInvalidParameter
	default:
		return driver.ExServiceUnavail
	}
}

// writeErr maps a driver error to the precise AWS Backup exception. Tagged
// driver.APIError values are honored first so the exact exception name is
// preserved; untagged errors fall back to the canonical-code mapping.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *driver.APIError
	if errors.As(err, &apiErr) {
		writeError(w, statusForException(apiErr.Exception), apiErr.Exception, msg)

		return
	}

	errType := exceptionForCode(err)
	writeError(w, statusForException(errType), errType, msg)
}

func notFoundPath(w http.ResponseWriter, path string) {
	writeError(w, http.StatusNotFound, driver.ExResourceNotFound, "unsupported path: "+path)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, driver.ExInvalidParameter, "method not allowed")
}

// decodeBody unmarshals the request body into v. An empty body is accepted and
// leaves v at its zero value.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}

		writeError(w, http.StatusBadRequest, driver.ExInvalidParameter, "invalid JSON: "+err.Error())

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

// writeEmpty writes a 200 response with an empty JSON object, used by the
// operations that return no body.
func writeEmpty(w http.ResponseWriter) {
	writeJSON(w, struct{}{})
}
