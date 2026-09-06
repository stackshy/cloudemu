package cognito

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// invalidParameter builds an InvalidParameterException-tagged error for bad
// input (missing name, unknown enum value, malformed pagination token).
func invalidParameter(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidParameter, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// resourceNotFound builds a ResourceNotFoundException-tagged error for a missing
// user pool or app client, which the Terraform AWS provider recognizes on its
// delete and refresh paths to treat the resource as gone.
func resourceNotFound(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExResourceNotFound, Err: errors.Newf(errors.NotFound, format, args...)}
}
