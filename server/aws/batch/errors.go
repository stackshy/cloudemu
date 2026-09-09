package batch

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20

	// The only two error shapes AWS Batch surfaces (awsRestjson1). The SDK reads
	// the X-Amzn-Errortype header to select the typed exception.
	exceptionClient = "ClientException"
	exceptionServer = "ServerException"
)

// errorBody is the restJson1 error body Batch returns: just a message. The
// header carries the exception type.
type errorBody struct {
	Message string `json:"message"`
}

// writeError writes a restJson1 error with an explicit type and status, used for
// protocol-level faults (bad path/method/JSON).
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Message: msg})
}

// writeErr maps a canonical cloudemu error to a Batch exception. Every
// client-side code becomes a ClientException (400); anything else is a
// ServerException (500). Only the clean message is surfaced — no cerrors code
// prefix leaks into the body.
func writeErr(w http.ResponseWriter, err error) {
	status, errType := http.StatusInternalServerError, exceptionServer

	switch {
	case cerrors.IsNotFound(err),
		cerrors.IsAlreadyExists(err),
		cerrors.IsInvalidArgument(err),
		cerrors.IsFailedPrecondition(err),
		cerrors.IsPermissionDenied(err):
		status, errType = http.StatusBadRequest, exceptionClient
	}

	writeError(w, status, errType, cerrors.Message(err))
}

// decodeJSON decodes the request body into v. An empty body is treated as an
// empty object (some operations carry all input in the path).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if err.Error() == "EOF" {
			return true
		}

		writeError(w, http.StatusBadRequest, exceptionClient, "invalid JSON: "+err.Error())

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
