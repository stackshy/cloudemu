package vpclattice

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const contentTypeJSON = "application/json"

const maxBodyBytes = 5 << 20

// errorBody is the restJson1 error body. The SDK reads the X-Amzn-Errortype
// header to select a typed exception, falling back to the body's __type.
type errorBody struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// writeError writes a restJson1 error response.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Type: errType, Message: msg})
}

// writeErr maps a canonical cloudemu error to the closest VPC Lattice exception.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ConflictException", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
	case cerrors.IsPermissionDenied(err):
		writeError(w, http.StatusForbidden, "AccessDeniedException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusConflict, "ConflictException", err.Error())
	case cerrors.IsThrottled(err):
		writeError(w, http.StatusTooManyRequests, "ThrottlingException", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerException", err.Error())
	}
}

func notFound(w http.ResponseWriter, path string) {
	writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+path)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "ValidationException", "method not allowed")
}

// decodeJSON decodes the request body into v, writing a ValidationException on
// failure. An empty body is treated as an empty object (some ops take no body).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)

	if err := dec.Decode(v); err != nil {
		if err.Error() == "EOF" {
			return true
		}

		writeError(w, http.StatusBadRequest, "ValidationException", "invalid JSON: "+err.Error())

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
