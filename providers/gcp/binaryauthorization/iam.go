package binaryauthorization

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"
)

// GetIamPolicy returns the attestor's stored IAM policy. An existing attestor
// with no policy yet returns an empty, versioned policy (real GCP never 404s
// getIamPolicy on an existing resource).
func (m *Mock) GetIamPolicy(_ context.Context, name string) (*driver.IAMPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.attestors.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "attestor %q not found", name)
	}

	if a.IAMPolicy == nil {
		return &driver.IAMPolicy{Version: 1, Etag: newEtag()}, nil
	}

	return clonePolicyIAM(a.IAMPolicy), nil
}

// SetIamPolicy stores the attestor's IAM policy and returns it with a refreshed
// etag.
func (m *Mock) SetIamPolicy(_ context.Context, name string, policy driver.IAMPolicy) (*driver.IAMPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.attestors.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "attestor %q not found", name)
	}

	stored := clonePolicyIAM(&policy)
	if stored.Version == 0 {
		stored.Version = 1
	}

	stored.Etag = newEtag()
	a.IAMPolicy = stored
	m.attestors.Set(name, a)

	return clonePolicyIAM(stored), nil
}

// TestIamPermissions echoes back the requested permissions — CloudEmu does not
// enforce IAM, so every requested permission is reported as granted.
func (m *Mock) TestIamPermissions(_ context.Context, name string, permissions []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.attestors.Has(name) {
		return nil, cerrors.Newf(cerrors.NotFound, "attestor %q not found", name)
	}

	return cloneStrings(permissions), nil
}
