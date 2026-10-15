package bedrockagentruntime

import (
	"encoding/json"
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// errorBody is the JSON body shape the runtime returns for failures. The SDK
// reads the X-Amzn-ErrorType header to map to a typed exception and falls back
// to the body's type field if absent.
type errorBody struct {
	Type         string `json:"__type"`
	Message      string `json:"message"`
	ResourceName string `json:"resourceName,omitempty"`
}

// writeError writes a restJson1 error response with the given HTTP status,
// error type, and message.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	writeErrorFor(w, status, errType, msg, "")
}

// writeErrorFor writes an error response that names the resource involved.
func writeErrorFor(w http.ResponseWriter, status int, errType, msg, resource string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Type: errType, Message: msg, ResourceName: resource})
}

// writeErr maps cloudemu canonical errors to runtime-shaped error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", cerrors.Message(err))
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ConflictException", cerrors.Message(err))
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "ValidationException", cerrors.Message(err))
	case cerrors.IsFailedPrecondition(err):
		// A state conflict: an ended session, an active session deleted, a
		// finished flow execution stopped.
		writeError(w, http.StatusConflict, "ConflictException", cerrors.Message(err))
	case cerrors.GetCode(err) == cerrors.Unavailable:
		// A resource the agent depends on (an action-group Lambda) failed;
		// resourceName names it when the backend knows it.
		var re *driver.ResourceError

		resource := ""
		if errors.As(err, &re) {
			resource = re.ResourceName
		}

		writeErrorFor(w, http.StatusFailedDependency, "DependencyFailedException", cerrors.Message(err), resource)
	case cerrors.IsThrottled(err):
		writeError(w, http.StatusTooManyRequests, "ThrottlingException", cerrors.Message(err))
	case cerrors.IsPermissionDenied(err):
		writeError(w, http.StatusForbidden, "AccessDeniedException", cerrors.Message(err))
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		writeError(w, http.StatusBadRequest, "ServiceQuotaExceededException", cerrors.Message(err))
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerException", cerrors.Message(err))
	}
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "ValidationException", "method not allowed")
}
