package kinesisvideo

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(what, id string) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, "%s %s not found", what, id),
	}
}

// inUse builds a ResourceInUseException-tagged error for a duplicate create.
func inUse(what, id string) error {
	return &driver.APIError{
		Exception: driver.ExResourceInUse,
		Err:       errors.Newf(errors.AlreadyExists, "%s %s already exists", what, id),
	}
}

// invalid builds an InvalidArgumentException-tagged error for invalid input.
func invalid(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExInvalidArgument,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}

// invalidResourceFormat builds an InvalidResourceFormatException-tagged error
// for a malformed ARN.
func invalidResourceFormat(arn string) error {
	return &driver.APIError{
		Exception: driver.ExInvalidResourceFormat,
		Err:       errors.Newf(errors.InvalidArgument, "invalid resource ARN format: %s", arn),
	}
}
