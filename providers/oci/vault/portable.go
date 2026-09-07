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
// Reads, though, reach every vault, so a secret made through the OCI-shaped
// surface is addressable portably too. OCI scopes secret names to the vault,
// so that reach makes a bare name ambiguous when two vaults both hold it: the
// portable operations reject such a name rather than silently picking one, and
// the portable create refuses to mint a name that another vault already holds.
// With a single vault in play — the ordinary case, and the only one the AWS,
// Azure and GCP secret mocks can have — none of this is observable.
//
// OCI never deletes a secret outright: DeleteSecret schedules the deletion at
// the soonest OCI permits, one day out, and the secret moves to
// PENDING_DELETION. The portable operations then treat it as gone — Get, List
// and the value operations report not-found — while the OCI-shaped surface
// still lists it and CancelSecretDeletion can bring it back, which is the
// same soft-delete the AWS Secrets Manager mock exposes.

// CreateSecret creates a secret with an initial value in the portable driver's
// vault.
//
//nolint:gocritic // hugeParam: driver.Secrets fixes this signature; cfg cannot be a pointer.
func (m *Mock) CreateSecret(
	_ context.Context, cfg driver.SecretConfig, value []byte,
) (*driver.SecretInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "secret name is required")
	}

	// A name another vault already holds would be created here only to be
	// unreadable through this surface, so it is refused up front.
	if other, ok := m.liveSecretByNameLocked(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"secret %q already exists in vault %s", cfg.Name, other.VaultID)
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

	s, err := m.portableSecretLocked(name)
	if err != nil {
		return err
	}

	return scheduleSecret(s, m.earliestDeletion(minSecretDeletionDays))
}

// GetSecret retrieves secret metadata by name.
func (m *Mock) GetSecret(_ context.Context, name string) (*driver.SecretInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, err := m.portableSecretLocked(name)
	if err != nil {
		return nil, err
	}

	info := toPortableInfo(s)

	return &info, nil
}

// ListSecrets returns every secret not pending deletion, ordered by OCID,
// across every vault — the same reach the by-name lookups have.
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

	s, err := m.portableSecretLocked(name)
	if err != nil {
		return nil, err
	}

	v := m.addVersionLocked(s, value, "", StageCurrent)

	return toPortableVersion(v, s.CurrentVersion), nil
}

// GetSecretValue reads one version of a secret. An empty versionID reads the
// CURRENT version; otherwise versionID is OCI's version number.
func (m *Mock) GetSecretValue(_ context.Context, name, versionID string) (*driver.SecretVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, err := m.portableSecretLocked(name)
	if err != nil {
		return nil, err
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

	s, err := m.portableSecretLocked(name)
	if err != nil {
		return nil, err
	}

	out := make([]driver.SecretVersion, 0, len(s.Versions))
	for _, v := range s.Versions {
		out = append(out, *toPortableVersion(v, s.CurrentVersion))
	}

	return out, nil
}

// portableSecretLocked resolves a bare portable name across every vault, since
// a secret made through the OCI-shaped surface is as addressable as one the
// portable driver made. OCI scopes secret names to the vault, so one name can
// reach two secrets; rather than silently picking either, an ambiguous name is
// rejected naming both vaults.
func (m *Mock) portableSecretLocked(name string) (*secretData, error) {
	var found *secretData

	for _, s := range m.secrets.SortedValues() {
		if s.Name != name || s.LifecycleState != StateActive {
			continue
		}

		if found != nil {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"secret %q is ambiguous: it exists in vaults %s and %s", name, found.VaultID, s.VaultID)
		}

		found = s
	}

	if found == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "secret %q not found", name)
	}

	return found, nil
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
