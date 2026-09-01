package virtualmachines

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Per-instance power states a materialized scale-set VM reports. They mirror
// the ARM PowerState codes the wire layer echoes, so the server can render them
// directly without a translation table.
const (
	scaleSetVMRunning     = "running"
	scaleSetVMStopped     = "stopped"
	scaleSetVMDeallocated = "deallocated"
)

// ScaleSet is an Azure Virtual Machine Scale Set (VMSS). Only the fields a
// discoverer prices on are modeled: the SKU (VM size / tier / instance count)
// and the per-VM profile (Spot priority, hybrid-benefit license, OS type).
type ScaleSet struct {
	Name     string
	ID       string
	Location string
	SKUName  string
	SKUTier  string
	Capacity int
	// CapacityZero must be set true when Capacity==0 is an explicit
	// scale-in-to-zero request rather than an omitted field. Without it,
	// CreateScaleSet cannot tell "capacity not specified" (default to 1)
	// apart from "capacity explicitly 0" (honor it) — real Azure
	// tooling sends the latter via a nullable capacity field.
	CapacityZero bool
	Priority     string // Spot / Regular
	LicenseType  string
	OSType       string // Linux / Windows
	Tags         map[string]string
	// ResourceGroup is the ARM resource group the scale set belongs to, so a
	// resource-group cascade delete can find and tear down its scale sets.
	ResourceGroup string
	// Instances are the per-instance VMs materialized from Capacity. Each holds
	// its ordinal instanceId and mutable power/provisioning state so a VMSS VM
	// can be enumerated, addressed, powered off, and deleted individually
	// (Microsoft.Compute/virtualMachineScaleSets/{vmss}/virtualMachines).
	Instances []ScaleSetVM
}

// ScaleSetVM is one materialized virtual machine of a scale set. instanceId is
// the string ordinal ("0", "1", …) Azure addresses the VM by; the name Azure
// renders is "{vmss}_{instanceId}", built by the wire layer.
type ScaleSetVM struct {
	InstanceID        string
	ProvisioningState string
	PowerState        string // running / stopped / deallocated
}

// CreateScaleSet stores a VMSS, defaulting the fields real Azure fills in.
//
//nolint:gocritic // hugeParam: s mirrors the scaleSetStore interface signature.
func (m *Mock) CreateScaleSet(_ context.Context, s ScaleSet) (*ScaleSet, error) {
	if s.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "scale set name is required")
	}

	m.applyScaleSetDefaults(&s)

	// CreateOrUpdate is idempotent: on a re-PUT of an existing scale set, carry
	// its already-materialized instances forward so their power state survives,
	// then reconcile only the count to the requested capacity.
	if existing, ok := m.scaleSets.Get(s.Name); ok {
		s.Instances = existing.Instances
	}

	s.Instances = reconcileScaleSetVMs(s.Instances, s.Capacity)

	stored := s

	m.scaleSets.Set(s.Name, &stored)

	out := stored

	return &out, nil
}

// applyScaleSetDefaults fills the fields real Azure defaults on create, in
// place. A zero capacity is defaulted to 1 only when it wasn't explicitly
// requested; an explicit "capacity":0 (scale-in-to-zero) is honored — a VMSS at
// capacity 0 is a valid, running-with-no-instances state.
func (m *Mock) applyScaleSetDefaults(s *ScaleSet) {
	if s.ID == "" {
		s.ID = fmt.Sprintf(
			"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/%s", s.Name)
	}

	if s.Location == "" {
		s.Location = m.opts.Region
	}

	if s.SKUName == "" {
		s.SKUName = "Standard_D2s_v3"
	}

	if s.SKUTier == "" {
		s.SKUTier = "Standard"
	}

	if s.Capacity == 0 && !s.CapacityZero {
		s.Capacity = 1
	}

	if s.Priority == "" {
		s.Priority = "Regular"
	}

	if s.OSType == "" {
		s.OSType = "Linux"
	}
}

// reconcileScaleSetVMs returns the instance set a scale set at capacity should
// hold, given the instances it currently holds. It always returns a fresh
// slice (never aliasing existing) so the caller can store it without sharing a
// backing array with a prior version. Scaling out appends new instances with
// the next monotonic ordinals (so a gap left by a deleted instance is not
// reused); scaling in drops the highest-ordinal instances, matching Azure's
// default scale-in ordering.
func reconcileScaleSetVMs(existing []ScaleSetVM, capacity int) []ScaleSetVM {
	if capacity < 0 {
		capacity = 0
	}

	out := make([]ScaleSetVM, len(existing))
	copy(out, existing)
	sortScaleSetVMs(out)

	if len(out) > capacity {
		return out[:capacity]
	}

	for next := nextOrdinal(out); len(out) < capacity; next++ {
		out = append(out, ScaleSetVM{
			InstanceID:        strconv.Itoa(next),
			ProvisioningState: "Succeeded",
			PowerState:        scaleSetVMRunning,
		})
	}

	return out
}

