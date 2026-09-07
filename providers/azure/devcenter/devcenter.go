// Package devcenter provides an in-memory mock of Azure Dev Center
// (Microsoft.DevCenter/devcenters) — the ARM control plane only. It manages the
// dev center resource lifecycle (create/update/get/delete/list); the data plane
// (the devcenter.azure.com developer API: projects, pools, catalogs, dev boxes,
// environments) is out of scope.
//
// The resource carries a set of computed, service-minted fields that MUST stay
// stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_dev_center) see no drift on re-plan:
//   - devCenterUri: "https://<guid>-<name>.<region>.devcenter.azure.com",
//     deterministic from the name and region.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - identity.principalId / identity.tenantId for a system-assigned identity.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches and
// a snapshot/restore.
package devcenter

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
	providerNamespace = "Microsoft.DevCenter"
	// resourceType is the ARM resource type segment.
	resourceType = "devcenters"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// uriSuffix is the fixed tail of every dev center URI host; real Azure emits
	// "https://<guid>-<name>.<region>.devcenter.azure.com".
	uriSuffix = "devcenter.azure.com"
	// enabled / disabled are the ARM enum values for the catalog-item-sync toggle.
	enabled  = "Enabled"
	disabled = "Disabled"
)

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a dev center. Type is one of
// SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned". PrincipalID and
// TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// DevCenter is a stored Microsoft.DevCenter/devcenters resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read.
type DevCenter struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`

	// Writable properties.
	DisplayName                 string `json:"displayName,omitempty"`
	CatalogItemSyncEnableStatus string `json:"catalogItemSyncEnableStatus"`

	// Computed, stable fields.
	ProvisioningState string `json:"provisioningState"`
	DevCenterURI      string `json:"devCenterUri"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *DevCenter) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// Input carries the mutable fields of a create/update request. The pointer
// fields distinguish "not supplied" (nil, preserve existing) from an explicit
// value, so a PATCH overlays only what it names.
type Input struct {
	Tags                        map[string]string
	Identity                    *Identity
	DisplayName                 *string
	CatalogItemSyncEnableStatus *string
}

// Mock is the in-memory backend for dev center resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*DevCenter]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty dev center mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*DevCenter](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new dev center or updates an existing one. The
// computed fields (devCenterUri, identity ids) are minted deterministically so
// they stay stable across updates. Location is immutable and preserved on
// update. It returns the stored resource and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name, location string, in *Input) (DevCenter, bool, error) {
	if err := validate(sub, rg, name); err != nil {
		return DevCenter{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	var s DevCenter
	if existed {
		s = *existing
	} else {
		s = newDevCenter(sub, rg, name, location)
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

// newDevCenter seeds a fresh resource with its immutable identity, location and
// the ARM default toggle value.
func newDevCenter(sub, rg, name, location string) DevCenter {
	return DevCenter{
		Subscription:                sub,
		ResourceGroup:               rg,
		Name:                        name,
		Location:                    location,
		CatalogItemSyncEnableStatus: disabled,
		ProvisioningState:           stateSucceeded,
	}
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (DevCenter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return DevCenter{}, cerrors.Newf(cerrors.NotFound, "dev center %q not found", name)
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
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]DevCenter, error) {
	return m.filter(func(s *DevCenter) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]DevCenter, error) {
	return m.filter(func(s *DevCenter) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverDevCenters returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverDevCenters(_ context.Context) ([]DevCenter, error) {
	return m.filter(func(*DevCenter) bool { return true }), nil
}

// PurgeResourceGroup deletes every dev center under sub/rg, so a resource-group
// delete cascades into its dev centers.
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
func (m *Mock) filter(pred func(*DevCenter) bool) []DevCenter {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []DevCenter

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
func applyInput(s *DevCenter, in *Input) {
	if in.Tags != nil {
		s.Tags = maps.Clone(in.Tags)
	}

	if in.DisplayName != nil {
		s.DisplayName = *in.DisplayName
	}

	if in.CatalogItemSyncEnableStatus != nil && *in.CatalogItemSyncEnableStatus != "" {
		s.CatalogItemSyncEnableStatus = normalizeEnum(*in.CatalogItemSyncEnableStatus)
	}
}

// mintComputed fills the stable, service-minted fields. Every value is derived
// deterministically from the stored identity, so it never changes on a read.
func mintComputed(s *DevCenter) {
	s.ProvisioningState = stateSucceeded
	s.DevCenterURI = uriFor(s.Name, s.Location)
}

// uriFor builds the deterministic dev center URI, matching the real
// "https://<guid>-<name>.<region>.devcenter.azure.com" shape.
func uriFor(name, location string) string {
	guid := idgen.SyntheticGUID("devcenter-uri/" + strings.ToLower(name))

	return fmt.Sprintf("https://%s-%s.%s.%s", guid, strings.ToLower(name), regionCode(location), uriSuffix)
}

// regionCode normalizes an ARM location into the host label real Azure uses in
// the URI (lowercased, whitespace removed). The exact value only has to be
// stable — Terraform reads the URI back verbatim.
func regionCode(location string) string {
	if location == "" {
		return "global"
	}

	return strings.ToLower(strings.ReplaceAll(location, " ", ""))
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
		return cerrors.New(cerrors.InvalidArgument, "dev center name is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *DevCenter) DevCenter {
	out := *s
	out.Tags = maps.Clone(s.Tags)

	if s.Identity != nil {
		id := *s.Identity
		id.UserAssigned = maps.Clone(s.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}
