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
	var ex ecsExceptioner
	if stderrors.As(err, &ex) {
		status := http.StatusBadRequest
		if cerrors.GetCode(err) == cerrors.Internal {
			status = http.StatusInternalServerError
		}

		wire.WriteJSONError(w, status, ex.ECSException(), err.Error())

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ClientException", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "ServerException", err.Error())
	}
}
