package redshift

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func errClusterNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "Redshift cluster %q not found", id)
}

func errClusterSnapshotNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "Redshift cluster snapshot %q not found", id)
}
