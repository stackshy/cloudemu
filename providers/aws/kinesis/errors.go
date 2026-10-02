package kinesis

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

func errNotFoundName(ref string) error {
	return errors.Newf(errors.NotFound, "stream %q not found", ref)
}

func errNotFound(format string, args ...any) error {
	return errors.Newf(errors.NotFound, format, args...)
}

func errInUse(format string, args ...any) error {
	return errors.Newf(errors.FailedPrecondition, format, args...)
}

// invalidArg builds an InvalidArgumentException-tagged error.
func invalidArg(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidArgument, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// expiredIterator builds an ExpiredIteratorException-tagged error.
func expiredIterator(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExExpiredIterator, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}