// sortScaleSetVMs orders instances by numeric ordinal ascending; a non-numeric
// id (never produced by us) sorts after the numeric ones by string.
func sortScaleSetVMs(vms []ScaleSetVM) {
	sort.SliceStable(vms, func(i, j int) bool {
		a, aok := ordinalOf(vms[i].InstanceID)
		b, bok := ordinalOf(vms[j].InstanceID)

		if aok && bok {
			return a < b
		}

		if aok != bok {
			return aok
		}

		return vms[i].InstanceID < vms[j].InstanceID
	})
}

// nextOrdinal returns the smallest ordinal not already in use — one past the
// highest numeric instanceId, or 0 when there are none.
func nextOrdinal(vms []ScaleSetVM) int {
	next := 0

	for i := range vms {
		if n, ok := ordinalOf(vms[i].InstanceID); ok && n >= next {
			next = n + 1
		}
	}

	return next
}

func ordinalOf(id string) (int, bool) {
	n, err := strconv.Atoi(id)
	if err != nil {
		return 0, false
	}

	return n, true
}

// findScaleSetKey resolves a scale-set name to the exact store key, matching
// case-insensitively (ARM resource names are case-insensitive).
func (m *Mock) findScaleSetKey(name string) (string, bool) {
	if m.scaleSets.Has(name) {
		return name, true
	}

	for _, s := range m.scaleSets.SortedValues() {
		if strings.EqualFold(s.Name, name) {
			return s.Name, true
		}
	}

	return "", false
}

// ListScaleSetVMs returns the materialized VMs of a scale set (ARM VMSS VMs
// List). Returns NotFound when no scale set with that name exists.
func (m *Mock) ListScaleSetVMs(_ context.Context, vmssName string) ([]ScaleSetVM, error) {
	key, ok := m.findScaleSetKey(vmssName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %q not found", vmssName)
	}

	set, _ := m.scaleSets.Get(key)

	out := make([]ScaleSetVM, len(set.Instances))
	copy(out, set.Instances)

	return out, nil
}

// GetScaleSetVM returns a single materialized VM of a scale set by instanceId.
func (m *Mock) GetScaleSetVM(_ context.Context, vmssName, instanceID string) (*ScaleSetVM, error) {
	key, ok := m.findScaleSetKey(vmssName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %q not found", vmssName)
	}

	set, _ := m.scaleSets.Get(key)

	for i := range set.Instances {
		if set.Instances[i].InstanceID == instanceID {
			vm := set.Instances[i]
			return &vm, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %q has no instance %q", vmssName, instanceID)
}

// DeleteScaleSetVM removes one instance from a scale set and decrements its
// effective capacity, so a follow-up list reports one fewer VM.
func (m *Mock) DeleteScaleSetVM(_ context.Context, vmssName, instanceID string) error {
	key, ok := m.findScaleSetKey(vmssName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %q not found", vmssName)
	}

	found := false

	m.scaleSets.Update(key, func(s *ScaleSet) *ScaleSet {
		for i := range s.Instances {
			if s.Instances[i].InstanceID != instanceID {
				continue
			}

			s.Instances = append(s.Instances[:i], s.Instances[i+1:]...)
			s.Capacity = len(s.Instances)
			found = true

			break
		}

		return s
	})

	if !found {
		return cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %q has no instance %q", vmssName, instanceID)
	}

	return nil
}

// PowerScaleSetVM applies a per-instance power action (start / poweroff /
// deallocate / restart / reimage) to one VM of a scale set, updating its power
// state. Unknown actions are rejected with InvalidArgument.
func (m *Mock) PowerScaleSetVM(_ context.Context, vmssName, instanceID, action string) error {
	key, ok := m.findScaleSetKey(vmssName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %q not found", vmssName)
	}

	power, perr := powerStateForAction(action)
	if perr != nil {
		return perr
	}

	found := false

	m.scaleSets.Update(key, func(s *ScaleSet) *ScaleSet {
		for i := range s.Instances {
			if s.Instances[i].InstanceID == instanceID {
				s.Instances[i].PowerState = power
				found = true

				break
			}
		}

		return s
	})

	if !found {
		return cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %q has no instance %q", vmssName, instanceID)
	}

	return nil
}

// powerStateForAction maps a per-instance power action verb to the power state
// the instance settles in. start/restart/reimage leave the VM running;
// poweroff stops it (allocated); deallocate releases the compute.
func powerStateForAction(action string) (string, error) {
	switch strings.ToLower(action) {
	case "start", "restart", "reimage":
		return scaleSetVMRunning, nil
	case "poweroff":
		return scaleSetVMStopped, nil
	case "deallocate":
		return scaleSetVMDeallocated, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument, "unsupported scale-set VM power action %q", action)
	}
}

// DeleteScaleSet removes a stored VMSS by name (ARM VMSS Delete). Returns
// NotFound when no scale set with that name exists.
func (m *Mock) DeleteScaleSet(_ context.Context, name string) error {
	if !m.scaleSets.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %q not found", name)
	}

	return nil
}

// ListScaleSets returns every stored VMSS.
func (m *Mock) ListScaleSets(_ context.Context) ([]ScaleSet, error) {
	stored := m.scaleSets.SortedValues()

	out := make([]ScaleSet, 0, len(stored))
	for _, s := range stored {
		out = append(out, *s)
	}

	return out, nil
}
