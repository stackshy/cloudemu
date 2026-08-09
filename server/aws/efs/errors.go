package efs

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// errorBody is the restJson1 error body. The SDK reads the X-Amzn-Errortype
// header to select a typed exception, falling back to the body's __type.
type errorBody struct {
	Type      string `json:"__type"`
	ErrorCode string `json:"ErrorCode"`
	Message   string `json:"Message"`
}

// writeError writes a restJson1 error response. EFS error bodies carry both
// ErrorCode and Message; the header selects the typed exception.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Type: errType, ErrorCode: errType, Message: msg})
}

// writeErr maps a canonical cloudemu error to the closest EFS exception.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "FileSystemNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "FileSystemAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "BadRequest", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusConflict, "FileSystemInUse", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}

func notFound(w http.ResponseWriter, path string) {
	writeError(w, http.StatusNotFound, "BadRequest", "unsupported path: "+path)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "BadRequest", "method not allowed")
}

// decodeJSON decodes the request body into v. An empty body is treated as an
// empty object (several EFS ops carry all input in the path).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if err.Error() == "EOF" {
			return true
		}

		writeError(w, http.StatusBadRequest, "BadRequest", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// writeJSON writes a 200 restJson1 success body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	encodeJSON(w, v)
}

// encodeJSON writes v as JSON without touching status/headers, for callers that
// have already written a non-200 success status (e.g. 201 Created).
func encodeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

// writeStatus writes an empty body with a specific status (e.g. 204 for
// CreateMountTarget-less deletes). Most EFS deletes return 204 No Content.
func writeStatus(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}
