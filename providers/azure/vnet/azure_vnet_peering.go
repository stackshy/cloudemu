package vnet

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// UpsertAzureVNetPeering creates or replaces a single virtualNetworkPeerings
// sub-resource by name via an atomic read-modify-write on the stored slice,
// leaving every sibling peering on the same VNet untouched.
//
//nolint:gocritic // hugeParam: peering is a small value type; interface method signature cannot be changed.
func (m *Mock) UpsertAzureVNetPeering(_ context.Context, vnetID string, peering driver.AzureVNetPeering) (driver.AzureVNetPeering, error) {
	if !m.vpcs.Has(vnetID) {
		return driver.AzureVNetPeering{}, cerrors.Newf(cerrors.NotFound, "virtual network %q not found", vnetID)
	}

	// Ensure the key exists so the Update below always finds it — a VNet's
	// first peering has nothing to read-modify-write yet.
	m.azureVNetPeerings.SetIfAbsent(vnetID, nil)

	m.azureVNetPeerings.Update(vnetID, func(peerings []driver.AzureVNetPeering) []driver.AzureVNetPeering {
		out := append([]driver.AzureVNetPeering(nil), peerings...)

		for i := range out {
			if out[i].Name == peering.Name {
				out[i] = peering
				return out
			}
		}

		return append(out, peering)
	})

	return peering, nil
}

// GetAzureVNetPeering returns one stored peering by name.
func (m *Mock) GetAzureVNetPeering(_ context.Context, vnetID, peeringName string) (driver.AzureVNetPeering, bool) {
	peerings, ok := m.azureVNetPeerings.Get(vnetID)
	if !ok {
		return driver.AzureVNetPeering{}, false
	}

	for _, p := range peerings {
		if p.Name == peeringName {
			return p, true
		}
	}

	return driver.AzureVNetPeering{}, false
}

// ListAzureVNetPeerings returns every peering stored for a VNet, ordered by
// name — map iteration order is random and real ARM returns a deterministic
// list ordering.
func (m *Mock) ListAzureVNetPeerings(_ context.Context, vnetID string) []driver.AzureVNetPeering {
	peerings, ok := m.azureVNetPeerings.Get(vnetID)
	if !ok {
		return nil
	}

	out := append([]driver.AzureVNetPeering(nil), peerings...)

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// DeleteAzureVNetPeering removes a single peering by name via an atomic
// read-modify-write, leaving every sibling peering untouched.
func (m *Mock) DeleteAzureVNetPeering(_ context.Context, vnetID, peeringName string) error {
	if !m.vpcs.Has(vnetID) {
		return cerrors.Newf(cerrors.NotFound, "virtual network %q not found", vnetID)
	}

	missing := false

	ok := m.azureVNetPeerings.Update(vnetID, func(peerings []driver.AzureVNetPeering) []driver.AzureVNetPeering {
		idx := -1

		for i := range peerings {
			if peerings[i].Name == peeringName {
				idx = i
				break
			}
		}

		if idx == -1 {
			missing = true
			return peerings
		}

		return append(append([]driver.AzureVNetPeering(nil), peerings[:idx]...), peerings[idx+1:]...)
	})

	if !ok || missing {
		return cerrors.Newf(cerrors.NotFound, "virtual network peering %q not found", peeringName)
	}

	return nil
}

// SetAzureVNetPeeringState atomically updates just the peeringState field of
// one stored peering via a read-modify-write, used to sync the reciprocal
// side of a two-way peering without clobbering its other properties.
func (m *Mock) SetAzureVNetPeeringState(_ context.Context, vnetID, peeringName, state string) error {
	if !m.vpcs.Has(vnetID) {
		return cerrors.Newf(cerrors.NotFound, "virtual network %q not found", vnetID)
	}

	missing := false

	ok := m.azureVNetPeerings.Update(vnetID, func(peerings []driver.AzureVNetPeering) []driver.AzureVNetPeering {
		out := append([]driver.AzureVNetPeering(nil), peerings...)

		for i := range out {
			if out[i].Name == peeringName {
				out[i].PeeringState = state
				return out
			}
		}

		missing = true

		return out
	})

	if !ok || missing {
		return cerrors.Newf(cerrors.NotFound, "virtual network peering %q not found", peeringName)
	}

	return nil
}
