package guardduty

import (
	"encoding/json"
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// errorBody is the restJson1 error body. The SDK reads the X-Amzn-Errortype
// header to select a typed exception, falling back to the body's __type.
type errorBody struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// writeError writes a restJson1 error response with the given exception type.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Type: errType, Message: msg})
}

// statusForException returns the HTTP status for a GuardDuty exception name.
func statusForException(exception string) int {
	switch exception {
	case driver.ExResourceNotFound:
		return http.StatusNotFound
	case driver.ExConflict:
		return http.StatusConflict
	case driver.ExBadRequest:
		return http.StatusBadRequest
	case driver.ExAccessDenied:
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

// exceptionForCode maps a canonical cloudemu code to a GuardDuty exception name
// for errors that did not carry an explicit driver.APIError tag.
func exceptionForCode(err error) (status int, errType string) {
	switch {
	case cerrors.IsNotFound(err):
		return http.StatusNotFound, driver.ExResourceNotFound
	case cerrors.IsAlreadyExists(err):
		return http.StatusConflict, driver.ExConflict
	case cerrors.IsInvalidArgument(err):
		return http.StatusBadRequest, driver.ExBadRequest
	default:
		return http.StatusInternalServerError, driver.ExInternal
	}
}

// writeErr maps a driver error to the precise GuardDuty exception. Tagged
// driver.APIError values are honored first so the exact exception name is
// preserved; untagged errors fall back to the canonical-code mapping.
func writeErr(w http.ResponseWriter, err error) {
	var apiErr *driver.APIError
	if errors.As(err, &apiErr) {
		writeError(w, statusForException(apiErr.Exception), apiErr.Exception, err.Error())

		return
	}

	status, errType := exceptionForCode(err)
	writeError(w, status, errType, err.Error())
}

func notFoundPath(w http.ResponseWriter, path string) {
	writeError(w, http.StatusNotFound, driver.ExBadRequest, "unsupported path: "+path)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, driver.ExBadRequest, "method not allowed")
}

// writeJSON writes a 200 restJson1 success body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(v)
}

// readBody reads and size-limits the request body, writing a BadRequestException
// on read failure. An empty body is returned as nil with ok=true.
func readBody(w http.ResponseWriter, r *http.Request) (body []byte, ok bool) {
	b, err := readAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, driver.ExBadRequest, "read body: "+err.Error())

		return nil, false
	}

	return b, true
}
