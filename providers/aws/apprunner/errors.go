package apprunner

import (
	"strconv"

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

// invalidRequest builds an InvalidRequestException-tagged error.
func invalidRequest(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExInvalidRequest,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}

// invalidState builds an InvalidStateException-tagged error.
func invalidState(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExInvalidState,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}

func itoa(v int32) string {
	return strconv.FormatInt(int64(v), 10)
}
