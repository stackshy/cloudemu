// Package logic provides an in-memory mock of Azure Logic Apps (Consumption)
// workflows (Microsoft.Logic/workflows), the ARM control plane only. It manages
// the workflow resource lifecycle (create/update/get/delete/list) and the
// enable/disable state toggle, and mints a trigger's callback URL
// (listCallbackUrl). Triggers, runs, versions and executing a workflow are out
// of scope; see docs/coverage/nongoals/logic.md.
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
	"bytes"
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
	// maxNameLen is the longest workflow name Azure accepts.
	maxNameLen = 43
	// jsonNull is the JSON null literal, treated as an absent opaque value.
	jsonNull = "null"
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
	// IntegrationServiceEnvironment and Sku are opaque resource references
	// ({"id":...} / {"name":...,"plan":{...}}) echoed verbatim.
	IntegrationServiceEnvironment json.RawMessage `json:"integrationServiceEnvironment,omitempty"`
	Sku                           json.RawMessage `json:"sku,omitempty"`
	AccessEndpoint                string          `json:"accessEndpoint"`
	ProvisioningState             string          `json:"provisioningState"`
	Revision                      uint64          `json:"revision"`
	CreatedTime                   time.Time       `json:"createdTime"`
	ChangedTime                   time.Time       `json:"changedTime"`
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

	IntegrationServiceEnvironment json.RawMessage
	Sku                           json.RawMessage
}

// Patch carries a PATCH (merge) request. A nil field keeps the stored value; a
// non-nil Tags map (empty included) replaces the tags, and a non-nil Identity
// replaces the identity (Type "None" detaches it). Location is immutable and so
// not patchable.
type Patch struct {
	Tags               map[string]string
	Identity           *Identity
	State              string
	Definition         json.RawMessage
	Parameters         json.RawMessage
	AccessControl      json.RawMessage
	IntegrationAccount json.RawMessage

	IntegrationServiceEnvironment json.RawMessage
	Sku                           json.RawMessage
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
	wf.IntegrationServiceEnvironment = cloneRaw(in.IntegrationServiceEnvironment)
	wf.Sku = cloneRaw(in.Sku)
	wf.Revision++
	wf.ChangedTime = now

	m.store.Set(k, &wf)

	return clone(&wf), !existed, nil
}

