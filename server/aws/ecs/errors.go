package ecs

import (
	stderrors "errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// ecsExceptioner is implemented by driver errors that carry a precise ECS
// exception name (e.g. ClusterNotFoundException vs ServiceNotFoundException).
// The server prefers that name over the generic code-based mapping so the SDK
// resolves the operation-appropriate typed exception.
type ecsExceptioner interface {
	ECSException() string
}

// writeErr maps a canonical cloudemu error to the closest ECS exception. ECS
// uses AWS JSON 1.1, so the SDK keys off the "__type" body written by
// wire.WriteJSONError to select the typed error.
func writeErr(w http.ResponseWriter, err error) {
	msg := wireMessage(err)

	var ex ecsExceptioner
	if stderrors.As(err, &ex) {
		status := http.StatusBadRequest
		if cerrors.GetCode(err) == cerrors.Internal {
			status = http.StatusInternalServerError
		}

		wire.WriteJSONError(w, status, ex.ECSException(), msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ClientException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "ServerException", msg)
	}
}

// wireMessage returns the bare error message for the wire, stripping the
// internal "<Code>: " prefix that cerrors.Error.Error() adds so the SDK-surfaced
// exception message reads like real AWS (e.g. "No Fargate configuration exists
// for given values." rather than "InvalidArgument: No Fargate...").
func wireMessage(err error) string {
	var ce *cerrors.Error
	if stderrors.As(err, &ce) {
		return ce.Message
	}

	return err.Error()
}
