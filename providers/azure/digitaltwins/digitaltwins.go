// Package digitaltwins provides an in-memory mock of Azure Digital Twins
// (Microsoft.DigitalTwins/digitalTwinsInstances) — the ARM control plane only.
// It manages the instance lifecycle (create/update/get/delete/list); the data
// plane (twin graph, models, DTDL, event routes, queries) is out of scope.
//
// A Digital Twins instance carries a set of computed, service-minted fields that
// MUST stay stable for the lifetime of the resource so infrastructure-as-code
// tools (Terraform's azurerm_digital_twins_instance) see no drift on re-plan:
//   - hostName: "<name>.api.<region>.digitaltwins.azure.net", deterministic from
//     the name and location; Terraform exports it as host_name.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - identity.principalId / identity.tenantId: the system-assigned identity's
//     ids, captured by callers to grant RBAC role assignments.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches and
// a snapshot/restore.
package digitaltwins

import (
	"context"
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
	providerNamespace = "Microsoft.DigitalTwins"
	// resourceType is the ARM resource type segment.
	resourceType = "digitalTwinsInstances"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// hostNameSuffix is the fixed tail of every instance hostname; real Azure
	// emits "<name>.api.<region>.digitaltwins.azure.net".
	hostNameSuffix = "digitaltwins.azure.net"
	// defaultRegion is the fallback hostName region segment when a resource has no
	// location (never in practice — location is required).
	defaultRegion = "eastus"
	// publicNetworkAccessEnabled is the default public-network-access value real
	// Azure reports when the client sends none.
	publicNetworkAccessEnabled = "Enabled"
)

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a Digital Twins instance. Type is
// one of SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned".
// PrincipalID and TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// Instance is a stored Microsoft.DigitalTwins/digitalTwinsInstances resource.
// Subscription, ResourceGroup and Name preserve the caller's casing; the
// computed fields are minted at create and never regenerated on a read.
type Instance struct {
	Subscription        string            `json:"subscription"`
	ResourceGroup       string            `json:"resourceGroup"`
	Name                string            `json:"name"`
	Location            string            `json:"location"`
	Tags                map[string]string `json:"tags,omitempty"`
	Identity            *Identity         `json:"identity,omitempty"`
	PublicNetworkAccess string            `json:"publicNetworkAccess,omitempty"`

	HostName          string `json:"hostName"`
	ProvisioningState string `json:"provisioningState"`
	CreatedTime       string `json:"createdTime,omitempty"`
	LastUpdatedTime   string `json:"lastUpdatedTime,omitempty"`
}

// ARMID returns the fully-qualified ARM resource id, with the canonical
// provider/type casing real Azure emits.
func (i *Instance) ARMID() string {
	return idgen.AzureID(i.Subscription, i.ResourceGroup, providerNamespace, resourceType, i.Name)
}

// Input carries the mutable fields of a create/update request.
type Input struct {
	Location            string
	Tags                map[string]string
	Identity            *Identity
	PublicNetworkAccess string
}

// Mock is the in-memory backend for Digital Twins instances.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*Instance]

	// clock stamps createdTime and lastUpdatedTime; a FakeClock makes those
	// deterministic in tests.
	clock config.Clock

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty Digital Twins mock. The clock stamps the instance
// timestamps; it falls back to the real clock when opts (or its clock) is nil.
func New(opts *config.Options) *Mock {
	clock := config.Clock(config.RealClock{})
	if opts != nil && opts.Clock != nil {
		clock = opts.Clock
	}

	return &Mock{
		store:    memstore.New[*Instance](),
		clock:    clock,
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new Digital Twins instance or updates an existing
// one. The computed fields (hostName, provisioningState, identity ids, createdTime)
// are minted once at create and preserved across updates, so they stay stable.
// Location is immutable in real Azure and is preserved on update. It returns the
// stored resource and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in Input) (Instance, bool, error) {
	if err := validate(sub, rg, name, in); err != nil {
		return Instance{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	now := m.clock.Now().UTC().Format(time.RFC3339Nano)

	inst := Instance{Subscription: sub, ResourceGroup: rg, Name: name, Location: in.Location}
	if existed {
		inst = *existing
	} else {
		inst.HostName = hostName(name, in.Location)
		inst.ProvisioningState = stateSucceeded
		inst.CreatedTime = now
	}

	inst.LastUpdatedTime = now
	inst.Tags = maps.Clone(in.Tags)
	inst.Identity = m.resolveIdentity(in.Identity, sub, rg, name)

	inst.PublicNetworkAccess = in.PublicNetworkAccess
	if inst.PublicNetworkAccess == "" {
		inst.PublicNetworkAccess = publicNetworkAccessEnabled
	}

	m.store.Set(k, &inst)

	return clone(&inst), created, nil
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inst, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return Instance{}, cerrors.Newf(cerrors.NotFound, "digital twins instance %q not found", name)
	}

	return clone(inst), nil
}

// Delete removes the resource, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every resource in the given resource group,
// sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]Instance, error) {
	return m.filter(func(i *Instance) bool {
		return strings.EqualFold(i.Subscription, sub) && strings.EqualFold(i.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]Instance, error) {
	return m.filter(func(i *Instance) bool {
		return strings.EqualFold(i.Subscription, sub)
	}), nil
}

// DiscoverInstances returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverInstances(_ context.Context) ([]Instance, error) {
	return m.filter(func(*Instance) bool { return true }), nil
}

// PurgeResourceGroup deletes every Digital Twins instance under sub/rg, so a
// resource-group delete cascades into its instances.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, inst := range m.store.All() {
		if strings.EqualFold(inst.Subscription, sub) && strings.EqualFold(inst.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}

	return nil
}

// filter returns the resources matching pred, sorted by name for a stable order.
func (m *Mock) filter(pred func(*Instance) bool) []Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Instance

	for _, inst := range m.store.All() {
		if pred(inst) {
			out = append(out, clone(inst))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// hostName mints the stable API endpoint hostname for an instance. The region
// segment is the location with spaces removed and lowercased; the name preserves
// the API's lowercase host convention.
func hostName(name, location string) string {
	region := strings.ToLower(strings.ReplaceAll(location, " ", ""))
	if region == "" {
		region = defaultRegion
	}

	return strings.ToLower(name) + ".api." + region + "." + hostNameSuffix
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
		return cerrors.New(cerrors.InvalidArgument, "digital twins instance name is required")
	case in.Location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(i *Instance) Instance {
	out := *i
	out.Tags = maps.Clone(i.Tags)

	if i.Identity != nil {
		id := *i.Identity
		id.UserAssigned = maps.Clone(i.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}
