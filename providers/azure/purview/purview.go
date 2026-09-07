// Package purview provides an in-memory mock of Microsoft Purview
// (Microsoft.Purview/accounts) — the ARM control plane only. It manages the
// Purview account lifecycle (create/update/get/delete/list/listKeys); the data
// plane (the purview.azure.com Atlas catalog, scan and guardian APIs) is out of
// scope.
//
// The resource carries a set of computed, service-minted fields that MUST stay
// stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_purview_account) see no drift on re-plan:
//   - endpoints.catalog / .guardian / .scan: "https://<name>.purview.azure.com/<api>",
//     deterministic from the account name.
//   - managedResources.resourceGroup / .storageAccount / .eventHubNamespace:
//     deterministic ARM ids under the managed resource group.
//   - managedResourceGroupName: "managed-rg-<name>" when the request omits it.
//   - atlasKafkaPrimaryEndpoint / secondaryEndpoint (via ListKeys): deterministic
//     connection strings.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - identity.principalId / identity.tenantId for a system-assigned identity.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches and
// a snapshot/restore.
package purview

import (
	"context"
	"fmt"
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
	providerNamespace = "Microsoft.Purview"
	// resourceType is the ARM resource type segment.
	resourceType = "accounts"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// endpointSuffix is the fixed host of every Purview account endpoint; real
	// Azure emits "https://<name>.purview.azure.com/<catalog|scan|guardian>".
	endpointSuffix = "purview.azure.com"
	// enabled / disabled are the ARM enum values for publicNetworkAccess and
	// managedEventHubState.
	enabled  = "Enabled"
	disabled = "Disabled"
	// defaultSkuName is the sku a Purview account defaults to.
	defaultSkuName = "Standard"
	// defaultSkuCapacity is the capacity a Purview account defaults to.
	defaultSkuCapacity = 1
	// managedRGPrefix is prepended to the account name to derive the default
	// managed resource group name when the request omits one.
	managedRGPrefix = "managed-rg-"
	// storageHashLen / ehHashLen cap the deterministic name suffixes so the
	// generated storage account (24-char lowercase limit) and event hub namespace
	// stay within Azure's name bounds.
	storageHashLen = 16
	ehHashLen      = 12
)

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a Purview account. Type is one of
// SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned". PrincipalID and
// TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// Sku is the pricing tier of a Purview account. Name is the tier (e.g.
// "Standard"); Capacity is the provisioned capacity unit (1/4/16).
type Sku struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

// Endpoints are the deterministic, service-minted data-plane endpoints of a
// Purview account.
type Endpoints struct {
	Catalog  string `json:"catalog"`
	Guardian string `json:"guardian"`
	Scan     string `json:"scan"`
}

// ManagedResources are the deterministic ARM ids of the resources Azure
// provisions in the account's managed resource group.
type ManagedResources struct {
	ResourceGroup     string `json:"resourceGroup"`
	StorageAccount    string `json:"storageAccount"`
	EventHubNamespace string `json:"eventHubNamespace"`
}

// Account is a stored Microsoft.Purview/accounts resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read.
type Account struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`
	Sku           *Sku              `json:"sku,omitempty"`

	// Writable properties (ARM enum strings / config).
	PublicNetworkAccess      string `json:"publicNetworkAccess"`
	ManagedEventHubState     string `json:"managedEventHubState"`
	ManagedResourceGroupName string `json:"managedResourceGroupName"`

	// Computed, stable fields.
	ProvisioningState string            `json:"provisioningState"`
	FriendlyName      string            `json:"friendlyName"`
	Endpoints         *Endpoints        `json:"endpoints"`
	ManagedResources  *ManagedResources `json:"managedResources"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *Account) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// Keys are the deterministic Atlas Kafka connection strings ListKeys returns.
type Keys struct {
	AtlasKafkaPrimaryEndpoint   string
	AtlasKafkaSecondaryEndpoint string
}

// Input carries the mutable fields of a create/update request. The pointer
// fields distinguish "not supplied" (nil, preserve existing) from an explicit
// value, so a PATCH overlays only what it names.
type Input struct {
	Tags                     map[string]string
	Identity                 *Identity
	Sku                      *Sku
	PublicNetworkAccess      *string
	ManagedEventHubState     *string
	ManagedResourceGroupName *string
}

