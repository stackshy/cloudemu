package secretmanager

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// GetSecretIAMPolicy returns the secret's stored IAM policy. An existing secret
// with no policy yet returns an empty, versioned policy (real GCP never 404s
// getIamPolicy on an existing resource).
func (m *Mock) GetSecretIAMPolicy(_ context.Context, name string) (*driver.GCPIAMPolicy, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if sd.iam == nil {
		return &driver.GCPIAMPolicy{Version: 1, Etag: newEtag()}, nil
	}

	return clonePolicy(sd.iam), nil
}

// SetSecretIAMPolicy stores the secret's IAM policy and returns it with a
// refreshed etag.
func (m *Mock) SetSecretIAMPolicy(_ context.Context, name string, policy driver.GCPIAMPolicy) (*driver.GCPIAMPolicy, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	stored := clonePolicy(&policy)
	if stored.Version == 0 {
		stored.Version = 1
	}

	stored.Etag = newEtag()
	sd.iam = stored

	return clonePolicy(stored), nil
}

// TestSecretIAMPermissions echoes back the requested permissions — CloudEmu
// does not enforce IAM, so every requested permission is reported as granted.
func (m *Mock) TestSecretIAMPermissions(_ context.Context, name string, permissions []string) ([]string, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	granted := make([]string, len(permissions))
	copy(granted, permissions)

	return granted, nil
}

// clonePolicy deep-copies an IAM policy so stored and returned values don't
// share backing slices.
func clonePolicy(p *driver.GCPIAMPolicy) *driver.GCPIAMPolicy {
	out := &driver.GCPIAMPolicy{Version: p.Version, Etag: p.Etag}

	for _, b := range p.Bindings {
		members := make([]string, len(b.Members))
		copy(members, b.Members)
		out.Bindings = append(out.Bindings, driver.GCPIAMBinding{Role: b.Role, Members: members})
	}

	return out
}