// Update merge-patches an existing workflow (PATCH). The lookup, merge and
// store happen under one lock, so a concurrent DELETE cannot be undone by a
// PATCH that read the workflow first, and two concurrent PATCHes both land. A
// missing workflow is a NotFound error: real ARM answers a PATCH on an absent
// resource with 404, it never creates one. Like a PUT, a PATCH moves the
// version and changedTime.
//
//nolint:gocritic // patch mirrors a request-scoped value passed once per call.
func (m *Mock) Update(_ context.Context, sub, rg, name string, patch Patch) (Workflow, error) {
	if err := validatePatch(&patch); err != nil {
		return Workflow{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, ok := m.store.Get(k)
	if !ok {
		return Workflow{}, notFound(rg, name)
	}

	wf := clone(existing)

	if patch.Tags != nil {
		wf.Tags = maps.Clone(patch.Tags)
	}

	if patch.Identity != nil {
		wf.Identity = m.resolveIdentity(patch.Identity, wf.Subscription, wf.ResourceGroup, wf.Name)
	}

	if patch.State != "" {
		wf.State = canonicalState(patch.State)
	}

	mergeRaw(&wf.Definition, patch.Definition)
	mergeRaw(&wf.Parameters, patch.Parameters)
	mergeRaw(&wf.AccessControl, patch.AccessControl)
	mergeRaw(&wf.IntegrationAccount, patch.IntegrationAccount)
	mergeRaw(&wf.IntegrationServiceEnvironment, patch.IntegrationServiceEnvironment)
	mergeRaw(&wf.Sku, patch.Sku)

	wf.Revision++
	wf.ChangedTime = m.clock.Now().UTC()

	m.store.Set(k, &wf)

	return clone(&wf), nil
}

// mergeRaw replaces *dst with a copy of src when src was supplied.
func mergeRaw(dst *json.RawMessage, src json.RawMessage) {
	if len(src) == 0 {
		return
	}

	*dst = cloneRaw(src)
}

// Get returns the workflow, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (Workflow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wf, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return Workflow{}, notFound(rg, name)
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
		return Workflow{}, notFound(rg, name)
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
// sorted by lowercased ARM id.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]Workflow, error) {
	return m.filter(func(wf *Workflow) bool {
		return strings.EqualFold(wf.Subscription, sub) && strings.EqualFold(wf.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every workflow in the subscription, sorted by
// lowercased ARM id.
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

// filter returns the workflows matching pred, sorted by lowercased ARM id.
func (m *Mock) filter(pred func(*Workflow) bool) []Workflow {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Workflow

	for _, wf := range m.store.All() {
		if pred(wf) {
			out = append(out, clone(wf))
		}
	}

	// Sort by the lowercased ARM id: names alone collide across resource
	// groups, and a tie over map iteration order is not stable between calls.
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].ARMID()) < strings.ToLower(out[j].ARMID())
	})

	return out
}

// accessEndpoint mints the stable access endpoint for a workflow. The trailing
// token is derived from the resource identity, so it is stable across gets and
// a restart; the region segment is the location with spaces removed.
func accessEndpoint(sub, rg, name, location string) string {
	id := strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
	token := strings.ReplaceAll(idgen.SyntheticGUID("access/"+id), "-", "")

	return accessEndpointScaleUnit + regionSegment(location) + accessEndpointSuffix + token
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
	case !validName(name):
		return cerrors.Newf(cerrors.InvalidArgument,
			"The workflow name '%s' is invalid. It must be 1-%d characters of alphanumerics, "+
				"hyphens, underscores, periods and parentheses.", name, maxNameLen)
	case in.State != "" && !isWritableState(in.State):
		return cerrors.Newf(cerrors.InvalidArgument, "invalid workflow state %q", in.State)
	default:
		return validateDefinition(in.Definition)
	}
}

// validatePatch rejects a PATCH carrying a state the caller may not set or a
// definition that is not a JSON object.
func validatePatch(p *Patch) error {
	if p.State != "" && !isWritableState(p.State) {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid workflow state %q", p.State)
	}

	return validateDefinition(p.Definition)
}

// validateDefinition rejects a definition that is present but not a JSON
// object: the Workflow Definition Language document is always an object, and
// real Azure answers anything else with 400 InvalidRequestContent. Absent and
// null are allowed (a workflow may be created without a definition).
func validateDefinition(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == jsonNull {
		return nil
	}

	if trimmed[0] != '{' || !json.Valid(trimmed) {
		return ErrInvalidDefinition
	}

	return nil
}

// ErrInvalidDefinition is returned (unwrapped, an InvalidArgument) when a
// workflow definition is not a JSON object, so a wire layer can answer it with
// Azure's InvalidRequestContent code rather than a generic invalid parameter.
var ErrInvalidDefinition = cerrors.New(cerrors.InvalidArgument,
	"The request content is not valid: the workflow definition must be a JSON object.")

// validName applies the Azure naming rule for Microsoft.Logic/workflows:
// 1-43 characters of alphanumerics, hyphens, underscores, periods and
// parentheses (https://learn.microsoft.com/azure/azure-resource-manager/management/resource-name-rules#microsoftlogic).
func validName(name string) bool {
	if name == "" || len(name) > maxNameLen {
		return false
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '(', r == ')':
		default:
			return false
		}
	}

	return true
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

// notFound builds the NotFound error for a missing workflow, worded as ARM's
// ResourceNotFound message.
func notFound(rg, name string) error {
	return cerrors.Newf(cerrors.NotFound,
		"The Resource 'Microsoft.Logic/workflows/%s' under resource group '%s' was not found.", name, rg)
}

// cloneRaw copies an opaque JSON value. A JSON null is treated as absent.
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == jsonNull {
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
	out.IntegrationServiceEnvironment = cloneRaw(wf.IntegrationServiceEnvironment)
	out.Sku = cloneRaw(wf.Sku)

	if wf.Identity != nil {
		id := *wf.Identity
		id.UserAssigned = maps.Clone(wf.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}
