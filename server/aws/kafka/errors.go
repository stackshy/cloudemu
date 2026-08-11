package kafka

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
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

// statusForException returns the HTTP status for an MSK exception name.
func statusForException(exception string) int {
	switch exception {
	case driver.ExNotFound:
		return http.StatusNotFound
	case driver.ExConflict:
		return http.StatusConflict
	case driver.ExBadRequest:
		return http.StatusBadRequest
	case driver.ExUnauthorized, driver.ExForbidden:
		return http.StatusForbidden
	case driver.ExTooManyRequests:
		return http.StatusTooManyRequests
	case driver.ExServiceUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// exceptionForCode maps a canonical cloudemu code to an MSK exception name for
// errors that did not carry an explicit driver.APIError tag.
func exceptionForCode(err error) (status int, errType string) {
	switch {
	case cerrors.IsNotFound(err):
		return http.StatusNotFound, driver.ExNotFound
	case cerrors.IsAlreadyExists(err):
		return http.StatusConflict, driver.ExConflict
	case cerrors.IsInvalidArgument(err):
		return http.StatusBadRequest, driver.ExBadRequest
	default:
		return http.StatusInternalServerError, driver.ExInternalServer
	}
}

// writeErr maps a driver error to the precise MSK exception. Tagged
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
	writeError(w, http.StatusNotFound, driver.ExNotFound, "unsupported path: "+path)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, driver.ExBadRequest, "method not allowed")
}

// readBody reads and returns the request body (capped), or writes an error and
// returns ok=false. An empty body yields an empty slice.
func readBody(w http.ResponseWriter, r *http.Request) (body []byte, ok bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, driver.ExBadRequest, "read body: "+err.Error())

		return nil, false
	}

	return b, true
}

// decodeBody reads the body and unmarshals it into v. An empty body is treated
// as an empty object (many MSK ops carry input in the path/query).
func decodeBody(w http.ResponseWriter, r *http.Request, v any) (body []byte, ok bool) {
	b, ok := readBody(w, r)
	if !ok {
		return nil, false
	}

	if len(b) > 0 {
		if err := json.Unmarshal(b, v); err != nil {
			writeError(w, http.StatusBadRequest, driver.ExBadRequest, "invalid JSON: "+err.Error())

			return nil, false
		}
	}

	return b, true
}

// writeJSON writes a 200 restJson1 success body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(v)
}

// withNext adds a nextToken field to a response map when next is non-empty.
func withNext(m map[string]any, next string) map[string]any {
	if next != "" {
		m["nextToken"] = next
	}

	return m
}