// Mock is the in-memory backend for Purview account resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*Account]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty Purview mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*Account](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new Purview account or updates an existing one. The
// computed fields (endpoints, managedResources, identity ids) are minted
// deterministically so they stay stable across updates. Location and the managed
// resource group name are immutable and preserved on update. It returns the
// stored resource and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name, location string, in *Input) (Account, bool, error) {
	if err := validate(sub, rg, name); err != nil {
		return Account{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	var s Account
	if existed {
		s = *existing
	} else {
		s = newAccount(sub, rg, name, location)
	}

	applyInput(&s, in)

	// Identity is re-resolved only when the request supplies one; a PATCH that
	// omits identity preserves the stored value (an explicit "None" clears it).
	if in.Identity != nil {
		s.Identity = m.resolveIdentity(in.Identity, sub, rg, name)
	}

	mintComputed(&s)

	m.store.Set(k, &s)

	return clone(&s), created, nil
}

// newAccount seeds a fresh resource with its immutable identity, location and the
// ARM default toggle values.
func newAccount(sub, rg, name, location string) Account {
	return Account{
		Subscription:             sub,
		ResourceGroup:            rg,
		Name:                     name,
		Location:                 location,
		PublicNetworkAccess:      enabled,
		ManagedEventHubState:     disabled,
		ManagedResourceGroupName: managedRGPrefix + name,
		Sku:                      &Sku{Name: defaultSkuName, Capacity: defaultSkuCapacity},
		ProvisioningState:        stateSucceeded,
	}
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (Account, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return Account{}, cerrors.Newf(cerrors.NotFound, "purview account %q not found", name)
	}

	return clone(s), nil
}

// ListKeys returns the deterministic Atlas Kafka connection strings for the
// account, or a NotFound error. Real Azure mints per-account keys; here they are
// derived from the name so they are stable across reads.
func (m *Mock) ListKeys(_ context.Context, sub, rg, name string) (Keys, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.store.Get(key(sub, rg, name)); !ok {
		return Keys{}, cerrors.Newf(cerrors.NotFound, "purview account %q not found", name)
	}

	return keysFor(name), nil
}

// Delete removes the resource, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every resource in the group, sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]Account, error) {
	return m.filter(func(s *Account) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]Account, error) {
	return m.filter(func(s *Account) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverAccounts returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverAccounts(_ context.Context) ([]Account, error) {
	return m.filter(func(*Account) bool { return true }), nil
}

// PurgeResourceGroup deletes every Purview account under sub/rg, so a
// resource-group delete cascades into its accounts.
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
func (m *Mock) filter(pred func(*Account) bool) []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Account

	for _, s := range m.store.All() {
		if pred(s) {
			out = append(out, clone(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyInput overlays the mutable request fields onto s, leaving the computed
// fields and the immutable location untouched. A nil pointer field means "not
// supplied": the stored value is preserved.
func applyInput(s *Account, in *Input) {
	if in.Tags != nil {
		s.Tags = maps.Clone(in.Tags)
	}

	if in.Sku != nil {
		s.Sku = &Sku{Name: skuName(in.Sku.Name), Capacity: skuCapacity(in.Sku.Capacity)}
	}

	overlayEnum(&s.PublicNetworkAccess, in.PublicNetworkAccess)
	overlayEnum(&s.ManagedEventHubState, in.ManagedEventHubState)

	// The managed resource group name is fixed once at create (ForceNew in
	// Terraform); it is only adopted from the request when explicitly supplied.
	if in.ManagedResourceGroupName != nil && *in.ManagedResourceGroupName != "" {
		s.ManagedResourceGroupName = *in.ManagedResourceGroupName
	}
}

// skuName / skuCapacity default a partially-specified sku to the standard tier.
func skuName(name string) string {
	if name == "" {
		return defaultSkuName
	}

	return name
}

func skuCapacity(capacity int) int {
	if capacity <= 0 {
		return defaultSkuCapacity
	}

	return capacity
}

// overlayEnum copies a supplied non-empty toggle value onto dst.
func overlayEnum(dst, in *string) {
	if in != nil && *in != "" {
		*dst = normalizeEnum(*in)
	}
}

// mintComputed fills the stable, service-minted fields. Every value is derived
// deterministically from the stored identity, so it never changes on a read.
func mintComputed(s *Account) {
	s.ProvisioningState = stateSucceeded

	if s.ManagedResourceGroupName == "" {
		s.ManagedResourceGroupName = managedRGPrefix + s.Name
	}

	if s.FriendlyName == "" {
		s.FriendlyName = s.Name
	}

	if s.Sku == nil {
		s.Sku = &Sku{Name: defaultSkuName, Capacity: defaultSkuCapacity}
	}

	s.Endpoints = endpointsFor(s.Name)
	s.ManagedResources = managedResourcesFor(s.Subscription, s.ManagedResourceGroupName, s.Name)
}

// endpointsFor builds the deterministic account endpoints, matching the real
// "https://<name>.purview.azure.com/<api>" shape.
func endpointsFor(name string) *Endpoints {
	host := fmt.Sprintf("https://%s.%s", strings.ToLower(name), endpointSuffix)

	return &Endpoints{
		Catalog:  host + "/catalog",
		Guardian: host + "/guardian",
		Scan:     host + "/scan",
	}
}

// managedResourcesFor mints the deterministic ARM ids of the resources Azure
// provisions in the account's managed resource group.
func managedResourcesFor(sub, managedRG, name string) *ManagedResources {
	rgID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", sub, managedRG)

	return &ManagedResources{
		ResourceGroup: rgID,
		StorageAccount: fmt.Sprintf("%s/providers/Microsoft.Storage/storageAccounts/scan%s",
			rgID, hash(storageHashLen, "purview-storage/"+strings.ToLower(name))),
		EventHubNamespace: fmt.Sprintf("%s/providers/Microsoft.EventHub/namespaces/Atlas-%s",
			rgID, hash(ehHashLen, "purview-eventhub/"+strings.ToLower(name))),
	}
}

// keysFor mints the deterministic Atlas Kafka connection strings ListKeys
// returns for the account.
func keysFor(name string) Keys {
	host := "atlas-" + hash(ehHashLen, "purview-eventhub/"+strings.ToLower(name)) + ".servicebus.windows.net:9093"
	primary := hash(0, "purview-kafka-primary/"+strings.ToLower(name))
	secondary := hash(0, "purview-kafka-secondary/"+strings.ToLower(name))

	return Keys{
		AtlasKafkaPrimaryEndpoint: fmt.Sprintf(
			"Endpoint=sb://%s;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=%s", host, primary),
		AtlasKafkaSecondaryEndpoint: fmt.Sprintf(
			"Endpoint=sb://%s;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=%s", host, secondary),
	}
}

// hash returns the hyphen-stripped SyntheticGUID for seed, truncated to n
// characters (n <= 0 returns the full value).
func hash(n int, seed string) string {
	h := strings.ReplaceAll(idgen.SyntheticGUID(seed), "-", "")
	if n > 0 && len(h) > n {
		h = h[:n]
	}

	return h
}

// normalizeEnum canonicalizes a toggle value to the ARM "Enabled"/"Disabled"
// casing, leaving any other value untouched.
func normalizeEnum(v string) string {
	switch {
	case strings.EqualFold(v, enabled):
		return enabled
	case strings.EqualFold(v, disabled):
		return disabled
	default:
		return v
	}
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
		for id := range in.UserAssigned {
			out.UserAssigned[id] = UserAssignedValue{
				PrincipalID: idgen.SyntheticGUID("ua-principal/" + strings.ToLower(id)),
				ClientID:    idgen.SyntheticGUID("ua-client/" + strings.ToLower(id)),
			}
		}
	}

	return out
}

// validate rejects a create/update with missing required identity fields.
func validate(sub, rg, name string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "purview account name is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *Account) Account {
	out := *s
	out.Tags = maps.Clone(s.Tags)

	if s.Sku != nil {
		sku := *s.Sku
		out.Sku = &sku
	}

	if s.Endpoints != nil {
		ep := *s.Endpoints
		out.Endpoints = &ep
	}

	if s.ManagedResources != nil {
		mr := *s.ManagedResources
		out.ManagedResources = &mr
	}

	if s.Identity != nil {
		id := *s.Identity
		id.UserAssigned = maps.Clone(s.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}
