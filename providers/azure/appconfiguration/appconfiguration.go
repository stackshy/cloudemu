// Package appconfiguration provides an in-memory mock of Azure App Configuration
// (Microsoft.AppConfiguration/configurationStores) — the ARM control plane only.
// It manages the configuration-store resource lifecycle
// (create/update/get/delete/list) and the listKeys / regenerateKey actions; the
// configuration data plane (the *.azconfig.io key-value store served from a
// separate endpoint) is out of scope.
//
// A configuration store carries a set of computed, service-minted fields that
// MUST stay stable for the lifetime of the resource so infrastructure-as-code
// tools (Terraform's azurerm_app_configuration) see no drift on re-plan:
//   - endpoint: "https://<name>.azconfig.io", deterministic from the name.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - the four access keys (Primary, Secondary, Primary Read Only, Secondary
//     Read Only) — each an id, secret value and connection string.
//   - identity.principalId / identity.tenantId for a system-assigned identity.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches,
// listKeys and a snapshot/restore.
package appconfiguration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace.
	providerNamespace = "Microsoft.AppConfiguration"
	// resourceType is the ARM resource type segment.
	resourceType = "configurationStores"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// endpointSuffix is the fixed tail of every store endpoint; real Azure emits
	// "https://<name>.azconfig.io".
	endpointSuffix = "azconfig.io"
	// keyIDLen is the length (in hex chars) of an access-key id, matching the
	// 16-char uppercase ids real Azure mints (e.g. "439AD01B4BE67DB1").
	keyIDLen = 16
)

// Sku is the pricing tier of a configuration store. Real Azure models the sku as
// a single-field object {name}; azurerm's plain-string sku maps onto Name.
type Sku struct {
	Name string `json:"name"`
}

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a configuration store. Type is one
// of SystemAssigned, UserAssigned, "SystemAssigned, UserAssigned" or None.
// PrincipalID and TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// KeyVaultProperties is the customer-managed-key configuration for encryption.
type KeyVaultProperties struct {
	KeyIdentifier    string `json:"keyIdentifier,omitempty"`
	IdentityClientID string `json:"identityClientId,omitempty"`
}

// Encryption is the encryption-at-rest configuration of a store, stored verbatim
// so it round-trips.
type Encryption struct {
	KeyVaultProperties *KeyVaultProperties `json:"keyVaultProperties,omitempty"`
}

// DataPlaneProxy is the ARM data-plane-proxy configuration, stored verbatim.
type DataPlaneProxy struct {
	AuthenticationMode    string `json:"authenticationMode,omitempty"`
	PrivateLinkDelegation string `json:"privateLinkDelegation,omitempty"`
}

// AccessKey is one of the four minted access keys. Value is the shared secret;
// the connection string is derived from the store endpoint and this key.
type AccessKey struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	ReadOnly bool   `json:"readOnly"`
}

