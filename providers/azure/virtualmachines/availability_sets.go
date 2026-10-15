package virtualmachines

import (
	"context"
	"maps"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

var _ driver.AzureAvailabilitySets = (*Mock)(nil)

// Availability set domain limits real Azure enforces.
const (
	maxFaultDomains  = 3
	maxUpdateDomains = 20
)

// availabilitySetKey keys a set by subscription, resource group and name;
// names are unique per resource group and case-insensitive.
func availabilitySetKey(subscription, resourceGroup, name string) string {
	return strings.ToLower(subscription + "/" + resourceGroup + "/" + name)
}

// PutAvailabilitySet creates or replaces an availability set.
//
//nolint:gocritic // set mirrors the interface's by-value request payload.
func (m *Mock) PutAvailabilitySet(
	_ context.Context, set driver.AzureAvailabilitySet,
) (*driver.AzureAvailabilitySet, error) {
	if set.PlatformFaultDomainCount < 1 || set.PlatformFaultDomainCount > maxFaultDomains {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"platformFaultDomainCount %d is out of range 1-%d", set.PlatformFaultDomainCount, maxFaultDomains)
	}

	if set.PlatformUpdateDomainCount < 1 || set.PlatformUpdateDomainCount > maxUpdateDomains {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"platformUpdateDomainCount %d is out of range 1-%d", set.PlatformUpdateDomainCount, maxUpdateDomains)
	}

	key := availabilitySetKey(set.Subscription, set.ResourceGroup, set.Name)

	stored := set
	stored.Tags = maps.Clone(set.Tags)
	m.availabilitySets.Set(key, &stored)

	out := stored
	out.Tags = maps.Clone(stored.Tags)

	return &out, nil
}

// GetAvailabilitySet returns one availability set.
func (m *Mock) GetAvailabilitySet(
	_ context.Context, subscription, resourceGroup, name string,
) (*driver.AzureAvailabilitySet, error) {
	set, ok := m.availabilitySets.Get(availabilitySetKey(subscription, resourceGroup, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "availability set %q not found", name)
	}

	out := *set
	out.Tags = maps.Clone(set.Tags)

	return &out, nil
}

// ListAvailabilitySets lists the sets of a subscription, optionally narrowed
// to one resource group.
func (m *Mock) ListAvailabilitySets(
	_ context.Context, subscription, resourceGroup string,
) ([]driver.AzureAvailabilitySet, error) {
	all := m.availabilitySets.All()
	keys := slices.Sorted(maps.Keys(all))
	out := make([]driver.AzureAvailabilitySet, 0, len(keys))

	for _, k := range keys {
		set := all[k]
		if !strings.EqualFold(set.Subscription, subscription) ||
			(resourceGroup != "" && !strings.EqualFold(set.ResourceGroup, resourceGroup)) {
			continue
		}

		cp := *set
		cp.Tags = maps.Clone(set.Tags)
		out = append(out, cp)
	}

	return out, nil
}

// DeleteAvailabilitySet removes an availability set.
func (m *Mock) DeleteAvailabilitySet(_ context.Context, subscription, resourceGroup, name string) error {
	if !m.availabilitySets.Delete(availabilitySetKey(subscription, resourceGroup, name)) {
		return cerrors.Newf(cerrors.NotFound, "availability set %q not found", name)
	}

	return nil
}
