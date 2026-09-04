package secretmanager

import (
	"context"
	"fmt"
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

// mutateVersion loads a live secret, locates a version, checks its etag
// precondition, and applies fn to it — all under the secret's write lock, so
// the etag comparison and the mutation happen in one atomic step (a separate
// check-then-write pair would let two concurrent callers starting from the
// same etag both pass the check before either wrote). It centralizes the
// not-found/precondition checks shared by enable/disable/destroy.
func (m *Mock) mutateVersion(name, versionID, etag string,
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

	if etagMismatch(etag, v.Etag) {
		return nil, &driver.GCPSecretPreconditionError{
			Message: fmt.Sprintf("etag mismatch for version %q of secret %q", v.VersionID, name),
		}
	}

	if err := fn(v); err != nil {
		return nil, err
	}

	result := *v
	result.Value = nil // lifecycle responses carry metadata only

	return &result, nil
}

// etagMismatch reports whether a caller-supplied precondition etag is
// non-empty and does not match the currently stored one. An empty etag always
// skips the check, matching real GCP's leniency when the caller omits it.
func etagMismatch(want, got string) bool {
	return want != "" && want != got
}

// EnableSecretVersion moves a version to ENABLED.
func (m *Mock) EnableSecretVersion(_ context.Context, name, versionID, etag string) (*driver.SecretVersion, error) {
	return m.mutateVersion(name, versionID, etag, func(v *driver.SecretVersion) error {
		if v.State == driver.VersionDestroyed {
			return errors.Newf(errors.FailedPrecondition, "version %q is destroyed", versionID)
		}

		v.State = driver.VersionEnabled
		v.Etag = newEtag()

		return nil
	})
}

// DisableSecretVersion moves a version to DISABLED.
func (m *Mock) DisableSecretVersion(_ context.Context, name, versionID, etag string) (*driver.SecretVersion, error) {
	return m.mutateVersion(name, versionID, etag, func(v *driver.SecretVersion) error {
		if v.State == driver.VersionDestroyed {
			return errors.Newf(errors.FailedPrecondition, "version %q is destroyed", versionID)
		}

		v.State = driver.VersionDisabled
		v.Etag = newEtag()

		return nil
	})
}

// DestroySecretVersion moves a version to DESTROYED, wiping its payload.
func (m *Mock) DestroySecretVersion(_ context.Context, name, versionID, etag string) (*driver.SecretVersion, error) {
	return m.mutateVersion(name, versionID, etag, func(v *driver.SecretVersion) error {
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

	if etagMismatch(patch.Etag, sd.info.Etag) {
		return nil, &driver.GCPSecretPreconditionError{Message: fmt.Sprintf("etag mismatch for secret %q", name)}
	}

	if patch.SetLabels {
		sd.info.Tags = copyMap(patch.Labels)
	}

	if patch.SetAnnotations {
		sd.info.Annotations = copyMap(patch.Annotations)
	}

	if patch.SetTopics {
		sd.info.Topics = copySlice(patch.Topics)
	}

	if patch.SetVersionAliases {
		sd.info.VersionAliases = copyMap(patch.VersionAliases)
	}

	if patch.SetRotation {
		sd.info.Rotation = cloneRotation(patch.Rotation)
	}

	if patch.SetExpireTime {
		expireTime, err := resolveExpiry(m.opts.Clock.Now().UTC(), patch.TTL, patch.ExpireTime)
		if err != nil {
			return nil, err
		}

		sd.info.ExpireTime = expireTime
	}

	sd.info.UpdatedAt = m.opts.Clock.Now().UTC().Format(time.RFC3339)
	sd.info.Etag = newEtag()

	result := sd.info

	return &result, nil
}
