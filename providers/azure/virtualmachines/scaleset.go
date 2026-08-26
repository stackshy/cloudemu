package virtualmachines

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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
}

// CreateScaleSet stores a VMSS, defaulting the fields real Azure fills in.
//
//nolint:gocritic // hugeParam: s mirrors the scaleSetStore interface signature.
func (m *Mock) CreateScaleSet(_ context.Context, s ScaleSet) (*ScaleSet, error) {
	if s.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "scale set name is required")
	}

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

	// A zero capacity is only defaulted to 1 when it wasn't explicitly
	// requested; an explicit "capacity":0 (scale-in-to-zero) is honored —
	// a VMSS at capacity 0 is a valid, running-with-no-instances state.
	if s.Capacity == 0 && !s.CapacityZero {
		s.Capacity = 1
	}

	if s.Priority == "" {
		s.Priority = "Regular"
	}

	if s.OSType == "" {
		s.OSType = "Linux"
	}

	stored := s

	m.scaleSets.Set(s.Name, &stored)

	out := stored

	return &out, nil
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
