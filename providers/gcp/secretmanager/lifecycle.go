package secretmanager

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Compile-time check that Mock implements the GCP-specific optional surface.
var _ driver.GCPSecrets = (*Mock)(nil)

// findVersion returns a pointer to the stored version with the given id.
// Caller must hold sd.mu.
func findVersion(sd *secretData, versionID string) (*driver.SecretVersion, bool) {
	for i := range sd.versions {
		v := &sd.versions[i]
		if versionID == "" && v.Current {
			return v, true
		}

		if v.VersionID == versionID {
			return v, true
		}
	}

	return nil, false
}

// mutateVersion loads a live secret, locates a version, and applies fn to it
// under the secret's write lock. It centralizes the not-found/deleted checks
// shared by enable/disable/destroy.
func (m *Mock) mutateVersion(name, versionID string,
	fn func(v *driver.SecretVersion) error,
) (*driver.SecretVersion, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	v, ok := findVersion(sd, versionID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "version %q not found for secret %q", versionID, name)
	}

	if err := fn(v); err != nil {
		return nil, err
	}

	result := *v
	result.Value = nil // lifecycle responses carry metadata only

	return &result, nil
}

// EnableSecretVersion moves a version to ENABLED.
func (m *Mock) EnableSecretVersion(_ context.Context, name, versionID string) (*driver.SecretVersion, error) {
	return m.mutateVersion(name, versionID, func(v *driver.SecretVersion) error {
		if v.State == driver.VersionDestroyed {
			return errors.Newf(errors.FailedPrecondition, "version %q is destroyed", versionID)
		}

		v.State = driver.VersionEnabled
		v.Etag = newEtag()

		return nil
	})
}

// DisableSecretVersion moves a version to DISABLED.
func (m *Mock) DisableSecretVersion(_ context.Context, name, versionID string) (*driver.SecretVersion, error) {
	return m.mutateVersion(name, versionID, func(v *driver.SecretVersion) error {
		if v.State == driver.VersionDestroyed {
			return errors.Newf(errors.FailedPrecondition, "version %q is destroyed", versionID)
		}

		v.State = driver.VersionDisabled
		v.Etag = newEtag()

		return nil
	})
}

// DestroySecretVersion moves a version to DESTROYED, wiping its payload.
func (m *Mock) DestroySecretVersion(_ context.Context, name, versionID string) (*driver.SecretVersion, error) {
	return m.mutateVersion(name, versionID, func(v *driver.SecretVersion) error {
		if v.State == driver.VersionDestroyed {
			return errors.Newf(errors.FailedPrecondition, "version %q is already destroyed", versionID)
		}

		v.State = driver.VersionDestroyed
		v.Value = nil
		v.DestroyTime = m.opts.Clock.Now().UTC().Format(time.RFC3339)
		v.Etag = newEtag()

		return nil
	})
}

// PatchSecret applies a partial update to a secret's metadata.
func (m *Mock) PatchSecret(_ context.Context, name string, patch driver.GCPSecretPatch) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if patch.SetLabels {
		labels := make(map[string]string, len(patch.Labels))
		for k, v := range patch.Labels {
			labels[k] = v
		}

		sd.info.Tags = labels
	}

	sd.info.UpdatedAt = m.opts.Clock.Now().UTC().Format(time.RFC3339)
	sd.info.Etag = newEtag()

	result := sd.info

	return &result, nil
}
