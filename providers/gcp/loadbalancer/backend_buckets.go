package loadbalancer

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Compile-time check that Mock implements the backend-bucket capability.
var _ driver.GCPBackendBucketStore = (*Mock)(nil)

// backendBucketScope is the store scope of a backend bucket; they are global.
const backendBucketScope = "global"

// InsertGCPBackendBucket stores a Cloud CDN backend bucket, returning
// AlreadyExists when the name is taken. Collection and Scope are forced so a
// caller can't file a backend bucket under another collection.
//
//nolint:gocritic // hugeParam: interface method signature is fixed.
func (m *Mock) InsertGCPBackendBucket(ctx context.Context, res driver.GCPResource) error {
	res.Collection = driver.GCPBackendBucketCollection
	res.Scope = backendBucketScope

	err := m.PutGCPResource(ctx, res)
	if cerrors.IsAlreadyExists(err) {
		return cerrors.Newf(cerrors.AlreadyExists, "The resource 'backendBuckets/%s' already exists", res.Name)
	}

	return err
}

// GetGCPBackendBucket returns the named backend bucket, or NotFound.
func (m *Mock) GetGCPBackendBucket(_ context.Context, name string) (*driver.GCPResource, error) {
	res, ok := m.gcpResources.Get(gcpResourceKey(driver.GCPBackendBucketCollection, backendBucketScope, name))
	if !ok {
		return nil, backendBucketNotFound(name)
	}

	return &res, nil
}

// ListGCPBackendBuckets returns every backend bucket.
func (m *Mock) ListGCPBackendBuckets(ctx context.Context) ([]driver.GCPResource, error) {
	return m.ListGCPResources(ctx, driver.GCPBackendBucketCollection, backendBucketScope)
}

// UpdateGCPBackendBucket applies mutate to the named backend bucket under the
// store lock. A mutate error leaves the stored record unchanged.
func (m *Mock) UpdateGCPBackendBucket(_ context.Context, name string, mutate func(*driver.GCPResource) error) error {
	var mutateErr error

	updated := m.gcpResources.Update(gcpResourceKey(driver.GCPBackendBucketCollection, backendBucketScope, name),
		func(res driver.GCPResource) driver.GCPResource {
			next := res
			if err := mutate(&next); err != nil {
				mutateErr = err
				return res
			}

			return next
		})
	if !updated {
		return backendBucketNotFound(name)
	}

	return mutateErr
}

// DeleteGCPBackendBucket removes the named backend bucket, or NotFound.
func (m *Mock) DeleteGCPBackendBucket(_ context.Context, name string) error {
	if !m.gcpResources.Delete(gcpResourceKey(driver.GCPBackendBucketCollection, backendBucketScope, name)) {
		return backendBucketNotFound(name)
	}

	return nil
}

// backendBucketNotFound renders compute's not-found message for a backend bucket.
func backendBucketNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "The resource 'backendBuckets/%s' was not found", name)
}
