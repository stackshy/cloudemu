// Package secretsmanager provides an in-memory mock implementation of AWS Secrets Manager.
package secretsmanager

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Compile-time check that Mock implements driver.Secrets.
var _ driver.Secrets = (*Mock)(nil)

type secretData struct {
	info     driver.SecretInfo
	versions []driver.SecretVersion
	// stages maps a staging label (AWSCURRENT/AWSPREVIOUS/AWSPENDING/custom) to
	// the version id that currently carries it. AWS attaches each label to at
	// most one version, so this is the authoritative per-secret staging index;
	// SecretVersion.Current is kept in sync as a derived convenience for the
	// portable read path.
	stages    map[string]string
	deletedAt time.Time
	// recoveryWindow is the number of days between the delete request and the
	// scheduled deletion date, captured from DeleteSecret's RecoveryWindowInDays.
	recoveryWindow int
	mu             sync.RWMutex
}

// DeleteSecret recovery-window bounds, matching the real service.
const (
	minRecoveryWindowDays     = 7
	maxRecoveryWindowDays     = 30
	defaultRecoveryWindowDays = 30
)

// Mock is an in-memory mock implementation of the AWS Secrets Manager service.
type Mock struct {
	secrets *memstore.Store[*secretData]
	opts    *config.Options
}

// New creates a new Secrets Manager mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		secrets: memstore.New[*secretData](),
		opts:    opts,
	}
}

// CreateSecret creates a new secret with an initial value.
func (m *Mock) CreateSecret(_ context.Context, cfg driver.SecretConfig, value []byte) (*driver.SecretInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "secret name is required")
	}

	if existing, ok := m.secrets.Get(cfg.Name); ok {
		existing.mu.RLock()
		scheduledForDeletion := !existing.deletedAt.IsZero()
		existing.mu.RUnlock()

		if scheduledForDeletion {
			return nil, errors.New(errors.FailedPrecondition,
				"a secret with this name is already scheduled for deletion")
		}

		return nil, errors.Newf(errors.AlreadyExists, "secret %q already exists", cfg.Name)
	}

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)
	// The ARN resource segment ends with a deterministic 6-char suffix
	// (:secret:<name>-<suffix>), matching real Secrets Manager. resolveSecretID
	// on the wire side strips it, so lookups by the friendly name still resolve.
	suffix := idgen.SecretARNSuffix(m.opts.Region + ":" + m.opts.AccountID + ":" + cfg.Name)
	arn := idgen.AWSARN("secretsmanager", m.opts.Region, m.opts.AccountID, "secret:"+cfg.Name+"-"+suffix)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.SecretInfo{
		ID:          idgen.GenerateID("secret-"),
		Name:        cfg.Name,
		ResourceID:  arn,
		Description: cfg.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        tags,
		KMSKeyID:    cfg.KMSKeyID,
	}

	data := make([]byte, len(value))
	copy(data, value)

	// AWS uses the ClientRequestToken as the version id (a UUID); absent one, it
	// generates a UUID itself.
	versionID := cfg.ClientRequestToken
	if versionID == "" {
		versionID = idgen.UUID()
	}

	version := driver.SecretVersion{
		VersionID: versionID,
		Value:     data,
		CreatedAt: now,
		Current:   true,
	}

	sd := &secretData{
		info:     info,
		versions: []driver.SecretVersion{version},
		stages:   map[string]string{stageCurrent: versionID},
	}

	m.secrets.Set(cfg.Name, sd)

	result := info

	return &result, nil
}

// DeleteSecret soft-deletes a secret by name, scheduling it for deletion after
// the default recovery window. It satisfies the portable driver; the AWS wire
// layer uses DeleteSecretWithOptions to honor RecoveryWindowInDays and
// ForceDeleteWithoutRecovery.
func (m *Mock) DeleteSecret(ctx context.Context, name string) error {
	_, _, err := m.DeleteSecretWithOptions(ctx, name, nil, false)

	return err
}

