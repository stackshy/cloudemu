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
	Name        string
	ID          string
	Location    string
	SKUName     string
	SKUTier     string
	Capacity    int
	Priority    string // Spot / Regular
	LicenseType string
	OSType      string // Linux / Windows
	Tags        map[string]string
}

// CreateScaleSet stores a VMSS, defaulting the fields real Azure fills in.
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

	if s.Capacity == 0 {
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

// ListScaleSets returns every stored VMSS.
func (m *Mock) ListScaleSets(_ context.Context) ([]ScaleSet, error) {
	stored := m.scaleSets.SortedValues()

	out := make([]ScaleSet, 0, len(stored))
	for _, s := range stored {
		out = append(out, *s)
	}

	return out, nil
}
