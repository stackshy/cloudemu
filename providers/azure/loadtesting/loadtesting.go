// Package loadtesting provides an in-memory mock of Azure Load Testing
// (Microsoft.LoadTestService/loadTests) — the ARM control plane only. It manages
// the load-test resource lifecycle (create/update/get/delete/list); the data
// plane (uploading test plans, running load tests, streaming results) is out of
// scope.
//
// A load-test resource carries three computed, service-minted fields that MUST
// stay stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_load_test) see no drift on re-plan:
//   - dataPlaneURI: the resource's data-plane hostname, minted once at create.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - identity.principalId / identity.tenantId: the system-assigned identity's
//     ids, captured by callers to grant RBAC role assignments.
//
// All three are derived deterministically from the resource identity, so the
// same resource always reports the same values — across gets, patches and a
// snapshot/restore.
package loadtesting

import (
	"context"
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
	providerNamespace = "Microsoft.LoadTestService"
	// resourceType is the ARM resource type segment.
	resourceType = "loadTests"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// dataPlaneSuffix is the fixed tail of every load-test data-plane hostname;
	// real Azure emits "<guid>.<region>.cnt-prod.loadtesting.azure.com".
	dataPlaneSuffix = "cnt-prod.loadtesting.azure.com"
	// defaultRegion is the fallback data-plane region segment when a resource has
	// no location (never in practice — location is required).
	defaultRegion = "eastus"
)

// EncryptionIdentity selects the managed identity used for a customer-managed
// key. Type is SystemAssigned or UserAssigned; ResourceID is the user-assigned
// identity's ARM id (empty for SystemAssigned).
type EncryptionIdentity struct {
	Type       string `json:"type,omitempty"`
	ResourceID string `json:"resourceId,omitempty"`
}

// Encryption is a customer-managed-key (CMK) configuration.
type Encryption struct {
	KeyURL   string              `json:"keyUrl,omitempty"`
	Identity *EncryptionIdentity `json:"identity,omitempty"`
}

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a load-test resource. Type is one
// of SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned". PrincipalID
// and TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// LoadTest is a stored Microsoft.LoadTestService/loadTests resource.
// Subscription, ResourceGroup and Name preserve the caller's casing; the
// computed fields are minted at create and never regenerated on a read.
type LoadTest struct {
	Subscription      string            `json:"subscription"`
	ResourceGroup     string            `json:"resourceGroup"`
	Name              string            `json:"name"`
	Location          string            `json:"location"`
	Tags              map[string]string `json:"tags,omitempty"`
	Description       string            `json:"description,omitempty"`
	Encryption        *Encryption       `json:"encryption,omitempty"`
	Identity          *Identity         `json:"identity,omitempty"`
	DataPlaneURI      string            `json:"dataPlaneUri"`
	ProvisioningState string            `json:"provisioningState"`
	CreatedAt         string            `json:"createdAt,omitempty"`
}

// ARMID returns the fully-qualified ARM resource id, with the canonical
// provider/type casing real Azure emits.
func (lt *LoadTest) ARMID() string {
	return idgen.AzureID(lt.Subscription, lt.ResourceGroup, providerNamespace, resourceType, lt.Name)
}

// Input carries the mutable fields of a create/update request.
type Input struct {
	Location    string
	Tags        map[string]string
	Description string
	Identity    *Identity
	Encryption  *Encryption
}

// Mock is the in-memory backend for load-test resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*LoadTest]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty load-testing mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*LoadTest](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new load-test resource or updates an existing one.
// The computed fields (dataPlaneURI, provisioningState, identity ids) are
// derived deterministically, so they stay stable across updates. Location is
// immutable in real Azure and is preserved on update. It returns the stored
// resource and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in Input) (LoadTest, bool, error) {
	if err := validate(sub, rg, name, in); err != nil {
		return LoadTest{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	lt := LoadTest{Subscription: sub, ResourceGroup: rg, Name: name, Location: in.Location}
	if existed {
		lt = *existing
	} else {
		lt.DataPlaneURI = dataPlaneURI(sub, rg, name, in.Location)
		lt.ProvisioningState = stateSucceeded
	}

	lt.Tags = maps.Clone(in.Tags)
	lt.Description = in.Description
	lt.Encryption = cloneEncryption(in.Encryption)
	lt.Identity = m.resolveIdentity(in.Identity, sub, rg, name)

	m.store.Set(k, &lt)

	return clone(&lt), created, nil
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (LoadTest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lt, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return LoadTest{}, cerrors.Newf(cerrors.NotFound, "load test %q not found", name)
	}

	return clone(lt), nil
}

// Delete removes the resource, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every resource in the given resource group,
// sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]LoadTest, error) {
	return m.filter(func(lt *LoadTest) bool {
		return strings.EqualFold(lt.Subscription, sub) && strings.EqualFold(lt.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]LoadTest, error) {
	return m.filter(func(lt *LoadTest) bool {
		return strings.EqualFold(lt.Subscription, sub)
	}), nil
}

// DiscoverLoadTests returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverLoadTests(_ context.Context) ([]LoadTest, error) {
	return m.filter(func(*LoadTest) bool { return true }), nil
}

// PurgeResourceGroup deletes every load-test resource under sub/rg, so a
// resource-group delete cascades into its load tests.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, lt := range m.store.All() {
		if strings.EqualFold(lt.Subscription, sub) && strings.EqualFold(lt.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}

	return nil
}

// filter returns the resources matching pred, sorted by name for a stable order.
func (m *Mock) filter(pred func(*LoadTest) bool) []LoadTest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []LoadTest

	for _, lt := range m.store.All() {
		if pred(lt) {
			out = append(out, clone(lt))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// dataPlaneURI mints the stable data-plane hostname for a resource. The leading
// guid is derived from the resource identity, so it is stable across gets and a
// restart; the region segment is the location with spaces removed and lowercased.
func dataPlaneURI(sub, rg, name, location string) string {
	id := strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
	guid := idgen.SyntheticGUID("dataplane/" + id)

	region := strings.ToLower(strings.ReplaceAll(location, " ", ""))
	if region == "" {
		region = defaultRegion
	}

	return guid + "." + region + "." + dataPlaneSuffix
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

// validate rejects a create/update with missing required fields.
func validate(sub, rg, name string, in Input) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "load test name is required")
	case in.Location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(lt *LoadTest) LoadTest {
	out := *lt
	out.Tags = maps.Clone(lt.Tags)
	out.Encryption = cloneEncryption(lt.Encryption)

	if lt.Identity != nil {
		id := *lt.Identity
		id.UserAssigned = maps.Clone(lt.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}

// cloneEncryption deep-copies an encryption config.
func cloneEncryption(e *Encryption) *Encryption {
	if e == nil {
		return nil
	}

	out := &Encryption{KeyURL: e.KeyURL}

	if e.Identity != nil {
		ident := *e.Identity
		out.Identity = &ident
	}

	return out
}
