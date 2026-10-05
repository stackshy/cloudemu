package objectstorage

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// What OCI does instead of the operation being asked for.
const (
	viaIAMPolicy  = "OCI grants bucket access through an Identity policy, not a policy document on the bucket"
	viaNoCORS     = "OCI Object Storage has no per-bucket CORS configuration"
	viaObjectMeta = "an OCI object carries opc-meta- user metadata, not tags; use UpdateObjectMetadata"
)

// unsupported reports an operation with no OCI equivalent.
func unsupported(operation, instead string) error {
	return cerrors.Newf(cerrors.Unimplemented, "%s is not an OCI operation: %s", operation, instead)
}

// PutBucketPolicy is not an OCI operation.
func (*Mock) PutBucketPolicy(_ context.Context, _ string, _ driver.BucketPolicy) error {
	return unsupported("PutBucketPolicy", viaIAMPolicy)
}

// GetBucketPolicy is not an OCI operation.
func (*Mock) GetBucketPolicy(_ context.Context, _ string) (*driver.BucketPolicy, error) {
	return nil, unsupported("GetBucketPolicy", viaIAMPolicy)
}

// DeleteBucketPolicy is not an OCI operation.
func (*Mock) DeleteBucketPolicy(_ context.Context, _ string) error {
	return unsupported("DeleteBucketPolicy", viaIAMPolicy)
}

// PutCORSConfig is not an OCI operation.
func (*Mock) PutCORSConfig(_ context.Context, _ string, _ driver.CORSConfig) error {
	return unsupported("PutCORSConfig", viaNoCORS)
}

// GetCORSConfig is not an OCI operation.
func (*Mock) GetCORSConfig(_ context.Context, _ string) (*driver.CORSConfig, error) {
	return nil, unsupported("GetCORSConfig", viaNoCORS)
}

// DeleteCORSConfig is not an OCI operation.
func (*Mock) DeleteCORSConfig(_ context.Context, _ string) error {
	return unsupported("DeleteCORSConfig", viaNoCORS)
}

// PutObjectTagging is not an OCI operation.
func (*Mock) PutObjectTagging(_ context.Context, _, _ string, _ map[string]string) error {
	return unsupported("PutObjectTagging", viaObjectMeta)
}

// GetObjectTagging is not an OCI operation.
func (*Mock) GetObjectTagging(_ context.Context, _, _ string) (map[string]string, error) {
	return nil, unsupported("GetObjectTagging", viaObjectMeta)
}

// DeleteObjectTagging is not an OCI operation.
func (*Mock) DeleteObjectTagging(_ context.Context, _, _ string) error {
	return unsupported("DeleteObjectTagging", viaObjectMeta)
}
