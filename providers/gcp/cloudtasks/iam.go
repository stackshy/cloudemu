package cloudtasks

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
)

// GetIamPolicy returns the queue's stored IAM policy. An existing queue with no
// policy yet returns an empty, versioned policy (real GCP never 404s
// getIamPolicy on an existing resource).
func (m *Mock) GetIamPolicy(_ context.Context, name string) (*driver.IAMPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.queues.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", name)
	}

	if q.IAMPolicy == nil {
		return &driver.IAMPolicy{Version: 1, Etag: newEtag()}, nil
	}

	return clonePolicy(q.IAMPolicy), nil
}

// SetIamPolicy stores the queue's IAM policy and returns it with a refreshed
// etag.
func (m *Mock) SetIamPolicy(_ context.Context, name string, policy driver.IAMPolicy) (*driver.IAMPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.queues.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", name)
	}

	stored := clonePolicy(&policy)
	if stored.Version == 0 {
		stored.Version = 1
	}

	stored.Etag = newEtag()
	q.IAMPolicy = stored
	m.queues.Set(name, q)

	return clonePolicy(stored), nil
}

// TestIamPermissions echoes back the requested permissions — CloudEmu does not
// enforce IAM, so every requested permission is reported as granted.
func (m *Mock) TestIamPermissions(_ context.Context, name string, permissions []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.queues.Has(name) {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", name)
	}

	return cloneStrings(permissions), nil
}

// newEtag returns a fresh opaque optimistic-concurrency tag.
func newEtag() string {
	return idgen.GenerateID("etag-")
}
