// Package keyvault provides an in-memory mock implementation of Azure Key Vault.
package keyvault

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Compile-time checks that Mock implements the shared Secrets driver and the
// Azure-specific KeyVaultSecrets, KeyVaultKeys and KeyVaultCertificates surfaces.
var (
	_ driver.Secrets              = (*Mock)(nil)
	_ driver.KeyVaultSecrets      = (*Mock)(nil)
	_ driver.KeyVaultKeys         = (*Mock)(nil)
	_ driver.KeyVaultCertificates = (*Mock)(nil)
)

// purgeWindowDays is the soft-delete retention window Key Vault schedules
// between deletion and automatic purge.
const purgeWindowDays = 90

// secretVersion is one stored secret version with its Key Vault attributes.
type secretVersion struct {
	versionID   string
	value       []byte
	contentType string
	tags        map[string]string
	enabled     bool
	expires     int64
	notBefore   int64
	created     time.Time
	updated     time.Time
	current     bool
}

type secretData struct {
	info           driver.SecretInfo
	versions       []secretVersion
	deletedAt      time.Time
	scheduledPurge time.Time
	mu             sync.RWMutex
}

// vaultData is one Key Vault vault's isolated secrets/keys/certificates
// namespace. The KeyVaultSecrets, KeyVaultKeys and KeyVaultCertificates
// surfaces (server/azure/keyvault) each resolve the caller's vault name to one
// of these before touching storage, so an object created in one vault is never
// visible through another.
type vaultData struct {
	secrets *memstore.Store[*secretData]
	keys    *memstore.Store[*keyData]
	certs   *memstore.Store[*certData]
}

func newVaultData() *vaultData {
	return &vaultData{
		secrets: memstore.New[*secretData](),
		keys:    memstore.New[*keyData](),
		certs:   memstore.New[*certData](),
	}
}

// defaultVault is the vault name that backs the shared, cross-cloud Secrets
// driver interface (CreateSecret, GetSecret, PutSecretValue, ...), which has
// no notion of a vault. A caller that reaches the Key Vault data-plane API
// under this vault name (the wire layer's host-extraction fallback for a
// request whose Host carries no recognizable vault subdomain — see
// vaultFromRequest in server/azure/keyvault) sees the exact same secrets and
// keys the portable API manages, preserving pre-multi-vault behavior. Any
// other vault name gets its own fully isolated namespace.
const defaultVault = "default"

// Mock is an in-memory mock implementation of Azure Key Vault.
type Mock struct {
	// secrets and keys are the defaultVault's stores, kept as direct fields so
	// the shared Secrets driver interface methods (which carry no vault name)
	// can use them without a map lookup.
	secrets *memstore.Store[*secretData]
	keys    *memstore.Store[*keyData]
	// vaults backs the Azure-specific KeyVaultSecrets/KeyVaultKeys data-plane
	// surface, keyed by vault name, so secrets/keys are isolated per vault.
	// vaults[defaultVault] aliases secrets/keys above.
	vaults *memstore.Store[*vaultData]
	opts   *config.Options
}

// New creates a new Key Vault mock with the given configuration options.
func New(opts *config.Options) *Mock {
	secrets := memstore.New[*secretData]()
	keys := memstore.New[*keyData]()

	vaults := memstore.New[*vaultData]()
	vaults.Set(defaultVault, &vaultData{secrets: secrets, keys: keys, certs: memstore.New[*certData]()})

	return &Mock{
		secrets: secrets,
		keys:    keys,
		vaults:  vaults,
		opts:    opts,
	}
}

// vault returns the named vault's secret/key store, creating it on first
// touch. Key Vault vaults have no separate ARM "create vault" step in the
// data-plane surface cloudemu emulates, so a vault springs into existence the
// first time any secret or key name is addressed within it.
func (m *Mock) vault(name string) *vaultData {
	if vd, ok := m.vaults.Get(name); ok {
		return vd
	}

	vd := newVaultData()
	if m.vaults.SetIfAbsent(name, vd) {
		return vd
	}

	// Lost the race to a concurrent first touch of the same vault name;
	// use the winner's store instead of orphaning ours.
	existing, _ := m.vaults.Get(name)

	return existing
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)

	return out
}

// CreateSecret creates a new secret with an initial value.
func (m *Mock) CreateSecret(_ context.Context, cfg driver.SecretConfig, value []byte) (*driver.SecretInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "secret name is required")
	}

	if existing, ok := m.secrets.Get(cfg.Name); ok {
		existing.mu.RLock()
		deleted := !existing.deletedAt.IsZero()
		existing.mu.RUnlock()

		if deleted {
			// Real Key Vault returns 409 Conflict / ObjectIsDeletedButRecoverable:
			// a soft-deleted name cannot be reused until Recover or Purge.
			return nil, errors.Newf(errors.AlreadyExists, "secret %q is in a deleted but recoverable state", cfg.Name)
		}

		return nil, errors.Newf(errors.AlreadyExists, "secret %q already exists", cfg.Name)
	}

	now := m.opts.Clock.Now().UTC()
	resourceID := idgen.AzureID(m.opts.AccountID, "rg-default", "Microsoft.KeyVault", "vaults/default/secrets", cfg.Name)

	info := driver.SecretInfo{
		ID:          idgen.GenerateID("secret-"),
		Name:        cfg.Name,
		ResourceID:  resourceID,
		Description: cfg.Description,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		Tags:        copyTags(cfg.Tags),
	}

	sd := &secretData{
		info: info,
		versions: []secretVersion{{
			versionID: idgen.GenerateID("ver-"),
			value:     copyBytes(value),
			tags:      copyTags(cfg.Tags),
			enabled:   true,
			created:   now,
			updated:   now,
			current:   true,
		}},
	}

	m.secrets.Set(cfg.Name, sd)

	result := info

	return &result, nil
}

