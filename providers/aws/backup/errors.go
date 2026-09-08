package backup

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// alreadyExists builds an AlreadyExistsException-tagged error.
func alreadyExists(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExAlreadyExists,
		Err:       errors.Newf(errors.AlreadyExists, format, args...),
	}
}

// invalidParam builds an InvalidParameterValueException-tagged error.
func invalidParam(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExInvalidParameter,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}

// missingParam builds a MissingParameterValueException-tagged error.
func missingParam(msg string) error {
	return &driver.APIError{
		Exception: driver.ExMissingParameter,
		Err:       errors.New(errors.InvalidArgument, msg),
	}
}

// invalidRequest builds an InvalidRequestException-tagged error, used for the
// state-machine guards (deleting a plan that still has selections, or a vault
// whose Vault Lock has become immutable).
func invalidRequest(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExInvalidRequest,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}
