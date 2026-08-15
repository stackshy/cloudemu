package guardduty

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// conflict builds a ConflictException-tagged error for a duplicate create.
func conflict(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExConflict,
		Err:       errors.Newf(errors.AlreadyExists, format, args...),
	}
}

// badRequest builds a BadRequestException-tagged error for invalid input. This
// is the exception real GuardDuty returns for malformed or missing arguments.
func badRequest(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExBadRequest,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}
