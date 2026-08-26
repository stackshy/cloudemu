package secretsmanager

import (
	"context"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Version stage labels, matching real Secrets Manager staging semantics.
const (
	stageCurrent  = "AWSCURRENT"
	stagePrevious = "AWSPREVIOUS"
)

// versionByID returns a pointer to the version with the given id (into the
// backing slice, so callers may mutate it under the lock), or ok=false.
func (sd *secretData) versionByID(id string) (*driver.SecretVersion, bool) {
	for i := range sd.versions {
		if sd.versions[i].VersionID == id {
			return &sd.versions[i], true
		}
	}

	return nil, false
}

// stagesForVersion returns the sorted staging labels attached to a version id.
func (sd *secretData) stagesForVersion(id string) []string {
	var labels []string

	for label, vid := range sd.stages {
		if vid == id {
			labels = append(labels, label)
		}
	}

	sort.Strings(labels)

	return labels
}

// setStage attaches label to versionID, detaching it from any prior holder
// (each label lives on at most one version). It then re-derives Current flags.
func (sd *secretData) setStage(label, versionID string) {
	if sd.stages == nil {
		sd.stages = make(map[string]string)
	}

	sd.stages[label] = versionID
	sd.syncCurrent()
}

// removeStage detaches label from whatever version holds it.
func (sd *secretData) removeStage(label string) {
	delete(sd.stages, label)
	sd.syncCurrent()
}

// promoteToCurrent moves AWSCURRENT to versionID, demoting the version that
// previously held AWSCURRENT to AWSPREVIOUS (and evicting the label from the old
// AWSPREVIOUS holder), mirroring the real service's staging shuffle.
func (sd *secretData) promoteToCurrent(versionID string) {
	if sd.stages == nil {
		sd.stages = make(map[string]string)
	}

	if prevCurrent, ok := sd.stages[stageCurrent]; ok && prevCurrent != versionID {
		sd.stages[stagePrevious] = prevCurrent
	}

	sd.stages[stageCurrent] = versionID
	sd.syncCurrent()
}

// applyStages assigns the requested labels to a freshly appended version. An
// empty request promotes it to AWSCURRENT (with the AWSPREVIOUS shuffle); an
// explicit set attaches exactly those labels (AWSCURRENT among them still runs
// the shuffle), leaving any labels not named where they are.
func (sd *secretData) applyStages(versionID string, requested []string) {
	if len(requested) == 0 {
		sd.promoteToCurrent(versionID)

		return
	}

	for _, label := range requested {
		if label == stageCurrent {
			sd.promoteToCurrent(versionID)

			continue
		}

		sd.setStage(label, versionID)
	}
}

// syncCurrent keeps SecretVersion.Current aligned with the AWSCURRENT label, so
// the portable GetSecretValue/ListSecretVersions read path stays correct.
func (sd *secretData) syncCurrent() {
	current := sd.stages[stageCurrent]
	for i := range sd.versions {
		sd.versions[i].Current = sd.versions[i].VersionID == current
	}
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

	if v, found := sd.versionByID(versionID); found {
		v.Binary = true

		return nil
	}

	return errors.Newf(errors.NotFound, "version %q not found for secret %q", versionID, name)
}

// GetSecretValueStage returns a secret value addressed by version ID or by stage
// label (AWSCURRENT/AWSPREVIOUS/custom). An empty versionID and stage returns the
// current version.
func (m *Mock) GetSecretValueStage(_ context.Context, name, versionID, stage string) (*driver.SecretVersion, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.New(errors.FailedPrecondition,
			"secret is scheduled for deletion, so this operation is not allowed")
	}

	if versionID != "" {
		if v, found := sd.versionByID(versionID); found {
			return copyVersion(v), nil
		}

		return nil, errors.Newf(errors.NotFound, "version %q not found for secret %q", versionID, name)
	}

	label := stage
	if label == "" {
		label = stageCurrent
	}

	target, ok := sd.stages[label]
	if !ok {
		return nil, errors.Newf(errors.NotFound, "stage %q not found for secret %q", label, name)
	}

	v, found := sd.versionByID(target)
	if !found {
		return nil, errors.Newf(errors.NotFound, "stage %q not found for secret %q", label, name)
	}

	return copyVersion(v), nil
}

