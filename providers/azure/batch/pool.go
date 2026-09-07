package batch

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// Pool is a stored Microsoft.Batch/batchAccounts/pools child resource. It is
// keyed under its parent account; deleting the account cascades to it.
type Pool struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	AccountName   string            `json:"accountName"`
	Name          string            `json:"name"`
	Tags          map[string]string `json:"tags,omitempty"`

	VMSize      string `json:"vmSize"`
	DisplayName string `json:"displayName,omitempty"`
	// DeploymentConfiguration round-trips verbatim (imageReference /
	// nodeAgentSkuId), so the wire body a caller sends is the one it reads back.
	DeploymentConfiguration json.RawMessage `json:"deploymentConfiguration,omitempty"`

	TargetDedicatedNodes    int    `json:"targetDedicatedNodes"`
	TargetLowPriorityNodes  int    `json:"targetLowPriorityNodes"`
	CurrentDedicatedNodes   int    `json:"currentDedicatedNodes"`
	CurrentLowPriorityNodes int    `json:"currentLowPriorityNodes"`
	ResizeTimeout           string `json:"resizeTimeout,omitempty"`

	// Computed, stable fields.
	AllocationState   string `json:"allocationState"`
	ProvisioningState string `json:"provisioningState"`
}

// ARMID returns the fully-qualified ARM resource id of the pool, nested under its
// parent account.
func (p *Pool) ARMID() string {
	return idgen.AzureID(p.Subscription, p.ResourceGroup, providerNamespace, accountType, p.AccountName) +
		"/" + poolType + "/" + p.Name
}

// PoolInput carries the mutable fields of a pool create/update request. Pointer
// fields distinguish "not supplied" (nil, preserve existing) from an explicit
// value, so a PATCH overlays only what it names.
type PoolInput struct {
	Tags                    map[string]string
	VMSize                  *string
	DisplayName             *string
	DeploymentConfiguration json.RawMessage
	TargetDedicatedNodes    *int
	TargetLowPriorityNodes  *int
	ResizeTimeout           *string
}

// poolKey is the case-insensitive store key for a pool under an account.
func poolKey(sub, rg, account, name string) string {
	return accountKey(sub, rg, account) + "/" + poolType + "/" + strings.ToLower(name)
}

