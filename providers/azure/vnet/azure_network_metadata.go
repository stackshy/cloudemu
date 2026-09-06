package vnet

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time check that Mock implements the optional Azure metadata surface.
var _ driver.AzureNetworkMetadata = (*Mock)(nil)

// PutAzureVNetMetadata stores the Azure-only virtual-network fields (region and
// full address-prefix list) for the VPC with the given driver id. ResourceGUID
// is assigned on first write and preserved across every later PUT (a repeat
// ARM CreateOrUpdate is a full replace of the other fields, but the identity
// GUID must survive), matching how network interfaces preserve theirs.
func (m *Mock) PutAzureVNetMetadata(_ context.Context, id string, meta driver.AzureVNetMetadata) error {
	if existing, ok := m.azureVNetMeta.Get(id); ok && existing.ResourceGUID != "" {
		meta.ResourceGUID = existing.ResourceGUID
	} else {
		meta.ResourceGUID = generateGUID()
	}

	m.azureVNetMeta.Set(id, cloneVNetMeta(meta))

	return nil
}

// GetAzureVNetMetadata returns the stored Azure virtual-network metadata for id.
func (m *Mock) GetAzureVNetMetadata(_ context.Context, id string) (driver.AzureVNetMetadata, bool) {
	meta, ok := m.azureVNetMeta.Get(id)
	if !ok {
		return driver.AzureVNetMetadata{}, false
	}

	return cloneVNetMeta(meta), true
}

// DeleteAzureVNetMetadata drops the stored metadata for id (called when the VPC
// is deleted).
func (m *Mock) DeleteAzureVNetMetadata(_ context.Context, id string) {
	m.azureVNetMeta.Delete(id)
}

// PutAzureNSGMetadata stores the Azure-only security-group fields (region and
// custom security rules) for the security group with the given driver id.
// ResourceGUID is assigned on first write and preserved across every later
// PUT, matching PutAzureVNetMetadata.
func (m *Mock) PutAzureNSGMetadata(_ context.Context, id string, meta driver.AzureNSGMetadata) error {
	if existing, ok := m.azureNSGMeta.Get(id); ok && existing.ResourceGUID != "" {
		meta.ResourceGUID = existing.ResourceGUID
	} else {
		meta.ResourceGUID = generateGUID()
	}

	m.azureNSGMeta.Set(id, cloneNSGMeta(meta))

	return nil
}

// GetAzureNSGMetadata returns the stored Azure security-group metadata for id.
func (m *Mock) GetAzureNSGMetadata(_ context.Context, id string) (driver.AzureNSGMetadata, bool) {
	meta, ok := m.azureNSGMeta.Get(id)
	if !ok {
		return driver.AzureNSGMetadata{}, false
	}

	return cloneNSGMeta(meta), true
}

// DeleteAzureNSGMetadata drops the stored metadata for id (called when the
// security group is deleted).
func (m *Mock) DeleteAzureNSGMetadata(_ context.Context, id string) {
	m.azureNSGMeta.Delete(id)
}

// UpsertAzureNSGRule creates or replaces a single custom security rule by
// name via an atomic read-modify-write on the stored metadata, leaving every
// sibling rule untouched — the SecurityRules sub-resource CRUD's mutation.
//
//nolint:gocritic,dupl // hugeParam: fixed interface sig; dupl: parallels UpsertAzureRoute's COW clone-one-subresource shape by design.
func (m *Mock) UpsertAzureNSGRule(_ context.Context, id string, rule driver.AzureNSGRule) (driver.AzureNSGMetadata, error) {
	var updated driver.AzureNSGMetadata

	ok := m.azureNSGMeta.Update(id, func(meta driver.AzureNSGMetadata) driver.AzureNSGMetadata {
		rules := append([]driver.AzureNSGRule(nil), meta.SecurityRules...)

		replaced := false

		for i := range rules {
			if rules[i].Name == rule.Name {
				rules[i] = rule
				replaced = true

				break
			}
		}

		if !replaced {
			rules = append(rules, rule)
		}

		meta.SecurityRules = rules
		updated = cloneNSGMeta(meta)

		return meta
	})
	if !ok {
		return driver.AzureNSGMetadata{}, cerrors.Newf(cerrors.NotFound, "network security group %q not found", id)
	}

	return updated, nil
}

// DeleteAzureNSGRule removes a single custom security rule by name via an
// atomic read-modify-write, leaving every sibling rule untouched.
func (m *Mock) DeleteAzureNSGRule(_ context.Context, id, ruleName string) error {
	ruleMissing := false

	ok := m.azureNSGMeta.Update(id, func(meta driver.AzureNSGMetadata) driver.AzureNSGMetadata {
		idx := -1

		for i := range meta.SecurityRules {
			if meta.SecurityRules[i].Name == ruleName {
				idx = i
				break
			}
		}

		if idx == -1 {
			ruleMissing = true

			return meta
		}

		meta.SecurityRules = append(append([]driver.AzureNSGRule(nil), meta.SecurityRules[:idx]...), meta.SecurityRules[idx+1:]...)

		return meta
	})
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", id)
	}

	if ruleMissing {
		return cerrors.Newf(cerrors.NotFound, "security rule %q not found", ruleName)
	}

	return nil
}

// PutAzureRouteTableMetadata stores the Azure-only route-table fields (region,
// routes and user tags) for the route table with the given driver id, replacing
// any previously stored metadata.
func (m *Mock) PutAzureRouteTableMetadata(_ context.Context, id string, meta driver.AzureRouteTableMetadata) error {
	m.azureRouteTableMeta.Set(id, cloneRouteTableMeta(meta))
	return nil
}

