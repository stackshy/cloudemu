package wafv2

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// nonexistent builds a WAFNonexistentItemException-tagged error.
func nonexistent(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExNonexistentItem, Err: errors.Newf(errors.NotFound, format, args...)}
}

// duplicate builds a WAFDuplicateItemException-tagged error.
func duplicate(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExDuplicateItem, Err: errors.Newf(errors.AlreadyExists, format, args...)}
}

// staleLock builds a WAFOptimisticLockException-tagged error.
func staleLock(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExOptimisticLock, Err: errors.Newf(errors.FailedPrecondition, format, args...)}
}

// invalidParameter builds a WAFInvalidParameterException-tagged error.
func invalidParameter(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidParameter, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}
