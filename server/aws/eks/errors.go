package eks

import (
	"encoding/json"
	stderrors "errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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
	// A provider error may carry a precise EKS exception name and HTTP status
	// (e.g. ResourceInUseException/409 for deleting a cluster that still has
	// node groups, Fargate profiles, or add-ons attached), which the generic
	// code-based mapping below would otherwise surface as InvalidRequestException.
	var ex interface {
		EKSException() (string, int)
	}

	if stderrors.As(err, &ex) {
		name, status := ex.EKSException()
		writeError(w, status, name, wireMessage(err))

		return
	}

	// wireMessage strips the internal "<Code>: " prefix so the surfaced message
	// reads like real AWS (the X-Amzn-ErrorType header already carries the code).
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", wireMessage(err))
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ResourceInUseException", wireMessage(err))
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidParameterException", wireMessage(err))
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "InvalidRequestException", wireMessage(err))
	default:
		writeError(w, http.StatusInternalServerError, "ServerException", wireMessage(err))
	}
}

// wireMessage returns the bare error message for the wire, stripping the
// internal "<Code>: " prefix that cerrors.Error.Error() adds so the surfaced
// exception message reads like real AWS rather than leaking the canonical code.
func wireMessage(err error) string {
	var ce *cerrors.Error
	if stderrors.As(err, &ce) {
		return ce.Message
	}

	return err.Error()
}
