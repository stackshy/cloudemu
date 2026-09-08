package recoveryservices

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

const (
	// PolicySegment is the ARM backup-policies child-collection segment.
	PolicySegment = "backuppolicies"
	// ConfigSegment is the ARM backup vault-config child segment (the soft-delete
	// / security config, a singleton per vault).
	ConfigSegment = "backupconfig"
	// StorageConfigSegment is the ARM backup storage-config child segment (the
	// storage-redundancy config, a singleton per vault).
	StorageConfigSegment = "backupstorageconfig"
)

// BackupPolicy is a stored .../vaults/backupPolicies child. Its Properties block
// (backupManagementType, schedulePolicy, retentionPolicy, …) is held as raw JSON
// and round-trips verbatim, so the caller reads back exactly the policy it sent.
// Etag is minted once at create and stays stable.
type BackupPolicy struct {
	Subscription  string          `json:"subscription"`
	ResourceGroup string          `json:"resourceGroup"`
	VaultName     string          `json:"vaultName"`
	Name          string          `json:"name"`
	Properties    json.RawMessage `json:"properties,omitempty"`
	Etag          string          `json:"etag"`
}

// ARMID returns the fully-qualified ARM resource id of the policy, nested under
// its parent vault (.../vaults/{vault}/backupPolicies/{name}).
func (p *BackupPolicy) ARMID() string {
	return vaultResourceID(p.Subscription, p.ResourceGroup, p.VaultName) + "/backupPolicies/" + p.Name
}

// ARMType returns the policy's ARM resource type.
func (*BackupPolicy) ARMType() string {
	return providerNamespace + "/" + vaultType + "/backupPolicies"
}

// Config is a stored per-vault configuration singleton — either the
// BackupResourceVaultConfig (soft-delete / security, Kind==ConfigSegment) or the
// BackupResourceStorageConfig (storage redundancy, Kind==StorageConfigSegment).
// The two share an identical shape, so one type and one store back both; Kind
// selects the ARM id/name/type on the wire. Its Properties block round-trips
// verbatim.
type Config struct {
	Subscription  string          `json:"subscription"`
	ResourceGroup string          `json:"resourceGroup"`
	VaultName     string          `json:"vaultName"`
	Kind          string          `json:"kind"`
	Properties    json.RawMessage `json:"properties,omitempty"`
	Etag          string          `json:"etag"`
}

// ARMID returns the fully-qualified ARM resource id of the config singleton,
// keyed off its Kind (.../backupconfig/vaultconfig or
// .../backupstorageconfig/vaultstorageconfig).
func (c *Config) ARMID() string {
	base := vaultResourceID(c.Subscription, c.ResourceGroup, c.VaultName)
	if c.Kind == StorageConfigSegment {
		return base + "/backupstorageconfig/vaultstorageconfig"
	}

	return base + "/backupconfig/vaultconfig"
}

// vaultResourceID returns a vault's ARM resource id with the caller's casing
// preserved (unlike vaultKey, which lower-cases for storage).
func vaultResourceID(sub, rg, vault string) string {
	return idgen.AzureID(sub, rg, providerNamespace, vaultType, vault)
}

// policyKey is the case-insensitive store key for a policy under a vault.
func policyKey(sub, rg, vault, name string) string {
	return vaultKey(sub, rg, vault) + "/" + PolicySegment + "/" + strings.ToLower(name)
}