// DeleteSecretWithOptions is the AWS DeleteSecret surface. A nil recoveryWindow
// applies the 30-day default; ForceDeleteWithoutRecovery removes the secret
// permanently (no recovery window, unrecoverable). It returns the deleted
// secret's metadata plus the scheduled DeletionDate (RFC3339). Passing both a
// recovery window and force, or a window outside 7-30, is rejected.
func (m *Mock) DeleteSecretWithOptions(
	_ context.Context, name string, recoveryWindow *int64, force bool,
) (*driver.SecretInfo, string, error) {
	if force && recoveryWindow != nil {
		return nil, "", errors.New(errors.InvalidArgument,
			"RecoveryWindowInDays can't be used together with ForceDeleteWithoutRecovery")
	}

	window := defaultRecoveryWindowDays

	if recoveryWindow != nil {
		if *recoveryWindow < minRecoveryWindowDays || *recoveryWindow > maxRecoveryWindowDays {
			return nil, "", errors.New(errors.InvalidArgument,
				"RecoveryWindowInDays must be between 7 and 30 days")
		}

		window = int(*recoveryWindow)
	}

	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, "", errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	now := m.opts.Clock.Now().UTC()

	if force {
		sd.mu.RLock()
		info := sd.info
		sd.mu.RUnlock()

		m.secrets.Delete(name)

		return &info, now.Format(time.RFC3339), nil
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !sd.deletedAt.IsZero() {
		return nil, "", errors.Newf(errors.FailedPrecondition,
			"You can't perform this operation on the secret because it was marked for deletion.")
	}

	sd.deletedAt = now
	sd.recoveryWindow = window
	info := sd.info

	return &info, now.AddDate(0, 0, window).Format(time.RFC3339), nil
}

// GetSecret retrieves secret metadata by name.
func (m *Mock) GetSecret(_ context.Context, name string) (*driver.SecretInfo, error) {
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

	result := sd.info

	return &result, nil
}

// ListSecrets lists all secrets, excluding soft-deleted ones.
func (m *Mock) ListSecrets(_ context.Context) ([]driver.SecretInfo, error) {
	all := m.secrets.All()

	secrets := make([]driver.SecretInfo, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		if sd.deletedAt.IsZero() {
			secrets = append(secrets, sd.info)
		}
		sd.mu.RUnlock()
	}

	return secrets, nil
}

// PutSecretValue stores a new version of a secret value and promotes it to
// AWSCURRENT. It is the shared cross-cloud append-a-version path; the AWS wire
// layer uses PutSecretValueStaged to honor ClientRequestToken and VersionStages.
func (m *Mock) PutSecretValue(_ context.Context, name string, value []byte) (*driver.SecretVersion, error) {
	return m.PutSecretValueStaged(context.Background(), name, value, "", nil)
}

// PutSecretValueStaged stores a new secret version honoring the AWS
// ClientRequestToken/VersionStages semantics:
//   - clientRequestToken, when set, becomes the new version's id. Reusing it with
//     identical content is an idempotent no-op (the existing version is returned);
//     reusing it with different content is ResourceExistsException.
//   - versionStages, when non-empty, are the exact labels the new version takes,
//     and AWSCURRENT is NOT implied — so staging a candidate as [AWSPENDING]
//     leaves the prior AWSCURRENT untouched. An empty versionStages promotes the
//     new version to AWSCURRENT (demoting the prior current to AWSPREVIOUS).
func (m *Mock) PutSecretValueStaged(
	_ context.Context, name string, value []byte, clientRequestToken string, versionStages []string,
) (*driver.SecretVersion, error) {
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

	data := make([]byte, len(value))
	copy(data, value)

	if clientRequestToken != "" {
		if existing, dup := sd.versionByID(clientRequestToken); dup {
			return m.reusedTokenVersion(existing, data, clientRequestToken)
		}
	}

	versionID := clientRequestToken
	if versionID == "" {
		versionID = idgen.UUID()
	}

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)
	sd.versions = append(sd.versions, driver.SecretVersion{
		VersionID: versionID,
		Value:     data,
		CreatedAt: now,
	})
	sd.applyStages(versionID, versionStages)
	sd.info.UpdatedAt = now

	result, _ := sd.versionByID(versionID)

	return copyVersion(result), nil
}

// reusedTokenVersion enforces ClientRequestToken idempotency: same token + same
// content returns the existing version unchanged; same token + different content
// is ResourceExistsException.
func (*Mock) reusedTokenVersion(
	existing *driver.SecretVersion, data []byte, token string,
) (*driver.SecretVersion, error) {
	if bytes.Equal(existing.Value, data) {
		return copyVersion(existing), nil
	}

	return nil, errors.Newf(errors.AlreadyExists,
		"a version with ClientRequestToken %q already exists with different content", token)
}

// GetSecretValue retrieves a secret value. Empty versionID returns the current version.
func (m *Mock) GetSecretValue(_ context.Context, name, versionID string) (*driver.SecretVersion, error) {
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

	for _, v := range sd.versions {
		if versionID == "" && v.Current {
			result := v

			data := make([]byte, len(v.Value))
			copy(data, v.Value)
			result.Value = data

			return &result, nil
		}

		if v.VersionID == versionID {
			result := v

			data := make([]byte, len(v.Value))
			copy(data, v.Value)
			result.Value = data

			return &result, nil
		}
	}

	return nil, errors.Newf(errors.NotFound, "version %q not found for secret %q", versionID, name)
}

// ListSecretVersions lists all versions of a secret.
func (m *Mock) ListSecretVersions(_ context.Context, name string) ([]driver.SecretVersion, error) {
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

	versions := make([]driver.SecretVersion, len(sd.versions))
	for i, v := range sd.versions {
		versions[i] = driver.SecretVersion{
			VersionID: v.VersionID,
			CreatedAt: v.CreatedAt,
			Current:   v.Current,
		}
	}

	return versions, nil
}
