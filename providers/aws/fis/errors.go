package fis

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// validation builds a ValidationException-tagged error for invalid input.
func validation(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExValidation,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}

// conflict builds a ConflictException-tagged error for an unmet precondition
// (for example stopping an experiment that is no longer running).
func conflict(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExConflict,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}
