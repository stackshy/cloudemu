package apprunner

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// invalidRequest builds an InvalidRequestException-tagged error for invalid
// input.
func invalidRequest(msg string) error {
	return &driver.APIError{
		Exception: driver.ExInvalidRequest,
		Err:       errors.New(errors.InvalidArgument, msg),
	}
}

// invalidState builds an InvalidStateException-tagged error for an illegal state
// transition (for example pausing a service that is not running).
func invalidState(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExInvalidState,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}
