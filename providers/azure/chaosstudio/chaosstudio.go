// Package chaosstudio provides an in-memory mock of Azure Chaos Studio
// (Microsoft.Chaos/experiments) — the ARM control plane only. It manages the
// experiment resource lifecycle (create/update/get/delete/list); no faults are
// ever injected (Chaos Studio's data plane — starting/canceling executions and
// the targets/capabilities resources nested under other resources — is out of
// scope). This package is unrelated to CloudEmu's features/chaos fault-injection
// engine; it emulates the Azure Chaos Studio ARM product surface.
//
// The resource carries a set of computed, service-minted fields that MUST stay
// stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_chaos_studio_experiment) see no drift on re-plan:
//   - provisioningState: "Succeeded" once provisioning completes.
//   - identity.principalId / identity.tenantId for a system-assigned identity.
//
// The rich selectors and steps configuration is carried verbatim as raw JSON, so
// the deeply-nested branch/action blocks round-trip byte-for-byte and cannot
// drift. Every computed field is derived deterministically from the resource
// identity, so the same resource always reports the same values — across gets,
// updates and a snapshot/restore.
package chaosstudio

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
	providerNamespace = "Microsoft.Chaos"
	// resourceType is the ARM resource type segment.
	resourceType = "experiments"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
)

// emptyArray is the default value for the required selectors/steps arrays when a
// create omits them, so the wire response always carries a valid JSON array.
func emptyArray() json.RawMessage { return json.RawMessage("[]") }

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to an experiment. Type is one of
// SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned". PrincipalID and
// TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// Experiment is a stored Microsoft.Chaos/experiments resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read. Selectors and Steps are held
// verbatim as raw JSON so the nested blocks round-trip exactly.
type Experiment struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`

	// Writable properties, carried verbatim as raw JSON arrays.
	Selectors json.RawMessage `json:"selectors,omitempty"`
	Steps     json.RawMessage `json:"steps,omitempty"`

	// Computed, stable field.
	ProvisioningState string `json:"provisioningState"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *Experiment) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// Input carries the mutable fields of a create/update request. A nil pointer or
// nil raw message means "not supplied" (preserve existing), so a PATCH overlays
// only what it names.
type Input struct {
	Tags      map[string]string
	Identity  *Identity
	Selectors json.RawMessage
	Steps     json.RawMessage
}

// Mock is the in-memory backend for experiment resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*Experiment]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty experiment mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*Experiment](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new experiment or updates an existing one. The
// computed fields (identity ids) are minted deterministically so they stay
// stable across updates. Location is immutable and preserved on update. It
// returns the stored resource and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name, location string, in *Input) (Experiment, bool, error) {
	if err := validate(sub, rg, name); err != nil {
		return Experiment{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	var s Experiment
	if existed {
		s = *existing
	} else {
		s = newExperiment(sub, rg, name, location)
	}

	applyInput(&s, in)

	// Identity is re-resolved only when the request supplies one; a PATCH that
	// omits identity preserves the stored value (an explicit "None" clears it).
	if in.Identity != nil {
		s.Identity = m.resolveIdentity(in.Identity, sub, rg, name)
	}

	s.ProvisioningState = stateSucceeded

	m.store.Set(k, &s)

	return clone(&s), created, nil
}

// newExperiment seeds a fresh resource with its immutable identity, location and
// empty (but valid) required arrays.
func newExperiment(sub, rg, name, location string) Experiment {
	return Experiment{
		Subscription:      sub,
		ResourceGroup:     rg,
		Name:              name,
		Location:          location,
		Selectors:         emptyArray(),
		Steps:             emptyArray(),
		ProvisioningState: stateSucceeded,
	}
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (Experiment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return Experiment{}, cerrors.Newf(cerrors.NotFound, "chaos experiment %q not found", name)
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
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]Experiment, error) {
	return m.filter(func(s *Experiment) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]Experiment, error) {
	return m.filter(func(s *Experiment) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverExperiments returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverExperiments(_ context.Context) ([]Experiment, error) {
	return m.filter(func(*Experiment) bool { return true }), nil
}

// PurgeResourceGroup deletes every experiment under sub/rg, so a resource-group
// delete cascades into its experiments.
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
func (m *Mock) filter(pred func(*Experiment) bool) []Experiment {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Experiment

	for _, s := range m.store.All() {
		if pred(s) {
			out = append(out, clone(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyInput overlays the mutable request fields onto s, leaving the computed
// field and the immutable location untouched. A nil pointer / nil raw message
// means "not supplied": the stored value is preserved.
func applyInput(s *Experiment, in *Input) {
	if in.Tags != nil {
		s.Tags = maps.Clone(in.Tags)
	}

	if in.Selectors != nil {
		s.Selectors = cloneRaw(in.Selectors)
	}

	if in.Steps != nil {
		s.Steps = cloneRaw(in.Steps)
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
		return cerrors.New(cerrors.InvalidArgument, "experiment name is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *Experiment) Experiment {
	out := *s
	out.Tags = maps.Clone(s.Tags)
	out.Selectors = cloneRaw(s.Selectors)
	out.Steps = cloneRaw(s.Steps)

	if s.Identity != nil {
		id := *s.Identity
		id.UserAssigned = maps.Clone(s.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}

// cloneRaw copies a raw JSON message so a stored resource never aliases the
// caller's byte slice. A nil input clones to nil.
func cloneRaw(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}

	return append(json.RawMessage(nil), in...)
}