// ConfigurationStore is a stored Microsoft.AppConfiguration/configurationStores
// resource. Subscription, ResourceGroup and Name preserve the caller's casing;
// the computed fields are minted at create and never regenerated on a read.
type ConfigurationStore struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Sku           *Sku              `json:"sku,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`

	DisableLocalAuth          *bool           `json:"disableLocalAuth,omitempty"`
	EnablePurgeProtection     *bool           `json:"enablePurgeProtection,omitempty"`
	PublicNetworkAccess       string          `json:"publicNetworkAccess,omitempty"`
	SoftDeleteRetentionInDays *int            `json:"softDeleteRetentionInDays,omitempty"`
	Encryption                *Encryption     `json:"encryption,omitempty"`
	DataPlaneProxy            *DataPlaneProxy `json:"dataPlaneProxy,omitempty"`

	Endpoint          string      `json:"endpoint"`
	ProvisioningState string      `json:"provisioningState"`
	CreationDate      string      `json:"creationDate"`
	Keys              []AccessKey `json:"keys"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *ConfigurationStore) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// ConnectionString builds the connection string for one of the store's keys.
func (s *ConfigurationStore) ConnectionString(k *AccessKey) string {
	return "Endpoint=" + s.Endpoint + ";Id=" + k.ID + ";Secret=" + k.Value
}

// Input carries the mutable fields of a create/update request.
type Input struct {
	Location                  string
	Tags                      map[string]string
	Sku                       *Sku
	Identity                  *Identity
	DisableLocalAuth          *bool
	EnablePurgeProtection     *bool
	PublicNetworkAccess       string
	SoftDeleteRetentionInDays *int
	Encryption                *Encryption
	DataPlaneProxy            *DataPlaneProxy
}

// Mock is the in-memory backend for configuration stores.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*ConfigurationStore]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty configuration-store mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*ConfigurationStore](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new configuration store or updates an existing one.
// The computed fields (endpoint, keys, identity ids, creationDate) are minted
// once at create and preserved across updates, so they stay stable. Location is
// immutable in real Azure and is preserved on update. It returns the stored
// resource and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in *Input) (ConfigurationStore, bool, error) {
	if err := validate(sub, rg, name, in); err != nil {
		return ConfigurationStore{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	s := ConfigurationStore{Subscription: sub, ResourceGroup: rg, Name: name, Location: in.Location}
	if existed {
		s = *existing
	} else {
		mintComputed(&s, sub, rg, name)
	}

	applyInput(&s, in)
	s.Identity = m.resolveIdentity(in.Identity, sub, rg, name)

	m.store.Set(k, &s)

	return clone(&s), created, nil
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (ConfigurationStore, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return ConfigurationStore{}, cerrors.Newf(cerrors.NotFound, "configuration store %q not found", name)
	}

	return clone(s), nil
}

// Delete removes the resource, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every resource in the group, sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]ConfigurationStore, error) {
	return m.filter(func(s *ConfigurationStore) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]ConfigurationStore, error) {
	return m.filter(func(s *ConfigurationStore) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverConfigurationStores returns every stored resource, for the inventory
// walk.
func (m *Mock) DiscoverConfigurationStores(_ context.Context) ([]ConfigurationStore, error) {
	return m.filter(func(*ConfigurationStore) bool { return true }), nil
}

// PurgeResourceGroup deletes every configuration store under sub/rg, so a
// resource-group delete cascades into its stores.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, s := range m.store.All() {
		if strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}

	return nil
}

// filter returns the resources matching pred, sorted by name for a stable order.
func (m *Mock) filter(pred func(*ConfigurationStore) bool) []ConfigurationStore {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []ConfigurationStore

	for _, s := range m.store.All() {
		if pred(s) {
			out = append(out, clone(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyInput overlays the mutable request fields onto s, leaving the computed
// fields untouched.
func applyInput(s *ConfigurationStore, in *Input) {
	s.Tags = maps.Clone(in.Tags)
	s.Sku = clonePtrSku(in.Sku)
	s.DisableLocalAuth = clonePtrBool(in.DisableLocalAuth)
	s.EnablePurgeProtection = clonePtrBool(in.EnablePurgeProtection)
	s.PublicNetworkAccess = in.PublicNetworkAccess
	s.SoftDeleteRetentionInDays = clonePtrInt(in.SoftDeleteRetentionInDays)
	s.Encryption = cloneEncryption(in.Encryption)
	s.DataPlaneProxy = cloneDataPlaneProxy(in.DataPlaneProxy)
}

// mintComputed fills the stable, service-minted fields once, at create.
func mintComputed(s *ConfigurationStore, sub, rg, name string) {
	id := strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
	s.Endpoint = "https://" + strings.ToLower(name) + "." + endpointSuffix
	s.ProvisioningState = stateSucceeded
	s.CreationDate = time.Now().UTC().Format(time.RFC3339)
	s.Keys = mintKeys(id)
}

// mintKeys derives the four deterministic access keys real Azure returns:
// Primary and Secondary (read-write) plus their read-only counterparts.
func mintKeys(id string) []AccessKey {
	return []AccessKey{
		{ID: keyID("primary/" + id), Name: "Primary", Value: keySecret("primary/" + id), ReadOnly: false},
		{ID: keyID("secondary/" + id), Name: "Secondary", Value: keySecret("secondary/" + id), ReadOnly: false},
		{ID: keyID("primary-ro/" + id), Name: "Primary Read Only", Value: keySecret("primary-ro/" + id), ReadOnly: true},
		{ID: keyID("secondary-ro/" + id), Name: "Secondary Read Only", Value: keySecret("secondary-ro/" + id), ReadOnly: true},
	}
}

// keyID derives a stable 16-hex-character uppercase access-key id from a seed.
func keyID(seed string) string {
	h := strings.ToUpper(strings.ReplaceAll(idgen.SyntheticGUID(seed), "-", ""))

	return h[:keyIDLen]
}

// keySecret derives a stable base64 access-key secret from a seed.
func keySecret(seed string) string {
	sum := sha256.Sum256([]byte(seed))

	return base64.StdEncoding.EncodeToString(sum[:])
}

// resolveIdentity normalizes an incoming managed identity, synthesizing the
// deterministic ids Azure mints on assignment. A nil or "None" identity resolves
// to nil.
func (m *Mock) resolveIdentity(in *Identity, sub, rg, name string) *Identity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, "None") {
		return nil
	}

	out := &Identity{Type: in.Type}

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		id := strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
		out.PrincipalID = idgen.SyntheticGUID("principal/" + id)
		out.TenantID = m.tenantID
	}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]UserAssignedValue, len(in.UserAssigned))
		for uaID := range in.UserAssigned {
			out.UserAssigned[uaID] = UserAssignedValue{
				PrincipalID: idgen.SyntheticGUID("ua-principal/" + strings.ToLower(uaID)),
				ClientID:    idgen.SyntheticGUID("ua-client/" + strings.ToLower(uaID)),
			}
		}
	}

	return out
}

// validate rejects a create/update with missing required fields.
func validate(sub, rg, name string, in *Input) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "configuration store name is required")
	case in.Location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *ConfigurationStore) ConfigurationStore {
	out := *s
	out.Tags = maps.Clone(s.Tags)
	out.Sku = clonePtrSku(s.Sku)
	out.DisableLocalAuth = clonePtrBool(s.DisableLocalAuth)
	out.EnablePurgeProtection = clonePtrBool(s.EnablePurgeProtection)
	out.SoftDeleteRetentionInDays = clonePtrInt(s.SoftDeleteRetentionInDays)
	out.Encryption = cloneEncryption(s.Encryption)
	out.DataPlaneProxy = cloneDataPlaneProxy(s.DataPlaneProxy)
	out.Keys = append([]AccessKey(nil), s.Keys...)

	if s.Identity != nil {
		id := *s.Identity
		id.UserAssigned = maps.Clone(s.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}

func clonePtrSku(in *Sku) *Sku {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func clonePtrBool(in *bool) *bool {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func clonePtrInt(in *int) *int {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func cloneEncryption(in *Encryption) *Encryption {
	if in == nil {
		return nil
	}

	out := &Encryption{}

	if in.KeyVaultProperties != nil {
		kvp := *in.KeyVaultProperties
		out.KeyVaultProperties = &kvp
	}

	return out
}

func cloneDataPlaneProxy(in *DataPlaneProxy) *DataPlaneProxy {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}
