package frontdoor

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// grandchildKey keys the origin and route stores by
// (resourceGroup, profile, parent, name), where parent is the origin group (for
// an origin) or the endpoint (for a route).
func grandchildKey(rg, profile, parent, name string) string {
	return childKey(rg, profile, parent) + "/" + strings.ToLower(name)
}

// CreateOrUpdateOrigin stores o under (rg, profile, originGroup, name) as a full
// replace. The parent origin group must exist.
//
//nolint:gocritic // hugeParam: value carries maps copied defensively below.
func (m *Mock) CreateOrUpdateOrigin(
	_ context.Context, rg, profile, originGroup, name string, o driver.AzureFrontDoorOrigin,
) (*driver.AzureFrontDoorOrigin, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "front door origin name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.originGroups.Has(childKey(rg, profile, originGroup)) {
		return nil, false, cerrors.Newf(cerrors.NotFound, "front door origin group %q not found", originGroup)
	}

	key := grandchildKey(rg, profile, originGroup, name)
	_, existed := m.origins.Get(key)

	stored := cloneOrigin(o)
	stored.Name = name
	stored.ResourceGroup = rg
	stored.Profile = profile
	stored.OriginGroup = originGroup

	m.origins.Set(key, stored)

	out := cloneOrigin(stored)

	return &out, !existed, nil
}

// GetOrigin returns the stored origin.
func (m *Mock) GetOrigin(_ context.Context, rg, profile, originGroup, name string) (*driver.AzureFrontDoorOrigin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	o, ok := m.origins.Get(grandchildKey(rg, profile, originGroup, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door origin %q not found", name)
	}

	out := cloneOrigin(o)

	return &out, nil
}

// DeleteOrigin removes the stored origin.
func (m *Mock) DeleteOrigin(_ context.Context, rg, profile, originGroup, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.origins.Delete(grandchildKey(rg, profile, originGroup, name)) {
		return cerrors.Newf(cerrors.NotFound, "front door origin %q not found", name)
	}

	return nil
}

// ListOrigins returns the origins under (rg, profile, originGroup).
//
//nolint:dupl // parallel to ListRoutes over distinct grandchild types and stores.
func (m *Mock) ListOrigins(_ context.Context, rg, profile, originGroup string) ([]driver.AzureFrontDoorOrigin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.originGroups.Has(childKey(rg, profile, originGroup)) {
		return nil, cerrors.Newf(cerrors.NotFound, "front door origin group %q not found", originGroup)
	}

	all := m.origins.SortedValues()
	out := make([]driver.AzureFrontDoorOrigin, 0, len(all))

	for i := range all {
		if sameParent(all[i].ResourceGroup, all[i].Profile, all[i].OriginGroup, rg, profile, originGroup) {
			out = append(out, cloneOrigin(all[i]))
		}
	}

	return out, nil
}

// CreateOrUpdateRoute stores r under (rg, profile, endpoint, name) as a full
// replace. The parent endpoint must exist, and r.OriginGroup must name an origin
// group in the same profile.
//
//nolint:gocritic // hugeParam: value carries maps copied defensively below.
func (m *Mock) CreateOrUpdateRoute(
	_ context.Context, rg, profile, endpoint, name string, r driver.AzureFrontDoorRoute,
) (*driver.AzureFrontDoorRoute, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "front door route name is required")
	}

	if r.OriginGroup == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "front door route originGroup is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.endpoints.Has(childKey(rg, profile, endpoint)) {
		return nil, false, cerrors.Newf(cerrors.NotFound, "front door endpoint %q not found", endpoint)
	}

	if !m.originGroups.Has(childKey(rg, profile, r.OriginGroup)) {
		return nil, false, cerrors.Newf(cerrors.InvalidArgument,
			"front door route references origin group %q, which does not exist in profile %q", r.OriginGroup, profile)
	}

	key := grandchildKey(rg, profile, endpoint, name)
	_, existed := m.routes.Get(key)

	stored := cloneRoute(r)
	stored.Name = name
	stored.ResourceGroup = rg
	stored.Profile = profile
	stored.Endpoint = endpoint

	m.routes.Set(key, stored)

	out := cloneRoute(stored)

	return &out, !existed, nil
}

// GetRoute returns the stored route.
func (m *Mock) GetRoute(_ context.Context, rg, profile, endpoint, name string) (*driver.AzureFrontDoorRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.routes.Get(grandchildKey(rg, profile, endpoint, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door route %q not found", name)
	}

	out := cloneRoute(r)

	return &out, nil
}

// DeleteRoute removes the stored route.
func (m *Mock) DeleteRoute(_ context.Context, rg, profile, endpoint, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.routes.Delete(grandchildKey(rg, profile, endpoint, name)) {
		return cerrors.Newf(cerrors.NotFound, "front door route %q not found", name)
	}

	return nil
}

// ListRoutes returns the routes under (rg, profile, endpoint).
//
//nolint:dupl // parallel to ListOrigins over distinct grandchild types and stores.
func (m *Mock) ListRoutes(_ context.Context, rg, profile, endpoint string) ([]driver.AzureFrontDoorRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.endpoints.Has(childKey(rg, profile, endpoint)) {
		return nil, cerrors.Newf(cerrors.NotFound, "front door endpoint %q not found", endpoint)
	}

	all := m.routes.SortedValues()
	out := make([]driver.AzureFrontDoorRoute, 0, len(all))

	for i := range all {
		if sameParent(all[i].ResourceGroup, all[i].Profile, all[i].Endpoint, rg, profile, endpoint) {
			out = append(out, cloneRoute(all[i]))
		}
	}

	return out, nil
}

// sameParent reports whether a grandchild's (rg, profile, parent) matches the
// wanted triple, case-insensitively (ARM names are case-insensitive).
func sameParent(rg, profile, parent, wantRG, wantProfile, wantParent string) bool {
	return strings.EqualFold(rg, wantRG) && strings.EqualFold(profile, wantProfile) &&
		strings.EqualFold(parent, wantParent)
}

// routeReferencing returns the name of the first route in (rg, profile) that
// forwards to originGroup, or "" when none does. Callers hold m.mu.
func (m *Mock) routeReferencing(rg, profile, originGroup string) string {
	for _, r := range m.routes.SortedValues() {
		if sameParent(r.ResourceGroup, r.Profile, r.OriginGroup, rg, profile, originGroup) {
			return r.Name
		}
	}

	return ""
}

// purgeOrigins deletes every origin under (rg, profile, originGroup). An empty
// originGroup matches every origin in the profile. Callers hold m.mu.
func (m *Mock) purgeOrigins(rg, profile, originGroup string) {
	for _, o := range m.origins.SortedValues() {
		if !strings.EqualFold(o.ResourceGroup, rg) || !strings.EqualFold(o.Profile, profile) {
			continue
		}

		if originGroup == "" || strings.EqualFold(o.OriginGroup, originGroup) {
			m.origins.Delete(grandchildKey(o.ResourceGroup, o.Profile, o.OriginGroup, o.Name))
		}
	}
}

// purgeRoutes deletes every route under (rg, profile, endpoint). An empty
// endpoint matches every route in the profile. Callers hold m.mu.
func (m *Mock) purgeRoutes(rg, profile, endpoint string) {
	for _, r := range m.routes.SortedValues() {
		if !strings.EqualFold(r.ResourceGroup, rg) || !strings.EqualFold(r.Profile, profile) {
			continue
		}

		if endpoint == "" || strings.EqualFold(r.Endpoint, endpoint) {
			m.routes.Delete(grandchildKey(r.ResourceGroup, r.Profile, r.Endpoint, r.Name))
		}
	}
}
