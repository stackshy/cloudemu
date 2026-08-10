package acm

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

func errNotFound(arn string) error {
	return errors.Newf(errors.NotFound, "certificate %q not found", arn)
}

// invalidArn builds an InvalidArnException-tagged error.
func invalidArn(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidArn, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// invalidParameter builds an InvalidParameterException-tagged error.
func invalidParameter(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidParameter, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// invalidState builds an InvalidStateException-tagged error.
func invalidState(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidState, Err: errors.Newf(errors.FailedPrecondition, format, args...)}
}

// tooManyTags builds a TooManyTagsException-tagged error.
func tooManyTags(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExTooManyTags, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}
