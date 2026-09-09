// Package batch provides an in-memory mock of Azure Batch
// (Microsoft.Batch/batchAccounts) — the ARM control plane only. It manages the
// batchAccount lifecycle (create/update/get/delete/list) plus the account-key
// actions (listKeys/regenerateKey/syncAutoStorageKeys), and the nested
// Microsoft.Batch/batchAccounts/pools child resource (create/update/get/delete/
// list plus the resize/stopResize actions and their allocationState state
// machine).
//
// The Batch data plane — job scheduling, task execution, running compute nodes —
// is out of scope; this surface is the management-plane resource provider only.
//
// Every service-minted field stays stable for the lifetime of the resource so
// infrastructure-as-code tools (Terraform's azurerm_batch_account /
// azurerm_batch_pool) see no drift on re-plan:
//   - account accountEndpoint ("<name>.<region>.batch.azure.com") and
//     nodeManagementEndpoint, deterministic from the name and location.
//   - account provisioningState ("Succeeded"), poolAllocationMode and the
//     dedicated/low-priority core quotas real Azure defaults to.
//   - account primary/secondary keys, minted once at create and stable across
//     every get/listKeys — and across a regenerateKey of the *other* key.
//   - pool allocationState, provisioningState and current/target node counts.
//
// Keys are never echoed on a GET account; they surface only via listKeys /
// regenerateKey, matching real Azure.
package batch

import (
	"context"
	"maps"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace.
	providerNamespace = "Microsoft.Batch"
	// accountType is the ARM account resource type segment.
	accountType = "batchAccounts"
	// poolType is the ARM child resource type segment.
	poolType = "pools"

	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// allocationSteady is the pool allocationState when no resize is in flight.
	allocationSteady = "Steady"
	// allocationResizing is the pool allocationState while a resize is in flight.
	allocationResizing = "Resizing"

	// poolAllocationBatchService is the account poolAllocationMode real Azure
	// defaults to.
	poolAllocationBatchService = "BatchService"
	// endpointSuffix is the fixed tail of a batch account endpoint; real Azure
	// emits "<name>.<region>.batch.azure.com".
	endpointSuffix = "batch.azure.com"
	// nodeMgmtInfix distinguishes the node-management endpoint from the account
	// endpoint: "<name>.<region>.service.batch.azure.com".
	nodeMgmtInfix = "service"
	// defaultResizeTimeout is the fixedScale resizeTimeout real Azure defaults to.
	defaultResizeTimeout = "PT15M"

	// defaultDedicatedCoreQuota is the dedicatedCoreQuota a fresh account reports.
	defaultDedicatedCoreQuota = 20
	// defaultLowPriorityCoreQuota is the lowPriorityCoreQuota default.
	defaultLowPriorityCoreQuota = 20
	// defaultPoolQuota is the poolQuota default.
	defaultPoolQuota = 20
	// defaultJobQuota is the activeJobAndJobScheduleQuota default.
	defaultJobQuota = 20

	// keyNamePrimary / keyNameSecondary are the regenerateKey selectors.
	keyNamePrimary   = "Primary"
	keyNameSecondary = "Secondary"
)

