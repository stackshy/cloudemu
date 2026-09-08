// Package recoveryservices provides an in-memory mock of Azure Recovery Services
// (Microsoft.RecoveryServices/vaults) — the ARM control plane only. It manages
// the vault lifecycle (create-or-update, get, patch, delete, list-by-group,
// list-by-subscription), the vault's system/user-assigned managed identity and
// SKU, and the three per-vault configuration surfaces: the backup resource vault
// config (soft-delete / security), the backup resource storage config (storage
// redundancy) and the backup policies child collection.
//
// The Recovery Services data plane — Site Recovery replication (fabrics,
// protection containers, recovery plans), protected items, recovery points and
// backup-job execution — is out of scope; this surface is the management-plane
// resource provider only. No workloads are protected and no backups run; a
// policy is a stored document, not an active schedule.
//
// Every service-minted field stays stable for the lifetime of the resource so
// infrastructure-as-code tools (Terraform's azurerm_recovery_services_vault and
// azurerm_backup_policy_vm) see no drift on re-plan:
//   - vault id/name, provisioningState ("Succeeded"), sku, etag and the
//     system-assigned identity's principalId/tenantId, minted once at create and
//     byte-stable across every read/patch.
//   - backup policy id/type/etag, minted once at create.
//
// The vault properties, the backup vault/storage config properties and the
// policy's schedule/retention blocks are stored as raw JSON and round-trip
// verbatim, so a caller reads back exactly what it sent.
package recoveryservices

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace.
	providerNamespace = "Microsoft.RecoveryServices"
	// vaultType is the ARM vault resource-type segment.
	vaultType = "vaults"

	// stateSucceeded is the terminal provisioningState a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"

	// skuStandard is the default vault SKU real Azure assigns.
	skuStandard = "Standard"

	// emulatorTenantID is the single Azure AD directory (tenant) that all
	// system-assigned identities in this emulator belong to. Real Azure has one
	// tenant per directory, so this is a fixed emulator-wide value rather than a
	// per-resource synthesized GUID.
	emulatorTenantID = "11111111-1111-1111-1111-111111111111"
)

// ManagedIdentity is a vault's top-level managed identity. For a system-assigned
// identity the PrincipalID/TenantID are synthesized once (as Azure mints them on
// assignment) and stay stable; UserAssignedIDs holds the assigned user-identity
// resource ids, whose per-identity principal/client ids are synthesized on the
// wire.
type ManagedIdentity struct {
	Type            string   `json:"type"`
	PrincipalID     string   `json:"principalId,omitempty"`
	TenantID        string   `json:"tenantId,omitempty"`
	UserAssignedIDs []string `json:"userAssignedIds,omitempty"`
}

