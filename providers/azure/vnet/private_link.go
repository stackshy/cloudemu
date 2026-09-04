package vnet

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time check that Mock serves the Azure Private Link surface.
var _ driver.AzurePrivateLink = (*Mock)(nil)

// privateLinkKey composes the store key from the ARM addressing pair, matching
// gatewayKey: resource-group names are case-insensitive in Azure, so the group
// is lower-cased while the resource name is preserved as-is.
func privateLinkKey(resourceGroup, name string) string {
	return strings.ToLower(resourceGroup) + "/" + name
}

// Private endpoints.

// PutAzurePrivateEndpoint creates or replaces a private endpoint in place, keyed
// by (resourceGroup, name), so a repeat createOrUpdate PUT updates rather than
// duplicating.
//
//nolint:gocritic // hugeParam: pe mirrors the AzurePrivateLink driver signature.
func (m *Mock) PutAzurePrivateEndpoint(_ context.Context, pe driver.AzurePrivateEndpoint) driver.AzurePrivateEndpoint {
	stored := clonePrivateEndpoint(pe)
	m.azurePrivateEndpoints.Set(privateLinkKey(pe.ResourceGroup, pe.Name), stored)

	return clonePrivateEndpoint(stored)
}

// GetAzurePrivateEndpoint returns the endpoint identified by (resourceGroup, name).
func (m *Mock) GetAzurePrivateEndpoint(
	_ context.Context, resourceGroup, name string,
) (driver.AzurePrivateEndpoint, bool) {
	pe, ok := m.azurePrivateEndpoints.Get(privateLinkKey(resourceGroup, name))
	if !ok {
		return driver.AzurePrivateEndpoint{}, false
	}

	return clonePrivateEndpoint(pe), true
}

// DeleteAzurePrivateEndpoint removes the endpoint, reporting whether it existed.
func (m *Mock) DeleteAzurePrivateEndpoint(_ context.Context, resourceGroup, name string) bool {
	return m.azurePrivateEndpoints.Delete(privateLinkKey(resourceGroup, name))
}

// ListAzurePrivateEndpoints returns the endpoints in a resource group, or all
// when resourceGroup is empty (subscription-wide list), ordered by key.
func (m *Mock) ListAzurePrivateEndpoints(_ context.Context, resourceGroup string) []driver.AzurePrivateEndpoint {
	out := make([]driver.AzurePrivateEndpoint, 0)

	values := m.azurePrivateEndpoints.SortedValues()
	for i := range values {
		if resourceGroup != "" && !strings.EqualFold(values[i].ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, clonePrivateEndpoint(values[i]))
	}

	return out
}

// Private link services.

// PutAzurePrivateLinkService creates or replaces a private link service in place.
//
//nolint:gocritic // hugeParam: pls mirrors the AzurePrivateLink driver signature.
func (m *Mock) PutAzurePrivateLinkService(_ context.Context, pls driver.AzurePrivateLinkService) driver.AzurePrivateLinkService {
	stored := clonePrivateLinkService(pls)
	m.azurePrivateLinkServices.Set(privateLinkKey(pls.ResourceGroup, pls.Name), stored)

	return clonePrivateLinkService(stored)
}

// GetAzurePrivateLinkService returns the service identified by (resourceGroup, name).
func (m *Mock) GetAzurePrivateLinkService(
	_ context.Context, resourceGroup, name string,
) (driver.AzurePrivateLinkService, bool) {
	pls, ok := m.azurePrivateLinkServices.Get(privateLinkKey(resourceGroup, name))
	if !ok {
		return driver.AzurePrivateLinkService{}, false
	}

	return clonePrivateLinkService(pls), true
}

// DeleteAzurePrivateLinkService removes the service, reporting whether it existed.
func (m *Mock) DeleteAzurePrivateLinkService(_ context.Context, resourceGroup, name string) bool {
	return m.azurePrivateLinkServices.Delete(privateLinkKey(resourceGroup, name))
}

// ListAzurePrivateLinkServices returns the services in a resource group, or all
// when resourceGroup is empty (subscription-wide list), ordered by key.
func (m *Mock) ListAzurePrivateLinkServices(_ context.Context, resourceGroup string) []driver.AzurePrivateLinkService {
	out := make([]driver.AzurePrivateLinkService, 0)

	values := m.azurePrivateLinkServices.SortedValues()
	for i := range values {
		if resourceGroup != "" && !strings.EqualFold(values[i].ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, clonePrivateLinkService(values[i]))
	}

	return out
}

// Clone helpers deep-copy the tag map and any slices so stored and returned
// values never alias a caller's containers.

//nolint:gocritic // hugeParam: pe mirrors the AzurePrivateLink driver signature.
func clonePrivateEndpoint(pe driver.AzurePrivateEndpoint) driver.AzurePrivateEndpoint {
	out := pe
	out.Tags = maybeCopyTags(pe.Tags)
	out.PrivateLinkServiceConnections = cloneConnections(pe.PrivateLinkServiceConnections)
	out.ManualConnections = cloneConnections(pe.ManualConnections)

	return out
}

func cloneConnections(in []driver.AzurePrivateLinkServiceConnection) []driver.AzurePrivateLinkServiceConnection {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.AzurePrivateLinkServiceConnection, len(in))
	for i := range in {
		out[i] = in[i]

		if len(in[i].GroupIDs) > 0 {
			out[i].GroupIDs = append([]string(nil), in[i].GroupIDs...)
		}
	}

	return out
}

//nolint:gocritic // hugeParam: pls mirrors the AzurePrivateLink driver signature.
func clonePrivateLinkService(pls driver.AzurePrivateLinkService) driver.AzurePrivateLinkService {
	out := pls
	out.Tags = maybeCopyTags(pls.Tags)

	if len(pls.LoadBalancerFrontendIDs) > 0 {
		out.LoadBalancerFrontendIDs = append([]string(nil), pls.LoadBalancerFrontendIDs...)
	}

	if len(pls.VisibilitySubscriptions) > 0 {
		out.VisibilitySubscriptions = append([]string(nil), pls.VisibilitySubscriptions...)
	}

	if len(pls.AutoApprovalSubs) > 0 {
		out.AutoApprovalSubs = append([]string(nil), pls.AutoApprovalSubs...)
	}

	if len(pls.Fqdns) > 0 {
		out.Fqdns = append([]string(nil), pls.Fqdns...)
	}

	if len(pls.IPConfigurations) > 0 {
		out.IPConfigurations = append([]driver.AzurePrivateLinkServiceIPConfiguration(nil), pls.IPConfigurations...)
	}

	return out
}
