// Package communication provides an in-memory mock of Azure Communication
// Services (Microsoft.Communication/communicationServices) — the ARM control
// plane only. It manages the communicationServices resource lifecycle
// (create/update/get/delete/list) and the listKeys / regenerateKey actions; the
// data plane (sending SMS/email/chat, calling, phone numbers) is out of scope.
//
// A Communication Services resource is GLOBAL: its ARM location is always
// "global", not an Azure region. Where the data is stored at rest is instead
// carried by properties.dataLocation (e.g. "United States"), which is REQUIRED
// at create and IMMUTABLE thereafter.
//
// The resource carries a set of computed, service-minted fields that MUST stay
// stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_communication_service) see no drift on re-plan:
//   - hostName: "<name>.communication.azure.com", deterministic from the name.
//   - immutableResourceId: a stable GUID minted once at create.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - primaryKey / secondaryKey and their connection strings.
//   - identity.principalId / identity.tenantId for a system-assigned identity.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches,
// listKeys and a snapshot/restore.
package communication

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"maps"
	"slices"
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
	providerNamespace = "Microsoft.Communication"
	// resourceType is the ARM resource type segment.
	resourceType = "communicationServices"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// hostNameSuffix is the fixed tail of every communicationServices hostname;
	// real Azure emits "<name>.communication.azure.com" (the same host the
	// connection string endpoint embeds).
	hostNameSuffix = "communication.azure.com"
	// locationGlobal is the only ARM location a Communication Services resource
	// ever has: this resource type is global, not regional.
	locationGlobal = "global"
	// defaultVersion is the reported Communication Services resource version.
	defaultVersion = "1.0.0"
)

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a communicationServices resource.
// Type is one of SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned".
// PrincipalID and TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// Communication is a stored Microsoft.Communication/communicationServices
// resource. Subscription, ResourceGroup and Name preserve the caller's casing;
// the computed fields are minted at create and never regenerated on a read.
type Communication struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`

	DataLocation      string   `json:"dataLocation"`
	NotificationHubID string   `json:"notificationHubId,omitempty"`
	LinkedDomains     []string `json:"linkedDomains,omitempty"`

	HostName            string `json:"hostName"`
	ImmutableResourceID string `json:"immutableResourceId"`
	Version             string `json:"version"`
	ProvisioningState   string `json:"provisioningState"`
	PrimaryKey          string `json:"primaryKey"`
	SecondaryKey        string `json:"secondaryKey"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *Communication) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// PrimaryConnectionString is the connection string built from the primary key.
func (s *Communication) PrimaryConnectionString() string {
	return connectionString(s.HostName, s.PrimaryKey)
}

// SecondaryConnectionString is the connection string built from the secondary key.
func (s *Communication) SecondaryConnectionString() string {
	return connectionString(s.HostName, s.SecondaryKey)
}

// Input carries the mutable fields of a create/update request. DataLocation is
// consumed only at create (it is immutable) and ignored on update.
type Input struct {
	Tags              map[string]string
	Identity          *Identity
	DataLocation      string
	NotificationHubID string
	LinkedDomains     []string
}

// Mock is the in-memory backend for communicationServices resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*Communication]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty communicationServices mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*Communication](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new communicationServices resource or updates an
// existing one. The computed fields (hostName, immutableResourceId, keys,
// identity ids) are minted once at create and preserved across updates, so they
// stay stable. Location is always "global" and dataLocation is immutable: both
// are preserved on update. It returns the stored resource and whether it was
// newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in *Input) (Communication, bool, error) {
	if err := validate(sub, rg, name, in); err != nil {
		return Communication{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	s := Communication{Subscription: sub, ResourceGroup: rg, Name: name}
	if existed {
		s = *existing
	} else {
		// dataLocation is immutable: it is captured once, at create.
		s.DataLocation = in.DataLocation
		mintComputed(&s, sub, rg, name)
	}

	// The resource is global and dataLocation never changes after create.
	s.Location = locationGlobal

	applyInput(&s, in)
	s.Identity = m.resolveIdentity(in.Identity, sub, rg, name)

	m.store.Set(k, &s)

	return clone(&s), created, nil
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (Communication, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return Communication{}, cerrors.Newf(cerrors.NotFound, "communicationServices %q not found", name)
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
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]Communication, error) {
	return m.filter(func(s *Communication) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]Communication, error) {
	return m.filter(func(s *Communication) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverCommunication returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverCommunication(_ context.Context) ([]Communication, error) {
	return m.filter(func(*Communication) bool { return true }), nil
}

// PurgeResourceGroup deletes every communicationServices resource under sub/rg,
// so a resource-group delete cascades into its communicationServices resources.
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
func (m *Mock) filter(pred func(*Communication) bool) []Communication {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Communication

	for _, s := range m.store.All() {
		if pred(s) {
			out = append(out, clone(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyInput overlays the mutable request fields onto s, leaving the computed
// fields and the immutable dataLocation untouched.
func applyInput(s *Communication, in *Input) {
	s.Tags = maps.Clone(in.Tags)
	s.NotificationHubID = in.NotificationHubID
	s.LinkedDomains = slices.Clone(in.LinkedDomains)
}

// mintComputed fills the stable, service-minted fields once, at create.
func mintComputed(s *Communication, sub, rg, name string) {
	id := strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
	s.HostName = strings.ToLower(name) + "." + hostNameSuffix
	s.ImmutableResourceID = idgen.SyntheticGUID("immutable/" + id)
	s.Version = defaultVersion
	s.ProvisioningState = stateSucceeded
	s.PrimaryKey = mintKey("primary/" + id)
	s.SecondaryKey = mintKey("secondary/" + id)
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

// connectionString builds the Communication Services connection string for a
// host and key, matching the real "endpoint=https://<host>/;accesskey=<key>"
// shape.
func connectionString(host, accessKey string) string {
	return "endpoint=https://" + host + "/;accesskey=" + accessKey
}

// mintKey derives a stable, base64-encoded 32-byte access key from a seed, the
// shape a real Communication Services access key takes.
func mintKey(seed string) string {
	h := strings.ReplaceAll(idgen.SyntheticGUID(seed), "-", "") +
		strings.ReplaceAll(idgen.SyntheticGUID(seed+"#2"), "-", "")

	raw, err := hex.DecodeString(h)
	if err != nil {
		return base64.StdEncoding.EncodeToString([]byte(h))
	}

	return base64.StdEncoding.EncodeToString(raw)
}

// validate rejects a create/update with missing required fields. dataLocation is
// required: on create the client sends it; on update the merge carries the
// immutable stored value forward.
func validate(sub, rg, name string, in *Input) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "communicationServices name is required")
	case in.DataLocation == "":
		return cerrors.New(cerrors.InvalidArgument, "dataLocation is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *Communication) Communication {
	out := *s
	out.Tags = maps.Clone(s.Tags)
	out.LinkedDomains = slices.Clone(s.LinkedDomains)

	if s.Identity != nil {
		id := *s.Identity
		id.UserAssigned = maps.Clone(s.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}
