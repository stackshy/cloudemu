package location

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(what, id string) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, "%s %s does not exist", what, id),
	}
}

// conflict builds a ConflictException-tagged error for a duplicate create.
func conflict(what, id string) error {
	return &driver.APIError{
		Exception: driver.ExConflict,
		Err:       errors.Newf(errors.AlreadyExists, "%s %s already exists", what, id),
	}
}

// validation builds a ValidationException-tagged error for invalid input.
func validation(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExValidation,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}