// CreateOrUpdatePool creates a new pool or updates an existing one under its
// parent account. The parent account must exist — otherwise it returns a
// NotFound error (the wire layer maps it to ParentResourceNotFound). On create
// the pool settles to Steady with current == target node counts. It returns the
// stored pool and whether it was newly created.
func (m *Mock) CreateOrUpdatePool(
	_ context.Context, sub, rg, account, name string, in *PoolInput,
) (Pool, bool, error) {
	if err := validatePool(sub, rg, account, name); err != nil {
		return Pool{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.accounts.Has(accountKey(sub, rg, account)) {
		return Pool{}, false, cerrors.Newf(cerrors.NotFound, "batch account %q not found", account)
	}

	k := poolKey(sub, rg, account, name)

	existing, existed := m.pools.Get(k)
	created := !existed

	var p Pool
	if existed {
		p = *existing
	} else {
		p = newPool(sub, rg, account, name)
	}

	applyPoolInput(&p, in)

	// On create (or an update that renames the fixed-scale targets) the emulator
	// settles nodes immediately: current tracks target and allocationState stays
	// Steady. A resize/stopResize action is the only path that parks the pool in
	// the Resizing state.
	if created {
		p.CurrentDedicatedNodes = p.TargetDedicatedNodes
		p.CurrentLowPriorityNodes = p.TargetLowPriorityNodes
	}

	m.pools.Set(k, &p)

	return clonePool(&p), created, nil
}

// newPool seeds a fresh pool with its immutable identity, its ARM defaults and
// its computed, stable fields.
func newPool(sub, rg, account, name string) Pool {
	return Pool{
		Subscription:      sub,
		ResourceGroup:     rg,
		AccountName:       account,
		Name:              name,
		ResizeTimeout:     defaultResizeTimeout,
		AllocationState:   allocationSteady,
		ProvisioningState: stateSucceeded,
	}
}

// GetPool returns the pool, or a NotFound error.
func (m *Mock) GetPool(_ context.Context, sub, rg, account, name string) (Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.pools.Get(poolKey(sub, rg, account, name))
	if !ok {
		return Pool{}, cerrors.Newf(cerrors.NotFound, "batch pool %q not found", name)
	}

	return clonePool(p), nil
}

// DeletePool removes the pool, reporting whether it existed.
func (m *Mock) DeletePool(_ context.Context, sub, rg, account, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.pools.Delete(poolKey(sub, rg, account, name)), nil
}

// ListPoolsByAccount returns every pool under the account, sorted by name.
func (m *Mock) ListPoolsByAccount(_ context.Context, sub, rg, account string) ([]Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := accountKey(sub, rg, account) + "/" + poolType + "/"

	var out []Pool

	for k, p := range m.pools.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, clonePool(p))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// Resize begins a resize on the pool: it moves allocationState Steady->Resizing
// and updates the target node counts, leaving the current counts pinned until a
// stopResize settles them. A resize on a pool that is not Steady is rejected with
// a FailedPrecondition (the ARM 409 PoolBeingResized real Azure returns). A nil
// target leaves that dimension's target unchanged.
func (m *Mock) Resize(
	_ context.Context, sub, rg, account, name string, targetDedicated, targetLowPriority *int,
) (Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := poolKey(sub, rg, account, name)

	p, ok := m.pools.Get(k)
	if !ok {
		return Pool{}, cerrors.Newf(cerrors.NotFound, "batch pool %q not found", name)
	}

	if p.AllocationState != allocationSteady {
		return Pool{}, cerrors.Newf(cerrors.FailedPrecondition,
			"pool %q is already resizing", name)
	}

	updated := *p

	if targetDedicated != nil {
		updated.TargetDedicatedNodes = *targetDedicated
	}

	if targetLowPriority != nil {
		updated.TargetLowPriorityNodes = *targetLowPriority
	}

	updated.AllocationState = allocationResizing
	m.pools.Set(k, &updated)

	return clonePool(&updated), nil
}

// StopResize halts an in-flight resize: it moves allocationState
// Resizing->Steady and settles the current node counts onto the targets. A
// stopResize on a pool that is not Resizing is rejected with a
// FailedPrecondition (the ARM 409 real Azure returns for a Steady pool).
func (m *Mock) StopResize(_ context.Context, sub, rg, account, name string) (Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := poolKey(sub, rg, account, name)

	p, ok := m.pools.Get(k)
	if !ok {
		return Pool{}, cerrors.Newf(cerrors.NotFound, "batch pool %q not found", name)
	}

	if p.AllocationState != allocationResizing {
		return Pool{}, cerrors.Newf(cerrors.FailedPrecondition,
			"pool %q is not resizing", name)
	}

	updated := *p
	updated.AllocationState = allocationSteady
	updated.CurrentDedicatedNodes = updated.TargetDedicatedNodes
	updated.CurrentLowPriorityNodes = updated.TargetLowPriorityNodes
	m.pools.Set(k, &updated)

	return clonePool(&updated), nil
}

// applyPoolInput overlays the mutable request fields onto p, leaving the computed
// fields untouched. A nil pointer/map means "not supplied": the stored (or
// default) value is preserved, so a PATCH merges only what it names.
func applyPoolInput(p *Pool, in *PoolInput) {
	if in.Tags != nil {
		p.Tags = maps.Clone(in.Tags)
	}

	if in.VMSize != nil && *in.VMSize != "" {
		p.VMSize = *in.VMSize
	}

	if in.DisplayName != nil {
		p.DisplayName = *in.DisplayName
	}

	if in.DeploymentConfiguration != nil {
		p.DeploymentConfiguration = append(json.RawMessage(nil), in.DeploymentConfiguration...)
	}

	applyPoolScale(p, in)
}

// applyPoolScale overlays the fixed-scale node targets and resize timeout,
// defaulting the timeout when none is set yet.
func applyPoolScale(p *Pool, in *PoolInput) {
	if in.TargetDedicatedNodes != nil {
		p.TargetDedicatedNodes = *in.TargetDedicatedNodes
	}

	if in.TargetLowPriorityNodes != nil {
		p.TargetLowPriorityNodes = *in.TargetLowPriorityNodes
	}

	if in.ResizeTimeout != nil && *in.ResizeTimeout != "" {
		p.ResizeTimeout = *in.ResizeTimeout
	}

	if p.ResizeTimeout == "" {
		p.ResizeTimeout = defaultResizeTimeout
	}
}

// validatePool rejects a pool create/update with missing required fields.
func validatePool(sub, rg, account, name string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case account == "":
		return cerrors.New(cerrors.InvalidArgument, "account name is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "pool name is required")
	default:
		return nil
	}
}

// clonePool deep-copies a stored pool so callers never alias the backing store.
func clonePool(p *Pool) Pool {
	out := *p
	out.Tags = maps.Clone(p.Tags)

	if p.DeploymentConfiguration != nil {
		out.DeploymentConfiguration = append(json.RawMessage(nil), p.DeploymentConfiguration...)
	}

	return out
}
