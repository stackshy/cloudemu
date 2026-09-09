package appsync

import (
	"encoding/json"
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
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

// statusForException returns the HTTP status for an AppSync exception name.
func statusForException(exception string) int {
	switch exception {
	case driver.ExNotFound:
		return http.StatusNotFound
	case driver.ExBadRequest, driver.ExAPIKeyValidity:
		return http.StatusBadRequest
	case driver.ExConcurrentMod:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// exceptionForCode maps a canonical cloudemu code to an AppSync exception name
// for errors that did not carry an explicit driver.APIError tag.
func exceptionForCode(err error) (status int, errType string) {
	switch {
	case cerrors.IsNotFound(err):
		return http.StatusNotFound, driver.ExNotFound
	case cerrors.IsInvalidArgument(err), cerrors.IsAlreadyExists(err):
		return http.StatusBadRequest, driver.ExBadRequest
	default:
		return http.StatusInternalServerError, driver.ExInternalFailure
	}
}

// writeErr maps a driver error to the precise AppSync exception. Tagged
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
	writeError(w, http.StatusMethodNotAllowed, driver.ExBadRequest, "method not allowed")
}

// decodeBodyMap reads the request body into a map of raw fields. An empty body
// yields an empty map (many AppSync ops carry input in the path).
func decodeBodyMap(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	out := map[string]json.RawMessage{}

	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		if err.Error() == "EOF" {
			return out, true
		}

		writeError(w, http.StatusBadRequest, driver.ExBadRequest, "invalid JSON: "+err.Error())

		return nil, false
	}

	return out, true
}

// writeJSON writes a 200 restJson1 success body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(v)
}

// withNext adds a nextToken field to a response map when non-empty.
func withNext(body map[string]any, next string) map[string]any {
	if next != "" {
		body["nextToken"] = next
	}

	return body
}
