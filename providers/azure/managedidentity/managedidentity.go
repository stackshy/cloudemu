// Package managedidentity provides an in-memory mock of Azure User-Assigned
// Managed Identities (Microsoft.ManagedIdentity/userAssignedIdentities).
//
// A user-assigned identity is a standalone Azure resource that carries three
// stable, service-minted identifiers — clientId, principalId and tenantId. The
// principalId in particular is captured by callers to grant the identity RBAC
// role assignments, so it MUST stay stable for the lifetime of the identity: it
// is minted once at create time, persisted, and never regenerated on a read.
package managedidentity

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

// providerNamespace is the ARM provider namespace, used when composing a
// resource's fully-qualified id.
const providerNamespace = "Microsoft.ManagedIdentity"

// resourceType is the ARM resource type segment.
const resourceType = "userAssignedIdentities"

// Identity is a stored user-assigned managed identity. Subscription, Resource
// Group and Name preserve the caller's original casing for the response; the
// three ids are minted once at create time and never change.
type Identity struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	ClientID      string            `json:"clientId"`
	PrincipalID   string            `json:"principalId"`
	TenantID      string            `json:"tenantId"`
}

// ARMID returns the fully-qualified ARM resource id for the identity, with the
// canonical provider/type casing real Azure emits.
func (id *Identity) ARMID() string {
	return "/subscriptions/" + id.Subscription +
		"/resourceGroups/" + id.ResourceGroup +
		"/providers/" + providerNamespace +
		"/" + resourceType +
		"/" + id.Name
}

// Input carries the mutable fields of a create/update request.
type Input struct {
	Location string
	Tags     map[string]string
}

// Mock is the in-memory backend for user-assigned managed identities.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[Identity]

	// tenantID is the single AAD tenant this estate belongs to. Every identity
	// reports it, matching real Azure where all identities in a subscription
	// share one tenant. Minted once and persisted so it survives a restart.
	tenantID string
}

// New creates an empty managed-identity mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[Identity](),
		tenantID: idgen.UUID(),
	}
}

// key is the case-insensitive store key for an identity.
func key(sub, rg, name string) string {
	return strings.ToLower("/subscriptions/" + sub +
		"/resourceGroups/" + rg +
		"/providers/" + providerNamespace +
		"/" + resourceType +
		"/" + name)
}

// CreateOrUpdate creates a new identity or updates an existing one. The three
// ids are minted only on first create and preserved across updates; only
// Location and Tags are mutable. It returns the stored identity and whether it
// was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in Input) (Identity, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	id, existed := m.store.Get(k)
	created := !existed

	if created {
		id = Identity{
			Subscription: sub, ResourceGroup: rg, Name: name,
			ClientID:    idgen.UUID(),
			PrincipalID: idgen.UUID(),
			TenantID:    m.tenantID,
		}
	}

	id.Location = in.Location
	id.Tags = maps.Clone(in.Tags)

	m.store.Set(k, id)

	return id, created, nil
}

// Get returns the identity, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (Identity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return Identity{}, cerrors.Newf(cerrors.NotFound, "managed identity %q not found", name)
	}

	return id, nil
}

// Delete removes the identity, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every identity in the given resource group,
// sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]Identity, error) {
	return m.filter(func(id Identity) bool {
		return strings.EqualFold(id.Subscription, sub) && strings.EqualFold(id.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every identity in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]Identity, error) {
	return m.filter(func(id Identity) bool {
		return strings.EqualFold(id.Subscription, sub)
	}), nil
}

// DiscoverIdentities returns every stored identity, for the inventory walk.
func (m *Mock) DiscoverIdentities(_ context.Context) ([]Identity, error) {
	return m.filter(func(Identity) bool { return true }), nil
}

// PurgeResourceGroup deletes every identity under sub/rg, so a resource-group
// delete cascades into its identities.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, id := range m.store.All() {
		if strings.EqualFold(id.Subscription, sub) && strings.EqualFold(id.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}

	return nil
}

// filter returns the identities matching pred, sorted by name for a stable
// listing order.
func (m *Mock) filter(pred func(Identity) bool) []Identity {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Identity

	for _, id := range m.store.All() {
		if pred(id) {
			out = append(out, id)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}
