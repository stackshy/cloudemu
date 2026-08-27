package secretsmanager

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// UpdateSecret updates a secret's description and, when value is non-nil,
// stores it as a new AWSCURRENT version (SecretsManager UpdateSecret semantics:
// SecretString/SecretBinary are optional). An empty description leaves the
// existing one unchanged. It returns the created version's id (empty when no
// value change created a version), which UpdateSecret echoes to the caller.
func (m *Mock) UpdateSecret(_ context.Context, name, description string, value []byte) (*driver.SecretInfo, string, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, "", errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !sd.deletedAt.IsZero() {
		return nil, "", errors.New(errors.FailedPrecondition,
			"secret is scheduled for deletion, so this operation is not allowed")
	}

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)

	if description != "" {
		sd.info.Description = description
	}

	var versionID string

	if value != nil {
		data := make([]byte, len(value))
		copy(data, value)

		versionID = idgen.UUID()
		sd.versions = append(sd.versions, driver.SecretVersion{
			VersionID: versionID,
			Value:     data,
			CreatedAt: now,
		})
		sd.promoteToCurrent(versionID)
	}

	sd.info.UpdatedAt = now

	result := sd.info

	return &result, versionID, nil
}

// TagSecret adds or overwrites tags on a secret (SecretsManager TagResource).
func (m *Mock) TagSecret(_ context.Context, name string, tags map[string]string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.info.Tags == nil {
		sd.info.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		sd.info.Tags[k] = v
	}

	return nil
}

// UntagSecret removes tags by key from a secret (SecretsManager UntagResource).
func (m *Mock) UntagSecret(_ context.Context, name string, keys []string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	for _, k := range keys {
		delete(sd.info.Tags, k)
	}

	return nil
}
