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

// conflictWithID builds a kind-tagged AlreadyExists error that carries the id of
// the existing resource, so the wire layer can surface it (e.g. the existing
// file-system id inside a FileSystemAlreadyExists error for idempotent retries).
func conflictWithID(kind, resourceID, format string, args ...any) error {
	return &driver.ResourceError{
		Kind:       kind,
		ResourceID: resourceID,
		Err:        errors.Newf(errors.AlreadyExists, format, args...),
	}
}

// incorrectFSState builds the IncorrectFileSystemLifeCycleState condition: a
// kind-tagged FailedPrecondition the wire layer maps to that 409 exception.
func incorrectFSState(fileSystemID, state string) error {
	return &driver.ResourceError{Kind: driver.KindFileSystemState, Err: errors.Newf(errors.FailedPrecondition,
		"file system %q is in lifecycle state %q; the operation requires it to be %q",
		fileSystemID, state, driver.StateAvailable)}
}

// inUse builds a kind-tagged FailedPrecondition error.
func inUse(kind, format string, args ...any) error {
	return &driver.ResourceError{Kind: kind, Err: errors.Newf(errors.FailedPrecondition, format, args...)}
}