// GetAzureRouteTableMetadata returns the stored Azure route-table metadata for id.
func (m *Mock) GetAzureRouteTableMetadata(_ context.Context, id string) (driver.AzureRouteTableMetadata, bool) {
	meta, ok := m.azureRouteTableMeta.Get(id)
	if !ok {
		return driver.AzureRouteTableMetadata{}, false
	}

	return cloneRouteTableMeta(meta), true
}

// DeleteAzureRouteTableMetadata drops the stored metadata for id (called when
// the route table is deleted).
func (m *Mock) DeleteAzureRouteTableMetadata(_ context.Context, id string) {
	m.azureRouteTableMeta.Delete(id)
}

// UpsertAzureRoute creates or replaces a single route by name via an atomic
// read-modify-write on the stored route-table metadata, leaving every sibling
// route (and the table's other fields) untouched — the routes sub-resource
// CRUD's mutation.
//
//nolint:dupl // parallels UpsertAzureNSGRule: the same COW clone-and-mutate-one-subresource shape over a distinct metadata type by design.
func (m *Mock) UpsertAzureRoute(_ context.Context, id string, route driver.AzureRoute) (driver.AzureRouteTableMetadata, error) {
	var updated driver.AzureRouteTableMetadata

	ok := m.azureRouteTableMeta.Update(id, func(meta driver.AzureRouteTableMetadata) driver.AzureRouteTableMetadata {
		routes := append([]driver.AzureRoute(nil), meta.Routes...)

		replaced := false

		for i := range routes {
			if routes[i].Name == route.Name {
				routes[i] = route
				replaced = true

				break
			}
		}

		if !replaced {
			routes = append(routes, route)
		}

		meta.Routes = routes
		updated = cloneRouteTableMeta(meta)

		return meta
	})
	if !ok {
		return driver.AzureRouteTableMetadata{}, cerrors.Newf(cerrors.NotFound, "route table %q not found", id)
	}

	return updated, nil
}

// DeleteAzureRoute removes a single route by name via an atomic
// read-modify-write, leaving every sibling route untouched.
func (m *Mock) DeleteAzureRoute(_ context.Context, id, routeName string) error {
	routeMissing := false

	ok := m.azureRouteTableMeta.Update(id, func(meta driver.AzureRouteTableMetadata) driver.AzureRouteTableMetadata {
		idx := -1

		for i := range meta.Routes {
			if meta.Routes[i].Name == routeName {
				idx = i
				break
			}
		}

		if idx == -1 {
			routeMissing = true

			return meta
		}

		meta.Routes = append(append([]driver.AzureRoute(nil), meta.Routes[:idx]...), meta.Routes[idx+1:]...)

		return meta
	})
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "route table %q not found", id)
	}

	if routeMissing {
		return cerrors.Newf(cerrors.NotFound, "route %q not found", routeName)
	}

	return nil
}

// cloneRouteTableMeta deep-copies the route slice and tag map so stored and
// returned values never alias a caller's slice/map.
func cloneRouteTableMeta(meta driver.AzureRouteTableMetadata) driver.AzureRouteTableMetadata {
	out := driver.AzureRouteTableMetadata{Location: meta.Location}
	if len(meta.Routes) > 0 {
		out.Routes = append([]driver.AzureRoute(nil), meta.Routes...)
	}

	if len(meta.Tags) > 0 {
		out.Tags = copyTags(meta.Tags)
	}

	if meta.DisableBgpRoutePropagation != nil {
		v := *meta.DisableBgpRoutePropagation
		out.DisableBgpRoutePropagation = &v
	}

	return out
}

// cloneVNetMeta deep-copies the address-prefix slice so stored and returned
// values never alias a caller's slice.
func cloneVNetMeta(meta driver.AzureVNetMetadata) driver.AzureVNetMetadata {
	out := driver.AzureVNetMetadata{Location: meta.Location, ResourceGUID: meta.ResourceGUID}
	if len(meta.AddressPrefixes) > 0 {
		out.AddressPrefixes = append([]string(nil), meta.AddressPrefixes...)
	}

	return out
}

// cloneNSGMeta deep-copies the rule slice — and each rule's application-security-group
// reference slices — so stored and returned values never alias a caller's slice.
func cloneNSGMeta(meta driver.AzureNSGMetadata) driver.AzureNSGMetadata {
	out := driver.AzureNSGMetadata{Location: meta.Location, ResourceGUID: meta.ResourceGUID}

	if len(meta.SecurityRules) > 0 {
		rules := append([]driver.AzureNSGRule(nil), meta.SecurityRules...)
		for i := range rules {
			rules[i].SourceASGs = append([]string(nil), rules[i].SourceASGs...)
			rules[i].DestinationASGs = append([]string(nil), rules[i].DestinationASGs...)
			rules[i].SourceAddressPrefixes = append([]string(nil), rules[i].SourceAddressPrefixes...)
			rules[i].DestinationAddressPrefixes = append([]string(nil), rules[i].DestinationAddressPrefixes...)
			rules[i].SourcePortRanges = append([]string(nil), rules[i].SourcePortRanges...)
			rules[i].DestinationPortRanges = append([]string(nil), rules[i].DestinationPortRanges...)
		}

		out.SecurityRules = rules
	}

	return out
}