// Vault is a stored Microsoft.RecoveryServices/vaults resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read. Properties holds the
// writable vault properties block and round-trips verbatim.
type Vault struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`

	SkuName  string           `json:"skuName"`
	SkuTier  string           `json:"skuTier,omitempty"`
	Identity *ManagedIdentity `json:"identity,omitempty"`

	Properties json.RawMessage `json:"properties,omitempty"`

	// Computed, stable fields.
	ProvisioningState string `json:"provisioningState"`
	Etag              string `json:"etag"`
}

// ARMID returns the fully-qualified ARM resource id of the vault.
func (v *Vault) ARMID() string {
	return idgen.AzureID(v.Subscription, v.ResourceGroup, providerNamespace, vaultType, v.Name)
}

// VaultInput carries the mutable fields of a vault create/update request. A nil
// pointer/map means "not supplied": on a PATCH the stored value is preserved, so
// the request overlays only what it names.
type VaultInput struct {
	Tags       map[string]string
	SkuName    *string
	SkuTier    *string
	Identity   *ManagedIdentity
	Properties json.RawMessage
}

// Mock is the in-memory backend for vaults, their backup policies and their
// backup vault/storage configs.
type Mock struct {
	mu       sync.RWMutex
	clock    config.Clock
	vaults   *memstore.Store[*Vault]
	policies *memstore.Store[*BackupPolicy]
	configs  *memstore.Store[*Config]
}

// New creates an empty Recovery Services mock. The clock is retained for parity
// with the other Azure mocks; it falls back to the real clock when opts (or its
// clock) is nil so the mock stays usable standalone (e.g. New(nil) in tests).
func New(opts *config.Options) *Mock {
	clock := config.Clock(config.RealClock{})
	if opts != nil && opts.Clock != nil {
		clock = opts.Clock
	}

	return &Mock{
		clock:    clock,
		vaults:   memstore.New[*Vault](),
		policies: memstore.New[*BackupPolicy](),
		configs:  memstore.New[*Config](),
	}
}

// vaultKey is the case-insensitive store key for a vault.
func vaultKey(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, vaultType, name))
}

// CreateOrUpdateVault creates a new vault or replaces an existing one (ARM PUT
// semantics: the properties block is replaced wholesale). The computed fields
// (provisioningState, etag) and the system-assigned identity's principal/tenant
// ids are minted once at create and preserved across updates. Location is
// immutable in real Azure and is preserved on update. It returns the stored
// vault and whether it was newly created.
func (m *Mock) CreateOrUpdateVault(
	_ context.Context, sub, rg, name, location string, in *VaultInput,
) (Vault, bool, error) {
	if err := validateVault(sub, rg, name, location); err != nil {
		return Vault{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := vaultKey(sub, rg, name)

	existing, existed := m.vaults.Get(k)
	created := !existed

	var v Vault
	if existed {
		v = *existing
	} else {
		v = newVault(sub, rg, name, location)
	}

	applyVaultIdentity(&v, in, rg, name)

	if in.Properties != nil {
		v.Properties = append(json.RawMessage(nil), in.Properties...)
	}

	m.vaults.Set(k, &v)

	return cloneVault(&v), created, nil
}

// UpdateVault applies an ARM PATCH: tags are replaced wholesale (resource-level
// PATCH tags semantics), sku/identity are re-resolved only when supplied, and
// the properties block is merged key-by-key onto the stored block. A PATCH on a
// missing vault is a NotFound.
func (m *Mock) UpdateVault(_ context.Context, sub, rg, name string, in *VaultInput) (Vault, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.vaults.Get(vaultKey(sub, rg, name))
	if !ok {
		return Vault{}, cerrors.Newf(cerrors.NotFound, "recovery services vault %q not found", name)
	}

	v := *existing
	applyVaultIdentity(&v, in, rg, name)

	if in.Properties != nil {
		v.Properties = mergeRaw(v.Properties, in.Properties)
	}

	m.vaults.Set(vaultKey(sub, rg, name), &v)

	return cloneVault(&v), nil
}

// newVault seeds a fresh vault with its immutable identity, default SKU and its
// computed, stable fields (provisioningState, etag). The etag derives
// deterministically from the resource id so it is stable yet distinct per vault.
func newVault(sub, rg, name, location string) Vault {
	id := vaultKey(sub, rg, name)

	return Vault{
		Subscription:      sub,
		ResourceGroup:     rg,
		Name:              name,
		Location:          location,
		SkuName:           skuStandard,
		ProvisioningState: stateSucceeded,
		Etag:              idgen.SyntheticGUID("recoveryservices/etag/" + id),
	}
}

// GetVault returns the vault, or a NotFound error.
func (m *Mock) GetVault(_ context.Context, sub, rg, name string) (Vault, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.vaults.Get(vaultKey(sub, rg, name))
	if !ok {
		return Vault{}, cerrors.Newf(cerrors.NotFound, "recovery services vault %q not found", name)
	}

	return cloneVault(v), nil
}

// DeleteVault removes the vault and cascades to every backup policy and config
// under it, reporting whether the vault existed.
func (m *Mock) DeleteVault(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := vaultKey(sub, rg, name)
	existed := m.vaults.Delete(k)

	prefix := k + "/"
	for pk := range m.policies.All() {
		if strings.HasPrefix(pk, prefix) {
			m.policies.Delete(pk)
		}
	}

	for ck, c := range m.configs.All() {
		if vaultKey(c.Subscription, c.ResourceGroup, c.VaultName) == k {
			m.configs.Delete(ck)
		}
	}

	return existed, nil
}

// ListVaultsByResourceGroup returns every vault in the group, sorted by name.
func (m *Mock) ListVaultsByResourceGroup(_ context.Context, sub, rg string) ([]Vault, error) {
	return m.filterVaults(func(v *Vault) bool {
		return strings.EqualFold(v.Subscription, sub) && strings.EqualFold(v.ResourceGroup, rg)
	}), nil
}

// ListVaultsBySubscription returns every vault in the subscription, sorted by
// name.
func (m *Mock) ListVaultsBySubscription(_ context.Context, sub string) ([]Vault, error) {
	return m.filterVaults(func(v *Vault) bool {
		return strings.EqualFold(v.Subscription, sub)
	}), nil
}

// DiscoverVaults returns every stored vault, for the inventory walk.
func (m *Mock) DiscoverVaults(_ context.Context) ([]Vault, error) {
	return m.filterVaults(func(*Vault) bool { return true }), nil
}

// PurgeResourceGroup deletes every vault, policy and config under sub/rg, so a
// resource-group delete cascades into its Recovery Services resources.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, v := range m.vaults.All() {
		if strings.EqualFold(v.Subscription, sub) && strings.EqualFold(v.ResourceGroup, rg) {
			m.vaults.Delete(k)
		}
	}

	m.purgeChildStores(sub, rg)

	return nil
}

// purgeChildStores drops every policy and config whose vault lives in sub/rg.
// The caller holds the write lock.
func (m *Mock) purgeChildStores(sub, rg string) {
	for k, p := range m.policies.All() {
		if strings.EqualFold(p.Subscription, sub) && strings.EqualFold(p.ResourceGroup, rg) {
			m.policies.Delete(k)
		}
	}

	for k, c := range m.configs.All() {
		if strings.EqualFold(c.Subscription, sub) && strings.EqualFold(c.ResourceGroup, rg) {
			m.configs.Delete(k)
		}
	}
}

// filterVaults returns the vaults matching pred, sorted by name.
func (m *Mock) filterVaults(pred func(*Vault) bool) []Vault {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Vault

	for _, v := range m.vaults.All() {
		if pred(v) {
			out = append(out, cloneVault(v))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyVaultIdentity overlays the tags/sku/identity request fields onto v,
// leaving the immutable location and computed fields untouched. A nil
// pointer/map preserves the stored value.
func applyVaultIdentity(v *Vault, in *VaultInput, rg, name string) {
	if in.Tags != nil {
		v.Tags = maps.Clone(in.Tags)
	}

	if in.SkuName != nil && *in.SkuName != "" {
		v.SkuName = *in.SkuName
	}

	if in.SkuTier != nil {
		v.SkuTier = *in.SkuTier
	}

	if in.Identity != nil {
		v.Identity = resolveIdentity(in.Identity, rg, name)
	}

	if v.SkuName == "" {
		v.SkuName = skuStandard
	}
}

// resolveIdentity normalizes an incoming managed identity: for a system-assigned
// identity it synthesizes deterministic principal/tenant GUIDs (as Azure does on
// assignment); a nil or "None" identity resolves to nil.
func resolveIdentity(in *ManagedIdentity, rg, name string) *ManagedIdentity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, "None") {
		return nil
	}

	out := &ManagedIdentity{
		Type:            in.Type,
		UserAssignedIDs: append([]string(nil), in.UserAssignedIDs...),
	}
	sort.Strings(out.UserAssignedIDs)

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		// PrincipalID is per-resource: keying on (resource group, name) keeps two
		// vaults with the same name in different groups distinct, while the value
		// stays stable across gets/restarts for the same vault.
		out.PrincipalID = idgen.SyntheticGUID("recoveryservices/principal/" + rg + "/" + name)
		out.TenantID = emulatorTenantID
	}

	return out
}

// validateVault rejects a vault create/update with missing required fields.
func validateVault(sub, rg, name, location string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "vault name is required")
	case location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// cloneVault deep-copies a stored vault so callers never alias the backing store.
func cloneVault(v *Vault) Vault {
	out := *v
	out.Tags = maps.Clone(v.Tags)
	out.Identity = cloneIdentity(v.Identity)

	if v.Properties != nil {
		out.Properties = append(json.RawMessage(nil), v.Properties...)
	}

	return out
}

// cloneIdentity deep-copies a managed identity, or returns nil.
func cloneIdentity(id *ManagedIdentity) *ManagedIdentity {
	if id == nil {
		return nil
	}

	out := *id
	out.UserAssignedIDs = append([]string(nil), id.UserAssignedIDs...)

	return &out
}

// mergeRaw overlays the top-level keys of patch onto base and returns the merged
// raw JSON object. A malformed base or patch falls back to whichever side parses,
// so a merge never drops the caller's bytes silently.
func mergeRaw(base, patch json.RawMessage) json.RawMessage {
	merged := map[string]json.RawMessage{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &merged); err != nil {
			merged = map[string]json.RawMessage{}
		}
	}

	overlay := map[string]json.RawMessage{}
	if err := json.Unmarshal(patch, &overlay); err != nil {
		return append(json.RawMessage(nil), patch...)
	}

	maps.Copy(merged, overlay)

	raw, err := json.Marshal(merged)
	if err != nil {
		return append(json.RawMessage(nil), patch...)
	}

	return raw
}