// CreateOrUpdatePolicy creates or updates a backup policy under its parent vault.
// The parent vault must exist — otherwise it returns a NotFound error (the wire
// layer maps it to ParentResourceNotFound). The etag is minted once at create and
// preserved across updates. It returns the stored policy and whether it was newly
// created.
func (m *Mock) CreateOrUpdatePolicy(
	_ context.Context, sub, rg, vault, name string, properties json.RawMessage,
) (BackupPolicy, bool, error) {
	if err := validatePolicy(sub, rg, vault, name); err != nil {
		return BackupPolicy{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.vaults.Has(vaultKey(sub, rg, vault)) {
		return BackupPolicy{}, false, cerrors.Newf(cerrors.NotFound, "recovery services vault %q not found", vault)
	}

	k := policyKey(sub, rg, vault, name)

	existing, existed := m.policies.Get(k)
	created := !existed

	p := BackupPolicy{Subscription: sub, ResourceGroup: rg, VaultName: vault, Name: name}
	if existed {
		p.Etag = existing.Etag
	} else {
		p.Etag = idgen.SyntheticGUID("recoveryservices/policy-etag/" + k)
	}

	switch {
	case properties != nil:
		p.Properties = append(json.RawMessage(nil), properties...)
	case existed:
		p.Properties = existing.Properties
	}

	m.policies.Set(k, &p)

	return clonePolicy(&p), created, nil
}

// GetPolicy returns the named policy, or a NotFound error.
func (m *Mock) GetPolicy(_ context.Context, sub, rg, vault, name string) (BackupPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.policies.Get(policyKey(sub, rg, vault, name))
	if !ok {
		return BackupPolicy{}, cerrors.Newf(cerrors.NotFound, "backup policy %q not found", name)
	}

	return clonePolicy(p), nil
}

// DeletePolicy removes the named policy, reporting whether it existed. In real
// Azure a policy bound to a protected item cannot be deleted; the emulator does
// not model protected items (data plane, deferred), so a policy is always
// deletable.
func (m *Mock) DeletePolicy(_ context.Context, sub, rg, vault, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.policies.Delete(policyKey(sub, rg, vault, name)), nil
}

// ListPolicies returns every backup policy under a vault, sorted by name.
func (m *Mock) ListPolicies(_ context.Context, sub, rg, vault string) ([]BackupPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := vaultKey(sub, rg, vault) + "/" + PolicySegment + "/"

	var out []BackupPolicy

	for k, p := range m.policies.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, clonePolicy(p))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// GetVaultConfig returns the vault's backup config (soft-delete / security),
// defaulting to soft-delete on / geo-redundant when none was ever written.
func (m *Mock) GetVaultConfig(_ context.Context, sub, rg, vault string) (Config, error) {
	return m.getConfig(sub, rg, vault, ConfigSegment, defaultVaultConfigProps)
}

// UpdateVaultConfig merges the supplied properties onto the vault's backup config
// (its defaults when unset). Both the ARM PUT and PATCH map here — real Azure
// treats each as an update of the security config.
func (m *Mock) UpdateVaultConfig(_ context.Context, sub, rg, vault string, props json.RawMessage) (Config, error) {
	return m.updateConfig(sub, rg, vault, ConfigSegment, defaultVaultConfigProps, props)
}

// GetStorageConfig returns the vault's backup storage config, defaulting to
// geo-redundant with cross-region-restore off when none was ever written.
func (m *Mock) GetStorageConfig(_ context.Context, sub, rg, vault string) (Config, error) {
	return m.getConfig(sub, rg, vault, StorageConfigSegment, defaultStorageConfigProps)
}

// UpdateStorageConfig merges the supplied properties onto the vault's storage
// config. Real ARM locks the storage type once protected items exist; the
// emulator models no protected items (data plane, deferred), so the storage type
// is always changeable.
func (m *Mock) UpdateStorageConfig(_ context.Context, sub, rg, vault string, props json.RawMessage) (Config, error) {
	return m.updateConfig(sub, rg, vault, StorageConfigSegment, defaultStorageConfigProps, props)
}

// getConfig returns the stored config singleton of the given kind, or the
// deterministic defaults when none was written; the returned bytes are stable.
func (m *Mock) getConfig(sub, rg, vault, kind string, def func() json.RawMessage) (Config, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireVault(sub, rg, vault); err != nil {
		return Config{}, err
	}

	if c, ok := m.configs.Get(configKey(kind, sub, rg, vault)); ok {
		return cloneConfig(c), nil
	}

	return newConfig(sub, rg, vault, kind, def()), nil
}

// updateConfig merges props onto the stored config singleton of the given kind
// (its defaults when unset) and stores the result.
func (m *Mock) updateConfig(sub, rg, vault, kind string, def func() json.RawMessage, props json.RawMessage) (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireVault(sub, rg, vault); err != nil {
		return Config{}, err
	}

	k := configKey(kind, sub, rg, vault)

	base := def()
	if existing, ok := m.configs.Get(k); ok {
		base = existing.Properties
	}

	c := newConfig(sub, rg, vault, kind, mergeRaw(base, props))
	m.configs.Set(k, &c)

	return cloneConfig(&c), nil
}

// newConfig builds a config singleton of the given kind with a deterministic,
// stable etag.
func newConfig(sub, rg, vault, kind string, props json.RawMessage) Config {
	return Config{
		Subscription: sub, ResourceGroup: rg, VaultName: vault, Kind: kind,
		Properties: props,
		Etag:       idgen.SyntheticGUID("recoveryservices/" + kind + "/" + vaultKey(sub, rg, vault)),
	}
}

// requireVault returns a NotFound error when the parent vault is absent. The
// caller holds the lock.
func (m *Mock) requireVault(sub, rg, vault string) error {
	if !m.vaults.Has(vaultKey(sub, rg, vault)) {
		return cerrors.Newf(cerrors.NotFound, "recovery services vault %q not found", vault)
	}

	return nil
}

// configKey is the store key for a config singleton, namespaced by kind so the
// two singletons of a vault never collide.
func configKey(kind, sub, rg, vault string) string {
	return kind + "/" + vaultKey(sub, rg, vault)
}

// defaultVaultConfigProps is the Azure-default backup vault config: soft-delete
// enabled, enhanced security on, geo-redundant storage, unlocked.
func defaultVaultConfigProps() json.RawMessage {
	return json.RawMessage(`{"enhancedSecurityState":"Enabled","softDeleteFeatureState":"Enabled",` +
		`"storageModelType":"GeoRedundant","storageType":"GeoRedundant","storageTypeState":"Unlocked"}`)
}

// defaultStorageConfigProps is the Azure-default backup storage config:
// geo-redundant storage, unlocked, cross-region-restore off.
func defaultStorageConfigProps() json.RawMessage {
	return json.RawMessage(`{"storageModelType":"GeoRedundant","storageType":"GeoRedundant",` +
		`"storageTypeState":"Unlocked","crossRegionRestoreFlag":false}`)
}

// validatePolicy rejects a policy create/update with missing required fields.
func validatePolicy(sub, rg, vault, name string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case vault == "":
		return cerrors.New(cerrors.InvalidArgument, "vault name is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "policy name is required")
	default:
		return nil
	}
}

// clonePolicy deep-copies a stored policy so callers never alias the store.
func clonePolicy(p *BackupPolicy) BackupPolicy {
	out := *p
	if p.Properties != nil {
		out.Properties = append(json.RawMessage(nil), p.Properties...)
	}

	return out
}

// cloneConfig deep-copies a stored config singleton so callers never alias the
// store.
func cloneConfig(c *Config) Config {
	out := *c
	if c.Properties != nil {
		out.Properties = append(json.RawMessage(nil), c.Properties...)
	}

	return out
}
