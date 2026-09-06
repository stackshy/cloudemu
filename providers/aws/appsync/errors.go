package appsync

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// notFound builds a NotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// badRequest builds a BadRequestException-tagged error for invalid input or a
// duplicate resource (AppSync has no distinct AlreadyExists exception).
func badRequest(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExBadRequest,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}

// apiKeyValidityOutOfBounds builds an APIKeyValidityOutOfBoundsException-tagged
// error for an API-key expiry outside the allowed 1..365 day window.
func apiKeyValidityOutOfBounds(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExAPIKeyValidity,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}
