package secretsmanager

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// PutResourcePolicy attaches (or replaces) the JSON resource-based policy on a
// secret, the write path Terraform's aws_secretsmanager_secret_policy uses. The
// BlockPublicPolicy check lives in the wire layer, which rejects a public policy
// before calling this. A secret scheduled for deletion is rejected.
func (m *Mock) PutResourcePolicy(_ context.Context, name, policy string) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.New(errors.FailedPrecondition,
			"secret is scheduled for deletion, so this operation is not allowed")
	}

	sd.resourcePolicy = policy
	info := sd.info

	return &info, nil
}

// GetResourcePolicy returns the secret's metadata and its resource policy JSON
// (empty when none is set). It reads leniently so DescribeSecret-style reads
// keep working; only a missing secret is a NotFound.
func (m *Mock) GetResourcePolicy(_ context.Context, name string) (*driver.SecretInfo, string, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, "", errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	info := sd.info

	return &info, sd.resourcePolicy, nil
}

// DeleteResourcePolicy clears the secret's resource policy. A secret scheduled
// for deletion is rejected.
func (m *Mock) DeleteResourcePolicy(_ context.Context, name string) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.New(errors.FailedPrecondition,
			"secret is scheduled for deletion, so this operation is not allowed")
	}

	sd.resourcePolicy = ""
	info := sd.info

	return &info, nil
}
