package sagemaker

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// writeDriverError maps a driver error to the closest SageMaker exception and
// HTTP status. SageMaker uses awsJson1_1, so the SDK keys off X-Amzn-ErrorType
// (written by wire.WriteJSONError) to pick the typed error.
//
// An error tagged with driver.APIError keeps its exact exception name. Creating
// a resource that already exists is a ValidationException in real SageMaker
// ("Cannot create already existing ..."); ResourceInUse is reserved for
// deleting or changing a resource that something still depends on.
func writeDriverError(w http.ResponseWriter, err error) {
	if ex, ok := driver.ExceptionOf(err); ok {
		wire.WriteJSONError(w, http.StatusBadRequest, ex, cerrors.Message(err))

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		if _, ok := w.(notFoundAsValidation); ok {
			writeNotFoundAsValidation(w, err)

			return
		}

		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFound", cerrors.Message(err))
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, driver.ExValidation, cerrors.Message(err))
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, driver.ExResourceLimitExceeded, cerrors.Message(err))
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", cerrors.Message(err))
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", cerrors.Message(err))
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalFailure", cerrors.Message(err))
	}
}
