package kafka

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// notFound builds a NotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// conflict builds a ConflictException-tagged error (duplicate resource).
func conflict(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExConflict,
		Err:       errors.Newf(errors.AlreadyExists, format, args...),
	}
}

// badRequest builds a BadRequestException-tagged error (invalid input).
func badRequest(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExBadRequest,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}
