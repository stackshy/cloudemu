package vault

import (
	"context"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// The portable secrets driver, mapped onto OCI Vault.
//
// OCI has no secret outside a vault, so the portable create mints a vault and
// a master encryption key on first use and puts every portable secret there.
//
// OCI never deletes a secret outright: DeleteSecret schedules the deletion at
// the soonest OCI permits, one day out, and the secret moves to
// PENDING_DELETION. The portable operations then treat it as gone — Get, List
// and the value operations report not-found — while the OCI-shaped surface
// still lists it and CancelSecretDeletion can bring it back, which is the
// same soft-delete the AWS Secrets Manager mock exposes.

// CreateSecret creates a secret with an initial value in the portable driver's
// vault.
func (m *Mock) CreateSecret(
	_ context.Context, cfg driver.SecretConfig, value []byte,
) (*driver.SecretInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "secret name is required")
	}

	vaultID, keyID := m.defaultVaultLocked()

	spec := &SecretSpec{
		VaultID:      vaultID,
		KeyID:        keyID,
		Name:         cfg.Name,
		Description:  cfg.Description,
		Content:      value,
		FreeformTags: cfg.Tags,
	}

	if err := m.validateSecretSpecLocked(spec); err != nil {
		return nil, err
	}

	info := toPortableInfo(m.newSecretLocked(spec))

	return &info, nil
}

// DeleteSecret schedules the secret's deletion at the soonest OCI permits. The
// secret keeps its OCID and versions, and CancelSecretDeletion restores it.
func (m *Mock) DeleteSecret(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.secretByNameLocked(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "secret %q not found", name)
	}

	return scheduleSecret(s, m.earliestDeletion(minSecretDeletionDays))
}

// GetSecret retrieves secret metadata by name.
func (m *Mock) GetSecret(_ context.Context, name string) (*driver.SecretInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.secretByNameLocked(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "secret %q not found", name)
	}

	info := toPortableInfo(s)

	return &info, nil
}

// ListSecrets returns every secret not pending deletion, ordered by OCID.
func (m *Mock) ListSecrets(_ context.Context) ([]driver.SecretInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]driver.SecretInfo, 0, m.secrets.Len())

	for _, s := range m.secrets.SortedValues() {
		if s.LifecycleState != StateActive {
			continue
		}

		out = append(out, toPortableInfo(s))
	}

	return out, nil
}

// PutSecretValue writes a new version of a secret and makes it CURRENT.
func (m *Mock) PutSecretValue(_ context.Context, name string, value []byte) (*driver.SecretVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.secretByNameLocked(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "secret %q not found", name)
	}

	v := m.addVersionLocked(s, value, "", StageCurrent)

	return toPortableVersion(v, s.CurrentVersion), nil
}

// GetSecretValue reads one version of a secret. An empty versionID reads the
// CURRENT version; otherwise versionID is OCI's version number.
func (m *Mock) GetSecretValue(_ context.Context, name, versionID string) (*driver.SecretVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.secretByNameLocked(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "secret %q not found", name)
	}

	sel, err := portableSelector(versionID)
	if err != nil {
		return nil, err
	}

	v, err := selectVersion(s, sel)
	if err != nil {
		return nil, err
	}

	return toPortableVersion(v, s.CurrentVersion), nil
}

// ListSecretVersions returns every version of a secret, oldest first.
func (m *Mock) ListSecretVersions(_ context.Context, name string) ([]driver.SecretVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.secretByNameLocked(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "secret %q not found", name)
	}

	out := make([]driver.SecretVersion, 0, len(s.Versions))
	for _, v := range s.Versions {
		out = append(out, *toPortableVersion(v, s.CurrentVersion))
	}

	return out, nil
}

// defaultVaultLocked returns the vault and key the portable driver stores its
// secrets in, creating them on first use.
func (m *Mock) defaultVaultLocked() (vaultID, keyID string) {
	if m.vaults.Has(m.defaultVaultID) && m.keys.Has(m.defaultKeyID) {
		return m.defaultVaultID, m.defaultKeyID
	}

	v := m.newVaultLocked(&VaultSpec{DisplayName: defaultVaultName}, VaultTypeDefault)
	k := m.newKeyLocked(&KeySpec{
		VaultID:     v.ID,
		DisplayName: defaultKeyName,
		Shape:       KeyShape{Algorithm: AlgorithmAES, Length: 32},
	}, ProtectionModeHSM)

	m.defaultVaultID = v.ID
	m.defaultKeyID = k.ID

	return v.ID, k.ID
}

// portableSelector turns the portable version identifier into a bundle
// selector. OCI numbers versions, so a non-numeric identifier cannot name one.
func portableSelector(versionID string) (BundleSelector, error) {
	if versionID == "" {
		return BundleSelector{}, nil
	}

	n, err := strconv.ParseInt(versionID, 10, 64)
	if err != nil {
		return BundleSelector{}, cerrors.Newf(cerrors.InvalidArgument,
			"version %q is not an OCI secret version number", versionID)
	}

	return BundleSelector{VersionNumber: &n}, nil
}

// toPortableInfo projects a secret onto the portable shape. OCI has no ARN or
// self link, so the OCID serves as both the identifier and the resource ID.
func toPortableInfo(s *secretData) driver.SecretInfo {
	return driver.SecretInfo{
		ID:          s.ID,
		Name:        s.Name,
		ResourceID:  s.ID,
		Description: s.Description,
		CreatedAt:   s.TimeCreated,
		UpdatedAt:   s.TimeUpdated,
		Tags:        copyTags(s.FreeformTags),
	}
}

// toPortableVersion projects a version onto the portable shape, whose version
// identifier is OCI's version number.
func toPortableVersion(v *versionData, current int64) *driver.SecretVersion {
	return &driver.SecretVersion{
		VersionID: strconv.FormatInt(v.Number, 10),
		Value:     append([]byte(nil), v.Content...),
		CreatedAt: v.TimeCreated,
		Current:   v.Number == current,
	}
}
