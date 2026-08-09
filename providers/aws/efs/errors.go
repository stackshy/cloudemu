package efs

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// notFound builds a kind-tagged NotFound error so the wire layer can emit the
// precise EFS exception (e.g. MountTargetNotFound vs FileSystemNotFound).
func notFound(kind, format string, args ...any) error {
	return &driver.ResourceError{Kind: kind, Err: errors.Newf(errors.NotFound, format, args...)}
}

// conflict builds a kind-tagged AlreadyExists error.
func conflict(kind, format string, args ...any) error {
	return &driver.ResourceError{Kind: kind, Err: errors.Newf(errors.AlreadyExists, format, args...)}
}

// inUse builds a kind-tagged FailedPrecondition error.
func inUse(kind, format string, args ...any) error {
	return &driver.ResourceError{Kind: kind, Err: errors.Newf(errors.FailedPrecondition, format, args...)}
}
