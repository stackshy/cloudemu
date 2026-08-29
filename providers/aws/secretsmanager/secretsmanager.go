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
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
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
	// resourcePolicy is the JSON resource-based policy attached to the secret
	// (PutResourcePolicy); empty when none is set.
	resourcePolicy string
	mu             sync.RWMutex
}

// DeleteSecret recovery-window bounds, matching the real service.
const (
	minRecoveryWindowDays     = 7
	maxRecoveryWindowDays     = 30
	defaultRecoveryWindowDays = 30
)

// defaultKMSKey is the AWS-managed key Secrets Manager uses to encrypt a secret
// whose CreateSecret request omitted KmsKeyId.
const defaultKMSKey = "alias/aws/secretsmanager"

// KMSCrypto is the KMS seam Secrets Manager uses. DescribeKey validates that an
// explicit KmsKeyId names a real key (rejecting a dangling reference, as real
// Secrets Manager does); Encrypt/Decrypt route the secret value through real KMS
// so its at-rest form is genuine ciphertext and a disabled/deleted key makes the
// read fail. The kmscrypto.Envelope adapter satisfies it.
type KMSCrypto interface {
	DescribeKey(ctx context.Context, keyID string) (*kmsdriver.KeyMetadata, error)
	Encrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}

// Mock is an in-memory mock implementation of the AWS Secrets Manager service.
type Mock struct {
	secrets *memstore.Store[*secretData]
	// kmsCrypto, when wired via SetKMSCrypto, validates a caller-supplied KmsKeyId
	// against KMS and encrypts stored secret values through it. Nil leaves the
	// reference unchecked and stores values in the clear (library fallback).
	kmsCrypto KMSCrypto
	opts      *config.Options
}

// New creates a new Secrets Manager mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		secrets: memstore.New[*secretData](),
		opts:    opts,
	}
}

// SetKMSCrypto wires the KMS backend so a KmsKeyId passed to CreateSecret is
// validated to exist and stored secret values are encrypted through real KMS.
func (m *Mock) SetKMSCrypto(c KMSCrypto) {
	m.kmsCrypto = c
}

// encrypt seals a secret value under kmsKeyID (empty selects the default
// aws/secretsmanager managed key). With no KMS wired it returns the value
// unchanged — the library plaintext fallback.
func (m *Mock) encrypt(ctx context.Context, kmsKeyID string, plaintext []byte) ([]byte, error) {
	if m.kmsCrypto == nil {
		stored := make([]byte, len(plaintext))
		copy(stored, plaintext)

		return stored, nil
	}

	keyRef := kmsKeyID
	if keyRef == "" {
		keyRef = defaultKMSKey
	}

	return m.kmsCrypto.Encrypt(ctx, keyRef, plaintext)
}

// decrypt reverses encrypt. With no KMS wired the stored bytes are already
// plaintext.
func (m *Mock) decrypt(ctx context.Context, stored []byte) ([]byte, error) {
	if m.kmsCrypto == nil {
		return stored, nil
	}

	return m.kmsCrypto.Decrypt(ctx, stored)
}

// decryptVersion decrypts a copied version's Value in place, so every read path
// returns plaintext. A KMS failure (disabled/deleted key) surfaces here.
func (m *Mock) decryptVersion(ctx context.Context, v *driver.SecretVersion) (*driver.SecretVersion, error) {
	plaintext, err := m.decrypt(ctx, v.Value)
	if err != nil {
		return nil, err
	}

	v.Value = plaintext

	return v, nil
}

// CreateSecret creates a new secret with an initial value.
//
//nolint:gocritic // hugeParam: cfg matches the driver.Secrets interface signature.
func (m *Mock) CreateSecret(ctx context.Context, cfg driver.SecretConfig, value []byte) (*driver.SecretInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "secret name is required")
	}

	// A customer-supplied KmsKeyId must reference a key that exists; real Secrets
	// Manager rejects an unknown key with InvalidParameterException rather than
	// storing a dangling reference. The default aws/secretsmanager key (used when
	// KmsKeyId is empty) always exists, so only an explicit reference is checked.
	if cfg.KMSKeyID != "" && m.kmsCrypto != nil {
		if _, err := m.kmsCrypto.DescribeKey(ctx, cfg.KMSKeyID); err != nil {
			return nil, errors.Newf(errors.InvalidArgument,
				"KMS key %q does not exist or is not accessible", cfg.KMSKeyID)
		}
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

	stored, err := m.encrypt(ctx, cfg.KMSKeyID, value)
	if err != nil {
		return nil, err
	}

	// AWS uses the ClientRequestToken as the version id (a UUID); absent one, it
	// generates a UUID itself.
	versionID := cfg.ClientRequestToken
	if versionID == "" {
		versionID = idgen.UUID()
	}

	version := driver.SecretVersion{
		VersionID: versionID,
		Value:     stored,
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
	ctx context.Context, name string, value []byte, clientRequestToken string, versionStages []string,
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

	if clientRequestToken != "" {
		if existing, dup := sd.versionByID(clientRequestToken); dup {
			return m.reusedTokenVersion(ctx, existing, value, clientRequestToken)
		}
	}

	stored, err := m.encrypt(ctx, sd.info.KMSKeyID, value)
	if err != nil {
		return nil, err
	}

	versionID := clientRequestToken
	if versionID == "" {
		versionID = idgen.UUID()
	}

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)
	sd.versions = append(sd.versions, driver.SecretVersion{
		VersionID: versionID,
		Value:     stored,
		CreatedAt: now,
	})
	sd.applyStages(versionID, versionStages)
	sd.info.UpdatedAt = now

	result, _ := sd.versionByID(versionID)

	return copyVersion(result), nil
}

// reusedTokenVersion enforces ClientRequestToken idempotency: same token + same
// content returns the existing version unchanged; same token + different content
// is ResourceExistsException. The comparison is on plaintext — the stored value
// is ciphertext whose bytes differ per write even for identical content.
func (m *Mock) reusedTokenVersion(
	ctx context.Context, existing *driver.SecretVersion, value []byte, token string,
) (*driver.SecretVersion, error) {
	existingPlain, err := m.decrypt(ctx, existing.Value)
	if err != nil {
		return nil, err
	}

	if bytes.Equal(existingPlain, value) {
		return copyVersion(existing), nil
	}

	return nil, errors.Newf(errors.AlreadyExists,
		"a version with ClientRequestToken %q already exists with different content", token)
}

// GetSecretValue retrieves a secret value. Empty versionID returns the current
// version. The stored value is decrypted through KMS; a secret whose KMS key was
// later disabled or deleted fails here with the KMS error, as in real AWS.
func (m *Mock) GetSecretValue(ctx context.Context, name, versionID string) (*driver.SecretVersion, error) {
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

	for i := range sd.versions {
		v := &sd.versions[i]
		if (versionID == "" && v.Current) || v.VersionID == versionID {
			return m.decryptVersion(ctx, copyVersion(v))
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
