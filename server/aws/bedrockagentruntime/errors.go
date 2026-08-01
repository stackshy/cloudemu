package bedrockagentruntime

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// errorBody is the JSON body shape the runtime returns for failures. The SDK
// reads the X-Amzn-ErrorType header to map to a typed exception and falls back
// to the body's type field if absent.
type errorBody struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// writeError writes a restJson1 error response with the given HTTP status,
// error type, and message.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Type: errType, Message: msg})
}

// writeErr maps cloudemu canonical errors to runtime-shaped error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ConflictException", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
	case cerrors.IsThrottled(err):
		writeError(w, http.StatusTooManyRequests, "ThrottlingException", err.Error())
	case cerrors.IsPermissionDenied(err):
		writeError(w, http.StatusForbidden, "AccessDeniedException", err.Error())
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		writeError(w, http.StatusBadRequest, "ServiceQuotaExceededException", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerException", err.Error())
	}
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "ValidationException", "method not allowed")
}
