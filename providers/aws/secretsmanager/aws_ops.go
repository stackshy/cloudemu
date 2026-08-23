package secretsmanager

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Version stage labels and the default soft-delete recovery window, matching
// real Secrets Manager staging and DeleteSecret semantics.
const (
	stageCurrent  = "AWSCURRENT"
	stagePrevious = "AWSPREVIOUS"

	recoveryWindowDays = 30
)

// currentIndex returns the index of the current version, or -1 if none.
func currentIndex(versions []driver.SecretVersion) int {
	for i := range versions {
		if versions[i].Current {
			return i
		}
	}

	return -1
}

// MarkVersionBinary flags a version as binary so GetSecretValue returns it as
// SecretBinary rather than SecretString.
func (m *Mock) MarkVersionBinary(_ context.Context, name, versionID string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	for i := range sd.versions {
		if sd.versions[i].VersionID == versionID {
			sd.versions[i].Binary = true

			return nil
		}
	}

	return errors.Newf(errors.NotFound, "version %q not found for secret %q", versionID, name)
}

// GetSecretValueStage returns a secret value addressed by version ID or by stage
// label (AWSCURRENT/AWSPREVIOUS). An empty versionID and stage returns the
// current version.
func (m *Mock) GetSecretValueStage(_ context.Context, name, versionID, stage string) (*driver.SecretVersion, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "secret %q is scheduled for deletion", name)
	}

	if versionID != "" {
		for _, v := range sd.versions {
			if v.VersionID == versionID {
				return copyVersion(v), nil
			}
		}

		return nil, errors.Newf(errors.NotFound, "version %q not found for secret %q", versionID, name)
	}

	cur := currentIndex(sd.versions)
	if cur < 0 {
		return nil, errors.Newf(errors.NotFound, "secret %q has no current version", name)
	}

	switch stage {
	case "", stageCurrent:
		return copyVersion(sd.versions[cur]), nil
	case stagePrevious:
		if cur == 0 {
			return nil, errors.Newf(errors.NotFound, "secret %q has no AWSPREVIOUS version", name)
		}

		return copyVersion(sd.versions[cur-1]), nil
	default:
		return nil, errors.Newf(errors.NotFound, "stage %q not found for secret %q", stage, name)
	}
}

// SecretVersionStages returns the stage labels for each version ID, so
// DescribeSecret can populate VersionIdsToStages.
func (m *Mock) SecretVersionStages(_ context.Context, name string) (map[string][]string, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	stages := make(map[string][]string, len(sd.versions))

	cur := currentIndex(sd.versions)
	if cur >= 0 {
		stages[sd.versions[cur].VersionID] = []string{stageCurrent}

		if cur > 0 {
			stages[sd.versions[cur-1].VersionID] = []string{stagePrevious}
		}
	}

	return stages, nil
}

// SecretDeletionDate reports the scheduled deletion date (RFC3339) for a
// soft-deleted secret, or ok=false when the secret exists but is not deleted.
func (m *Mock) SecretDeletionDate(_ context.Context, name string) (string, bool) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return "", false
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if sd.deletedAt.IsZero() {
		return "", false
	}

	return sd.deletedAt.AddDate(0, 0, recoveryWindowDays).UTC().Format(time.RFC3339), true
}

// RestoreSecret cancels a scheduled deletion, making the secret usable again.
func (m *Mock) RestoreSecret(_ context.Context, name string) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.deletedAt = time.Time{}

	result := sd.info

	return &result, nil
}

// RotateSecret rotates a secret by staging a new current version. Real rotation
// invokes a Lambda to generate the new value; with no Lambda, the emulator
// carries the current value forward under a fresh version ID so callers see the
// version advance and AWSPREVIOUS move as they would in production.
func (m *Mock) RotateSecret(_ context.Context, name string) (*driver.SecretVersion, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "secret %q is scheduled for deletion", name)
	}

	cur := currentIndex(sd.versions)
	if cur < 0 {
		return nil, errors.Newf(errors.FailedPrecondition, "secret %q has no current version to rotate", name)
	}

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)

	prev := sd.versions[cur]
	data := make([]byte, len(prev.Value))
	copy(data, prev.Value)

	for i := range sd.versions {
		sd.versions[i].Current = false
	}

	version := driver.SecretVersion{
		VersionID: idgen.GenerateID("ver-"),
		Value:     data,
		CreatedAt: now,
		Current:   true,
		Binary:    prev.Binary,
	}

	sd.versions = append(sd.versions, version)
	sd.info.UpdatedAt = now

	result := version

	return &result, nil
}

func copyVersion(v driver.SecretVersion) *driver.SecretVersion {
	result := v
	data := make([]byte, len(v.Value))
	copy(data, v.Value)
	result.Value = data

	return &result
}