// liveSecret returns the stored secret if it exists and is not soft-deleted.
func (m *Mock) liveSecret(name string) *secretData {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil
	}

	sd.mu.RLock()
	deleted := !sd.deletedAt.IsZero()
	sd.mu.RUnlock()

	if deleted {
		return nil
	}

	return sd
}

// DeleteSecret soft-deletes a secret by name.
func (m *Mock) DeleteSecret(_ context.Context, name string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !sd.deletedAt.IsZero() {
		return errors.Newf(errors.NotFound, "secret %q is scheduled for deletion", name)
	}

	m.markDeletedLocked(sd)

	return nil
}

func (m *Mock) markDeletedLocked(sd *secretData) {
	now := m.opts.Clock.Now().UTC()
	sd.deletedAt = now
	sd.scheduledPurge = now.AddDate(0, 0, purgeWindowDays)
}

// GetSecret retrieves secret metadata by name.
func (m *Mock) GetSecret(_ context.Context, name string) (*driver.SecretInfo, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

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

// PutSecretValue stores a new version of a secret value.
func (m *Mock) PutSecretValue(_ context.Context, name string, value []byte) (*driver.SecretVersion, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	// PutSecretValue is the shared cross-cloud rotate-value path. Like AWS
	// Secrets Manager PutSecretValue and GCP AddSecretVersion, adding a version
	// must not touch resource-level tags — those are managed by separate tag
	// APIs. Preserve the secret's existing tags rather than clearing them.
	v := m.appendVersionLocked(sd, driver.KVSetParams{Value: value, Attributes: driver.KVAttributes{Enabled: true}}, false)

	return &driver.SecretVersion{
		VersionID: v.versionID,
		Value:     copyBytes(v.value),
		CreatedAt: v.created.Format(time.RFC3339),
		Current:   true,
	}, nil
}

// appendVersionLocked adds a new current version from the given params. When
// updateSecretTags is true the secret-level tags are replaced with the params'
// tags (Azure Key Vault SetSecret semantics); otherwise the existing
// secret-level tags are preserved (shared PutSecretValue semantics). The caller
// must hold sd.mu.
func (m *Mock) appendVersionLocked(sd *secretData, params driver.KVSetParams, updateSecretTags bool) *secretVersion {
	now := m.opts.Clock.Now().UTC()

	for i := range sd.versions {
		sd.versions[i].current = false
	}

	enabled := params.Attributes.Enabled

	v := secretVersion{
		versionID:   idgen.GenerateID("ver-"),
		value:       copyBytes(params.Value),
		contentType: params.ContentType,
		tags:        copyTags(params.Tags),
		enabled:     enabled,
		expires:     params.Attributes.Expires,
		notBefore:   params.Attributes.NotBefore,
		created:     now,
		updated:     now,
		current:     true,
	}

	sd.versions = append(sd.versions, v)
	sd.info.UpdatedAt = now.Format(time.RFC3339)

	if updateSecretTags {
		sd.info.Tags = copyTags(params.Tags)
	}

	return &sd.versions[len(sd.versions)-1]
}

// GetSecretValue retrieves a secret value. Empty versionID returns the current version.
func (m *Mock) GetSecretValue(_ context.Context, name, versionID string) (*driver.SecretVersion, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	v := findVersion(sd, versionID)
	if v == nil {
		return nil, errors.Newf(errors.NotFound, "version %q not found for secret %q", versionID, name)
	}

	return &driver.SecretVersion{
		VersionID: v.versionID,
		Value:     copyBytes(v.value),
		CreatedAt: v.created.Format(time.RFC3339),
		Current:   v.current,
	}, nil
}

// findVersion returns the version with the given id, or the current version
// when versionID is empty. The caller must hold sd.mu.
func findVersion(sd *secretData, versionID string) *secretVersion {
	for i := range sd.versions {
		v := &sd.versions[i]
		if versionID == "" && v.current {
			return v
		}

		if v.versionID == versionID {
			return v
		}
	}

	return nil
}

// ListSecretVersions lists all versions of a secret (metadata only).
func (m *Mock) ListSecretVersions(_ context.Context, name string) ([]driver.SecretVersion, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	versions := make([]driver.SecretVersion, len(sd.versions))
	for i := range sd.versions {
		v := &sd.versions[i]
		versions[i] = driver.SecretVersion{
			VersionID: v.versionID,
			CreatedAt: v.created.Format(time.RFC3339),
			Current:   v.current,
		}
	}

	return versions, nil
}
