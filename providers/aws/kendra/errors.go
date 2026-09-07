package kendra

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
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
