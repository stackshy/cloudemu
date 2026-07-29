package ecs

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// writeErr maps a canonical cloudemu error to the closest ECS exception. ECS
// uses AWS JSON 1.1, so the SDK keys off the "__type" body written by
// wire.WriteJSONError to select the typed error.
func writeErr(w http.ResponseWriter, err error) {
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
