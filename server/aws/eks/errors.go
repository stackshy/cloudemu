package eks

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/errors"
)

// errorBody is the JSON body shape EKS returns for failures. The SDK reads
// the X-Amzn-ErrorType header for routing and falls back to the body's
// type/code field if absent.
type errorBody struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// writeError writes a REST/JSON error response with the given HTTP status,
// EKS-shaped error type, and message. The X-Amzn-ErrorType header is the
// canonical signal the SDK reads to map to a typed exception.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-ErrorType", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Code: errType, Message: msg})
}

// writeErr maps cloudemu canonical errors to EKS-shaped error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ResourceInUseException", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "InvalidRequestException", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "ServerException", err.Error())
	}
}