// Account is a stored Microsoft.Batch/batchAccounts resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read.
type Account struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`

	PoolAllocationMode   string `json:"poolAllocationMode"`
	AutoStorageAccountID string `json:"autoStorageAccountId,omitempty"`

	// Computed, stable fields.
	AccountEndpoint                       string `json:"accountEndpoint"`
	NodeManagementEndpoint                string `json:"nodeManagementEndpoint"`
	ProvisioningState                     string `json:"provisioningState"`
	DedicatedCoreQuota                    int    `json:"dedicatedCoreQuota"`
	LowPriorityCoreQuota                  int    `json:"lowPriorityCoreQuota"`
	DedicatedCoreQuotaPerVMFamilyEnforced bool   `json:"dedicatedCoreQuotaPerVMFamilyEnforced"`
	PoolQuota                             int    `json:"poolQuota"`
	ActiveJobAndJobScheduleQuota          int    `json:"activeJobAndJobScheduleQuota"`

	// Account keys, minted once at create and regenerated only on demand. The
	// generation counters make a regenerate deterministic yet distinct: the key
	// derives from the seed plus its generation, so regenerating one key never
	// perturbs the other.
	PrimaryKey      string `json:"primaryKey"`
	SecondaryKey    string `json:"secondaryKey"`
	PrimaryKeyGen   int    `json:"primaryKeyGen"`
	SecondaryKeyGen int    `json:"secondaryKeyGen"`
}

// ARMID returns the fully-qualified ARM resource id of the account.
func (a *Account) ARMID() string {
	return idgen.AzureID(a.Subscription, a.ResourceGroup, providerNamespace, accountType, a.Name)
}

// AccountKeys is the {accountName, primary, secondary} triple listKeys and
// regenerateKey return.
type AccountKeys struct {
	AccountName string
	Primary     string
	Secondary   string
}

// AccountInput carries the mutable fields of an account create/update request.
// The pointer fields distinguish "not supplied" (nil, preserve existing) from an
// explicit value, so a PATCH overlays only what it names.
type AccountInput struct {
	Tags                 map[string]string
	PoolAllocationMode   *string
	AutoStorageAccountID *string
}

// Mock is the in-memory backend for batch accounts and their pools.
type Mock struct {
	mu       sync.RWMutex
	accounts *memstore.Store[*Account]
	pools    *memstore.Store[*Pool]
}

// New creates an empty batch mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		accounts: memstore.New[*Account](),
		pools:    memstore.New[*Pool](),
	}
}

// accountKey is the case-insensitive store key for an account.
func accountKey(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, accountType, name))
}

// CreateOrUpdateAccount creates a new account or updates an existing one. The
// computed fields (endpoints, provisioningState, quotas, keys) are minted once
// at create and preserved across updates. Location is immutable in real Azure
// and is preserved on update. It returns the stored account and whether it was
// newly created.
func (m *Mock) CreateOrUpdateAccount(
	_ context.Context, sub, rg, name, location string, in *AccountInput,
) (Account, bool, error) {
	if err := validateAccount(sub, rg, name, location); err != nil {
		return Account{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := accountKey(sub, rg, name)

	existing, existed := m.accounts.Get(k)
	created := !existed

	var a Account
	if existed {
		a = *existing
	} else {
		a = newAccount(sub, rg, name, location)
	}

	applyAccountInput(&a, in)
	m.accounts.Set(k, &a)

	return cloneAccount(&a), created, nil
}

// newAccount seeds a fresh account with its immutable identity, its computed,
// stable fields and its minted keys.
func newAccount(sub, rg, name, location string) Account {
	id := accountKey(sub, rg, name)

	return Account{
		Subscription:                 sub,
		ResourceGroup:                rg,
		Name:                         name,
		Location:                     location,
		PoolAllocationMode:           poolAllocationBatchService,
		AccountEndpoint:              accountEndpoint(name, location),
		NodeManagementEndpoint:       nodeManagementEndpoint(name, location),
		ProvisioningState:            stateSucceeded,
		DedicatedCoreQuota:           defaultDedicatedCoreQuota,
		LowPriorityCoreQuota:         defaultLowPriorityCoreQuota,
		PoolQuota:                    defaultPoolQuota,
		ActiveJobAndJobScheduleQuota: defaultJobQuota,
		PrimaryKey:                   mintKey("primary/" + id + "#0"),
		SecondaryKey:                 mintKey("secondary/" + id + "#0"),
	}
}

// GetAccount returns the account, or a NotFound error.
func (m *Mock) GetAccount(_ context.Context, sub, rg, name string) (Account, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.accounts.Get(accountKey(sub, rg, name))
	if !ok {
		return Account{}, cerrors.Newf(cerrors.NotFound, "batch account %q not found", name)
	}

	return cloneAccount(a), nil
}

// DeleteAccount removes the account and cascades to every pool under it,
// reporting whether the account existed.
func (m *Mock) DeleteAccount(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existed := m.accounts.Delete(accountKey(sub, rg, name))

	prefix := accountKey(sub, rg, name) + "/" + poolType + "/"
	for pk := range m.pools.All() {
		if strings.HasPrefix(pk, prefix) {
			m.pools.Delete(pk)
		}
	}

	return existed, nil
}

// ListAccountsByResourceGroup returns every account in the group, sorted by name.
func (m *Mock) ListAccountsByResourceGroup(_ context.Context, sub, rg string) ([]Account, error) {
	return m.filterAccounts(func(a *Account) bool {
		return strings.EqualFold(a.Subscription, sub) && strings.EqualFold(a.ResourceGroup, rg)
	}), nil
}

// ListAccountsBySubscription returns every account in the subscription, sorted by
// name.
func (m *Mock) ListAccountsBySubscription(_ context.Context, sub string) ([]Account, error) {
	return m.filterAccounts(func(a *Account) bool {
		return strings.EqualFold(a.Subscription, sub)
	}), nil
}

// DiscoverAccounts returns every stored account, for the inventory walk.
func (m *Mock) DiscoverAccounts(_ context.Context) ([]Account, error) {
	return m.filterAccounts(func(*Account) bool { return true }), nil
}

// ListKeys returns the account's stable keys, or a NotFound error.
func (m *Mock) ListKeys(_ context.Context, sub, rg, name string) (AccountKeys, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.accounts.Get(accountKey(sub, rg, name))
	if !ok {
		return AccountKeys{}, cerrors.Newf(cerrors.NotFound, "batch account %q not found", name)
	}

	return accountKeys(a), nil
}

// RegenerateKey regenerates only the named key ("Primary" or "Secondary"),
// leaving the other key byte-stable, and returns both keys.
func (m *Mock) RegenerateKey(_ context.Context, sub, rg, name, keyName string) (AccountKeys, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := accountKey(sub, rg, name)

	a, ok := m.accounts.Get(k)
	if !ok {
		return AccountKeys{}, cerrors.Newf(cerrors.NotFound, "batch account %q not found", name)
	}

	updated := *a

	switch {
	case strings.EqualFold(keyName, keyNamePrimary):
		updated.PrimaryKeyGen++
		updated.PrimaryKey = mintKey("primary/" + k + "#" + strconv.Itoa(updated.PrimaryKeyGen))
	case strings.EqualFold(keyName, keyNameSecondary):
		updated.SecondaryKeyGen++
		updated.SecondaryKey = mintKey("secondary/" + k + "#" + strconv.Itoa(updated.SecondaryKeyGen))
	default:
		return AccountKeys{}, cerrors.Newf(cerrors.InvalidArgument, "invalid keyName %q", keyName)
	}

	m.accounts.Set(k, &updated)

	return accountKeys(&updated), nil
}

// AccountExists reports whether the named account is stored, without cloning it.
func (m *Mock) AccountExists(_ context.Context, sub, rg, name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.accounts.Has(accountKey(sub, rg, name))
}

// PurgeResourceGroup deletes every account and pool under sub/rg, so a
// resource-group delete cascades into its batch resources.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, a := range m.accounts.All() {
		if strings.EqualFold(a.Subscription, sub) && strings.EqualFold(a.ResourceGroup, rg) {
			m.accounts.Delete(k)
		}
	}

	for k, p := range m.pools.All() {
		if strings.EqualFold(p.Subscription, sub) && strings.EqualFold(p.ResourceGroup, rg) {
			m.pools.Delete(k)
		}
	}

	return nil
}

// filterAccounts returns the accounts matching pred, sorted by name.
func (m *Mock) filterAccounts(pred func(*Account) bool) []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Account

	for _, a := range m.accounts.All() {
		if pred(a) {
			out = append(out, cloneAccount(a))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyAccountInput overlays the mutable request fields onto a, leaving the
// immutable location and computed fields untouched. A nil pointer/map means "not
// supplied": the stored value is preserved, so a PATCH merges only what it names.
func applyAccountInput(a *Account, in *AccountInput) {
	if in.Tags != nil {
		a.Tags = maps.Clone(in.Tags)
	}

	if in.PoolAllocationMode != nil && *in.PoolAllocationMode != "" {
		a.PoolAllocationMode = *in.PoolAllocationMode
	}

	if in.AutoStorageAccountID != nil {
		a.AutoStorageAccountID = *in.AutoStorageAccountID
	}

	if a.PoolAllocationMode == "" {
		a.PoolAllocationMode = poolAllocationBatchService
	}
}

// accountKeys projects a stored account onto its listKeys triple.
func accountKeys(a *Account) AccountKeys {
	return AccountKeys{AccountName: a.Name, Primary: a.PrimaryKey, Secondary: a.SecondaryKey}
}

// accountEndpoint derives the stable account endpoint from its name and location,
// matching the "<name>.<region>.batch.azure.com" form real Azure emits.
func accountEndpoint(name, location string) string {
	return strings.ToLower(name) + "." + region(location) + "." + endpointSuffix
}

// nodeManagementEndpoint derives the stable node-management endpoint, matching the
// "<name>.<region>.service.batch.azure.com" form real Azure emits.
func nodeManagementEndpoint(name, location string) string {
	return strings.ToLower(name) + "." + region(location) + "." + nodeMgmtInfix + "." + endpointSuffix
}

// region normalizes a location to lower-case with whitespace stripped
// (e.g. "West US" -> "westus").
func region(location string) string {
	return strings.ToLower(strings.ReplaceAll(location, " ", ""))
}

// validateAccount rejects an account create/update with missing required fields.
func validateAccount(sub, rg, name, location string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "account name is required")
	case location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// mintKey derives a stable 88-character base64-shaped account key from a seed,
// matching the length of a real Batch account key.
func mintKey(seed string) string {
	a := strings.ReplaceAll(idgen.SyntheticGUID(seed), "-", "")
	b := strings.ReplaceAll(idgen.SyntheticGUID(seed+"#b"), "-", "")
	c := strings.ReplaceAll(idgen.SyntheticGUID(seed+"#c"), "-", "")

	return (a + b + c + a)[:88]
}

// cloneAccount deep-copies a stored account so callers never alias the backing
// store.
func cloneAccount(a *Account) Account {
	out := *a
	out.Tags = maps.Clone(a.Tags)

	return out
}
