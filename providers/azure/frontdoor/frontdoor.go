// Package frontdoor provides an in-memory implementation of the Azure Front Door
// Standard/Premium (Microsoft.Cdn/profiles) store and its two independently-
// addressable child types (afdEndpoints, originGroups). Each type is stored
// natively; profiles are keyed by (resourceGroup, name) and children by
// (resourceGroup, profile, name), matching ARM addressing. Deleting a profile
// cascades to its children.
package frontdoor

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// Compile-time check that Mock implements the Azure Front Door store.
var _ driver.AzureFrontDoorProfiles = (*Mock)(nil)

// Mock is an in-memory Azure Front Door store: a parent profile store plus two
// child stores (endpoints, origin groups).
type Mock struct {
	profiles     *memstore.Store[driver.AzureFrontDoorProfile]
	endpoints    *memstore.Store[driver.AzureFrontDoorEndpoint]
	originGroups *memstore.Store[driver.AzureFrontDoorOriginGroup]
	opts         *config.Options
}

// New creates a new Front Door mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		profiles:     memstore.New[driver.AzureFrontDoorProfile](),
		endpoints:    memstore.New[driver.AzureFrontDoorEndpoint](),
		originGroups: memstore.New[driver.AzureFrontDoorOriginGroup](),
		opts:         opts,
	}
}

// profileKey keys the profile store by (resourceGroup, name). ARM names are
// case-insensitive, so the key is lower-cased; the stored body preserves the
// original casing.
func profileKey(rg, name string) string {
	return strings.ToLower(rg) + "/" + strings.ToLower(name)
}

// childKey keys a child store by (resourceGroup, profile, name).
func childKey(rg, profile, name string) string {
	return strings.ToLower(rg) + "/" + strings.ToLower(profile) + "/" + strings.ToLower(name)
}

// CreateOrUpdateProfile stores p as a full replace and reports whether it did not
// previously exist.
//
//nolint:gocritic // hugeParam: value carries maps copied defensively below.
func (m *Mock) CreateOrUpdateProfile(
	_ context.Context, rg, name string, p driver.AzureFrontDoorProfile,
) (*driver.AzureFrontDoorProfile, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "front door profile name is required")
	}

	_, existed := m.profiles.Get(profileKey(rg, name))

	stored := cloneProfile(p)
	stored.Name = name
	stored.ResourceGroup = rg

	m.profiles.Set(profileKey(rg, name), stored)

	out := cloneProfile(stored)

	return &out, !existed, nil
}

// GetProfile returns the stored profile.
func (m *Mock) GetProfile(_ context.Context, rg, name string) (*driver.AzureFrontDoorProfile, error) {
	p, ok := m.profiles.Get(profileKey(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door profile %q not found", name)
	}

	out := cloneProfile(p)

	return &out, nil
}

// DeleteProfile removes the profile and cascades to its endpoints and origin
// groups.
func (m *Mock) DeleteProfile(_ context.Context, rg, name string) error {
	if !m.profiles.Delete(profileKey(rg, name)) {
		return cerrors.Newf(cerrors.NotFound, "front door profile %q not found", name)
	}

	m.purgeChildren(rg, name)

	return nil
}

// purgeChildren deletes every endpoint and origin group under (rg, profile).
func (m *Mock) purgeChildren(rg, profile string) {
	for _, e := range m.endpoints.SortedValues() {
		if strings.EqualFold(e.ResourceGroup, rg) && strings.EqualFold(e.Profile, profile) {
			m.endpoints.Delete(childKey(e.ResourceGroup, e.Profile, e.Name))
		}
	}

	for _, g := range m.originGroups.SortedValues() {
		if strings.EqualFold(g.ResourceGroup, rg) && strings.EqualFold(g.Profile, profile) {
			m.originGroups.Delete(childKey(g.ResourceGroup, g.Profile, g.Name))
		}
	}
}

// ListProfiles returns the profiles in rg, or all when rg is empty.
func (m *Mock) ListProfiles(_ context.Context, rg string) ([]driver.AzureFrontDoorProfile, error) {
	all := m.profiles.SortedValues()

	out := make([]driver.AzureFrontDoorProfile, 0, len(all))

	for i := range all {
		if rg != "" && !strings.EqualFold(all[i].ResourceGroup, rg) {
			continue
		}

		out = append(out, cloneProfile(all[i]))
	}

	return out, nil
}

// CreateOrUpdateEndpoint stores e under (rg, profile, name) as a full replace.
//
//nolint:gocritic,dupl // hugeParam: maps copied below; parallel to CreateOrUpdateOriginGroup over distinct types.
func (m *Mock) CreateOrUpdateEndpoint(
	_ context.Context, rg, profile, name string, e driver.AzureFrontDoorEndpoint,
) (*driver.AzureFrontDoorEndpoint, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "front door endpoint name is required")
	}

	if !m.profiles.Has(profileKey(rg, profile)) {
		return nil, false, cerrors.Newf(cerrors.NotFound, "front door profile %q not found", profile)
	}

	_, existed := m.endpoints.Get(childKey(rg, profile, name))

	stored := cloneEndpoint(e)
	stored.Name = name
	stored.ResourceGroup = rg
	stored.Profile = profile

	m.endpoints.Set(childKey(rg, profile, name), stored)

	out := cloneEndpoint(stored)

	return &out, !existed, nil
}