// SecretVersionStages returns the stage labels for each version ID, so
// DescribeSecret can populate VersionIdsToStages. Versions with no labels
// (deprecated) are omitted.
func (m *Mock) SecretVersionStages(_ context.Context, name string) (map[string][]string, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	stages := make(map[string][]string, len(sd.versions))

	for i := range sd.versions {
		id := sd.versions[i].VersionID
		if labels := sd.stagesForVersion(id); len(labels) > 0 {
			stages[id] = labels
		}
	}

	return stages, nil
}

// UpdateSecretVersionStage moves a staging label between versions, the
// finishSecret step of the AWS rotation contract. moveTo attaches the label to
// that version; removeFrom detaches it (and must currently hold it). Moving
// AWSCURRENT auto-demotes the prior AWSCURRENT to AWSPREVIOUS.
func (m *Mock) UpdateSecretVersionStage(
	_ context.Context, name, versionStage, removeFrom, moveTo string,
) (*driver.SecretInfo, error) {
	if versionStage == "" {
		return nil, errors.New(errors.InvalidArgument, "VersionStage is required")
	}

	if moveTo == "" && removeFrom == "" {
		return nil, errors.New(errors.InvalidArgument,
			"either MoveToVersionId or RemoveFromVersionId must be provided")
	}

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

	if err := sd.validateStageMove(versionStage, removeFrom, moveTo); err != nil {
		return nil, err
	}

	sd.moveStage(versionStage, moveTo)
	sd.info.UpdatedAt = m.opts.Clock.Now().UTC().Format(time.RFC3339)

	info := sd.info

	return &info, nil
}

// validateStageMove checks the target versions exist and that a requested
// removeFrom actually holds the label, matching the real service's rejections.
func (sd *secretData) validateStageMove(versionStage, removeFrom, moveTo string) error {
	if moveTo != "" {
		if _, ok := sd.versionByID(moveTo); !ok {
			return errors.Newf(errors.NotFound, "version %q not found for MoveToVersionId", moveTo)
		}
	}

	if removeFrom != "" {
		if _, ok := sd.versionByID(removeFrom); !ok {
			return errors.Newf(errors.NotFound, "version %q not found for RemoveFromVersionId", removeFrom)
		}

		if holder, ok := sd.stages[versionStage]; !ok || holder != removeFrom {
			return errors.Newf(errors.InvalidArgument,
				"stage %q is not attached to version %q", versionStage, removeFrom)
		}
	}

	return nil
}

// moveStage applies a validated stage move.
func (sd *secretData) moveStage(versionStage, moveTo string) {
	if moveTo != "" {
		if versionStage == stageCurrent {
			sd.promoteToCurrent(moveTo)

			return
		}

		sd.setStage(versionStage, moveTo)

		return
	}

	// removeFrom-only: detach the label entirely (removeFrom already validated).
	sd.removeStage(versionStage)
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

	return sd.deletedAt.AddDate(0, 0, sd.recoveryWindow).UTC().Format(time.RFC3339), true
}

// SecretMetadata returns a secret's metadata even when it is scheduled for
// deletion, so DescribeSecret can report a soft-deleted secret (real Secrets
// Manager keeps DescribeSecret working, returning DeletedDate, until the secret
// is permanently removed). ResourceNotFoundException only for a missing secret.
func (m *Mock) SecretMetadata(_ context.Context, name string) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	result := sd.info

	return &result, nil
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
		return nil, errors.New(errors.FailedPrecondition,
			"secret is scheduled for deletion, so this operation is not allowed")
	}

	curID, ok := sd.stages[stageCurrent]
	if !ok {
		return nil, errors.Newf(errors.FailedPrecondition, "secret %q has no current version to rotate", name)
	}

	cur, _ := sd.versionByID(curID)
	data := make([]byte, len(cur.Value))
	copy(data, cur.Value)

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)
	versionID := idgen.UUID()
	sd.versions = append(sd.versions, driver.SecretVersion{
		VersionID: versionID,
		Value:     data,
		CreatedAt: now,
		Binary:    cur.Binary,
	})
	sd.promoteToCurrent(versionID)
	sd.info.UpdatedAt = now

	result, _ := sd.versionByID(versionID)

	return copyVersion(result), nil
}

func copyVersion(v *driver.SecretVersion) *driver.SecretVersion {
	result := *v
	data := make([]byte, len(v.Value))
	copy(data, v.Value)
	result.Value = data

	return &result
}
