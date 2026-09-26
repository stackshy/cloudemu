// Package logic provides an in-memory mock of Azure Logic Apps (Consumption)
// workflows (Microsoft.Logic/workflows), the ARM control plane only. It manages
// the workflow resource lifecycle (create/update/get/delete/list) and the
// enable/disable state toggle; running a workflow (triggers, runs, actions,
// callback URLs) is data plane and out of scope.
//
// The workflow definition, parameters, access-control and integration-account
// blocks are stored as opaque JSON and echoed verbatim: the emulator never
// interprets the Workflow Definition Language.
//
// A workflow carries service-minted fields that stay stable for the lifetime of
// the resource so infrastructure-as-code tools see no drift on re-plan:
//   - accessEndpoint: derived deterministically from the resource identity.
//   - provisioningState: "Succeeded" once the synchronous PUT completes.
//   - createdTime: stamped once at create.
//   - identity.principalId / identity.tenantId for a system-assigned identity.
//
// changedTime moves on every mutation (PUT, PATCH, enable, disable) and version
// moves on every PUT or PATCH, mirroring how the real service reports edits.
package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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
	providerNamespace = "Microsoft.Logic"
	// resourceType is the ARM resource type segment.
	resourceType = "workflows"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// accessEndpointSuffix is the fixed tail of every Consumption workflow access
	// endpoint host; real Azure emits
	// "https://prod-NN.<region>.logic.azure.com:443/workflows/<32 hex>".
	accessEndpointSuffix = ".logic.azure.com:443/workflows/"
	// accessEndpointScaleUnit is the scale-unit prefix of the access endpoint host.
	accessEndpointScaleUnit = "https://prod-00."
	// defaultRegion is the fallback access-endpoint region segment when a
	// workflow has no location (never in practice: location is required).
	defaultRegion = "eastus"
	// versionWidth is the zero-padded digit count of a workflow version string;
	// real versions are 20-digit numeric tokens.
	versionWidth = 20
)

// Workflow states a caller may set. Real Azure additionally reports Completed,
// Deleted and NotSpecified, which are service-owned and rejected on write.
const (
	StateEnabled   = "Enabled"
	StateDisabled  = "Disabled"
	StateSuspended = "Suspended"
)

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a workflow. Type is one of
// SystemAssigned, UserAssigned or "SystemAssigned, UserAssigned". PrincipalID
// and TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// Workflow is a stored Microsoft.Logic/workflows resource. Subscription,
// ResourceGroup and Name preserve the caller's casing. Definition, Parameters,
// AccessControl and IntegrationAccount are opaque JSON echoed verbatim.
type Workflow struct {
	Subscription       string            `json:"subscription"`
	ResourceGroup      string            `json:"resourceGroup"`
	Name               string            `json:"name"`
	Location           string            `json:"location"`
	Tags               map[string]string `json:"tags,omitempty"`
	Identity           *Identity         `json:"identity,omitempty"`
	State              string            `json:"state"`
	Definition         json.RawMessage   `json:"definition,omitempty"`
	Parameters         json.RawMessage   `json:"parameters,omitempty"`
	AccessControl      json.RawMessage   `json:"accessControl,omitempty"`
	IntegrationAccount json.RawMessage   `json:"integrationAccount,omitempty"`
	AccessEndpoint     string            `json:"accessEndpoint"`
	ProvisioningState  string            `json:"provisioningState"`
	Revision           uint64            `json:"revision"`
	CreatedTime        time.Time         `json:"createdTime"`
	ChangedTime        time.Time         `json:"changedTime"`
}

// ARMID returns the fully-qualified ARM resource id, with the canonical
// provider/type casing real Azure emits.
func (wf *Workflow) ARMID() string {
	return idgen.AzureID(wf.Subscription, wf.ResourceGroup, providerNamespace, resourceType, wf.Name)
}

// Version returns the workflow's version token: a zero-padded, monotonically
// increasing number that moves on every PUT or PATCH.
func (wf *Workflow) Version() string {
	return fmt.Sprintf("%0*d", versionWidth, wf.Revision)
}

// Input carries the mutable fields of a create/update request. An empty State
// defaults to Enabled on create and keeps the stored state on update.
type Input struct {
	Location           string
	Tags               map[string]string
	Identity           *Identity
	State              string
	Definition         json.RawMessage
	Parameters         json.RawMessage
	AccessControl      json.RawMessage
	IntegrationAccount json.RawMessage
}