// GetEndpoint returns the stored endpoint.
func (m *Mock) GetEndpoint(_ context.Context, rg, profile, name string) (*driver.AzureFrontDoorEndpoint, error) {
	e, ok := m.endpoints.Get(childKey(rg, profile, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door endpoint %q not found", name)
	}

	out := cloneEndpoint(e)

	return &out, nil
}

// DeleteEndpoint removes the stored endpoint.
func (m *Mock) DeleteEndpoint(_ context.Context, rg, profile, name string) error {
	if !m.endpoints.Delete(childKey(rg, profile, name)) {
		return cerrors.Newf(cerrors.NotFound, "front door endpoint %q not found", name)
	}

	return nil
}

// ListEndpoints returns the endpoints under (rg, profile).
func (m *Mock) ListEndpoints(_ context.Context, rg, profile string) ([]driver.AzureFrontDoorEndpoint, error) {
	all := m.endpoints.SortedValues()

	out := make([]driver.AzureFrontDoorEndpoint, 0, len(all))

	for i := range all {
		if strings.EqualFold(all[i].ResourceGroup, rg) && strings.EqualFold(all[i].Profile, profile) {
			out = append(out, cloneEndpoint(all[i]))
		}
	}

	return out, nil
}

// CreateOrUpdateOriginGroup stores g under (rg, profile, name) as a full replace.
//
//nolint:dupl // parallel to CreateOrUpdateEndpoint over distinct child types and stores.
func (m *Mock) CreateOrUpdateOriginGroup(
	_ context.Context, rg, profile, name string, g driver.AzureFrontDoorOriginGroup,
) (*driver.AzureFrontDoorOriginGroup, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "front door origin group name is required")
	}

	if !m.profiles.Has(profileKey(rg, profile)) {
		return nil, false, cerrors.Newf(cerrors.NotFound, "front door profile %q not found", profile)
	}

	_, existed := m.originGroups.Get(childKey(rg, profile, name))

	stored := cloneOriginGroup(g)
	stored.Name = name
	stored.ResourceGroup = rg
	stored.Profile = profile

	m.originGroups.Set(childKey(rg, profile, name), stored)

	out := cloneOriginGroup(stored)

	return &out, !existed, nil
}

// GetOriginGroup returns the stored origin group.
func (m *Mock) GetOriginGroup(_ context.Context, rg, profile, name string) (*driver.AzureFrontDoorOriginGroup, error) {
	g, ok := m.originGroups.Get(childKey(rg, profile, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door origin group %q not found", name)
	}

	out := cloneOriginGroup(g)

	return &out, nil
}

// DeleteOriginGroup removes the stored origin group.
func (m *Mock) DeleteOriginGroup(_ context.Context, rg, profile, name string) error {
	if !m.originGroups.Delete(childKey(rg, profile, name)) {
		return cerrors.Newf(cerrors.NotFound, "front door origin group %q not found", name)
	}

	return nil
}

// ListOriginGroups returns the origin groups under (rg, profile).
func (m *Mock) ListOriginGroups(_ context.Context, rg, profile string) ([]driver.AzureFrontDoorOriginGroup, error) {
	all := m.originGroups.SortedValues()

	out := make([]driver.AzureFrontDoorOriginGroup, 0, len(all))

	for i := range all {
		if strings.EqualFold(all[i].ResourceGroup, rg) && strings.EqualFold(all[i].Profile, profile) {
			out = append(out, cloneOriginGroup(all[i]))
		}
	}

	return out, nil
}
