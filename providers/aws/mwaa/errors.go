package mwaa

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

// notFound builds a ResourceNotFoundException-tagged error for an environment
// identified by name or ARN.
func notFound(id string) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, "environment %s not found", id),
	}
}

// validation builds a ValidationException-tagged error for invalid input or a
// duplicate environment (MWAA has no ConflictException; a name already in use
// is reported as a validation error).
func validation(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExValidation,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}
