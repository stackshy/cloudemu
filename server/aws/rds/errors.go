package rds

import (
	cerrors "github.com/stackshy/cloudemu/errors"
)

func errInstanceNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
}

func errClusterNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", id)
}

func errSnapshotNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "DB snapshot %q not found", id)
}

func errClusterSnapshotNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "DB cluster snapshot %q not found", id)
}