// Mock is the in-memory backend for Logic Apps workflows.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*Workflow]
	clock config.Clock

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty Logic Apps mock.
func New(opts *config.Options) *Mock {
	var clock config.Clock = config.RealClock{}
	if opts != nil && opts.Clock != nil {
		clock = opts.Clock
	}

	return &Mock{
		store:    memstore.New[*Workflow](),
		clock:    clock,
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a workflow.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new workflow or replaces an existing one. The
// computed fields (accessEndpoint, provisioningState, createdTime) are minted at
// create and preserved on update; location is immutable and kept on update. It
// returns the stored workflow and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in *Input) (Workflow, bool, error) {
	if err := validate(sub, rg, name, in); err != nil {
		return Workflow{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)
	now := m.clock.Now().UTC()

	existing, existed := m.store.Get(k)

	wf := Workflow{
		Subscription:      sub,
		ResourceGroup:     rg,
		Name:              name,
		Location:          in.Location,
		State:             StateEnabled,
		AccessEndpoint:    accessEndpoint(sub, rg, name, in.Location),
		ProvisioningState: stateSucceeded,
		CreatedTime:       now,
	}
	if existed {
		wf = clone(existing)
	}

	if in.State != "" {
		wf.State = canonicalState(in.State)
	}

	wf.Tags = maps.Clone(in.Tags)
	wf.Identity = m.resolveIdentity(in.Identity, sub, rg, name)
	wf.Definition = cloneRaw(in.Definition)
	wf.Parameters = cloneRaw(in.Parameters)
	wf.AccessControl = cloneRaw(in.AccessControl)
	wf.IntegrationAccount = cloneRaw(in.IntegrationAccount)
	wf.Revision++
	wf.ChangedTime = now

	m.store.Set(k, &wf)

	return clone(&wf), !existed, nil
}

// Get returns the workflow, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (Workflow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wf, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return Workflow{}, notFound(name)
	}

	return clone(wf), nil
}

// Enable sets a workflow's state to Enabled (POST .../enable). A missing
// workflow is a NotFound error.
func (m *Mock) Enable(_ context.Context, sub, rg, name string) (Workflow, error) {
	return m.setState(sub, rg, name, StateEnabled)
}

// Disable sets a workflow's state to Disabled (POST .../disable). A missing
// workflow is a NotFound error.
func (m *Mock) Disable(_ context.Context, sub, rg, name string) (Workflow, error) {
	return m.setState(sub, rg, name, StateDisabled)
}

// setState switches a workflow's state and moves its changedTime. The version
// is unchanged: the definition was not edited.
func (m *Mock) setState(sub, rg, name, state string) (Workflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	wf, ok := m.store.Get(k)
	if !ok {
		return Workflow{}, notFound(name)
	}

	updated := clone(wf)
	updated.State = canonicalState(state)
	updated.ChangedTime = m.clock.Now().UTC()

	m.store.Set(k, &updated)

	return clone(&updated), nil
}

// Delete removes the workflow, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every workflow in the given resource group,
// sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]Workflow, error) {
	return m.filter(func(wf *Workflow) bool {
		return strings.EqualFold(wf.Subscription, sub) && strings.EqualFold(wf.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every workflow in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]Workflow, error) {
	return m.filter(func(wf *Workflow) bool {
		return strings.EqualFold(wf.Subscription, sub)
	}), nil
}

// DiscoverWorkflows returns every stored workflow, for the inventory walk.
func (m *Mock) DiscoverWorkflows(_ context.Context) ([]Workflow, error) {
	return m.filter(func(*Workflow) bool { return true }), nil
}

// PurgeResourceGroup deletes every workflow under sub/rg, so a resource-group
// delete cascades into its workflows.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, wf := range m.store.All() {
		if strings.EqualFold(wf.Subscription, sub) && strings.EqualFold(wf.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}

	return nil
}

// filter returns the workflows matching pred, sorted by name for a stable order.
func (m *Mock) filter(pred func(*Workflow) bool) []Workflow {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Workflow

	for _, wf := range m.store.All() {
		if pred(wf) {
			out = append(out, clone(wf))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// accessEndpoint mints the stable access endpoint for a workflow. The trailing
// token is derived from the resource identity, so it is stable across gets and
// a restart; the region segment is the location with spaces removed.
func accessEndpoint(sub, rg, name, location string) string {
	id := strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
	token := strings.ReplaceAll(idgen.SyntheticGUID("access/"+id), "-", "")

	region := strings.ToLower(strings.ReplaceAll(location, " ", ""))
	if region == "" {
		region = defaultRegion
	}

	return accessEndpointScaleUnit + region + accessEndpointSuffix + token
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

// validate rejects a create/update with missing required fields or a state the
// caller may not set.
func validate(sub, rg, name string, in *Input) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "workflow name is required")
	case in.Location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	case in.State != "" && !isWritableState(in.State):
		return cerrors.Newf(cerrors.InvalidArgument, "invalid workflow state %q", in.State)
	default:
		return nil
	}
}

// isWritableState reports whether state (case-insensitive) may be set by a caller.
func isWritableState(state string) bool {
	return canonicalState(state) != ""
}

// canonicalState returns the canonical casing of a writable state, or "" when
// state is not one a caller may set.
func canonicalState(state string) string {
	for _, s := range []string{StateEnabled, StateDisabled, StateSuspended} {
		if strings.EqualFold(s, state) {
			return s
		}
	}

	return ""
}

// notFound builds the NotFound error for a missing workflow.
func notFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "The Resource 'Microsoft.Logic/workflows/%s' was not found.", name)
}

// cloneRaw copies an opaque JSON value. A JSON null is treated as absent.
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	return slices.Clone(raw)
}

// clone deep-copies a stored workflow so callers never alias the backing store.
func clone(wf *Workflow) Workflow {
	out := *wf
	out.Tags = maps.Clone(wf.Tags)
	out.Definition = cloneRaw(wf.Definition)
	out.Parameters = cloneRaw(wf.Parameters)
	out.AccessControl = cloneRaw(wf.AccessControl)
	out.IntegrationAccount = cloneRaw(wf.IntegrationAccount)

	if wf.Identity != nil {
		id := *wf.Identity
		id.UserAssigned = maps.Clone(wf.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}
