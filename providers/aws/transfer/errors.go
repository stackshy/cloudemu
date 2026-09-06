package transfer

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

// notFound builds a ResourceNotFoundException-tagged error for a missing
// server, user, or SSH key.
func notFound(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExResourceNotFound, Err: errors.Newf(errors.NotFound, format, args...)}
}

// alreadyExists builds a ResourceExistsException-tagged error for a duplicate
// create (server id collision or a user name already on the server).
func alreadyExists(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExResourceExists, Err: errors.Newf(errors.AlreadyExists, format, args...)}
}

// invalidRequest builds an InvalidRequestException-tagged error for malformed
// or invalid input.
func invalidRequest(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidRequest, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}
