package vnet

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time check that Mock serves the Azure network-gateway surface.
var _ driver.AzureNetworkGateways = (*Mock)(nil)

// gatewayKey composes the store key from the ARM addressing pair. Resource-group
// names are case-insensitive in Azure, so it is lower-cased; the resource name is
// preserved as-is, mirroring asgKey.
func gatewayKey(resourceGroup, name string) string {
	return strings.ToLower(resourceGroup) + "/" + name
}

// Virtual network gateways.

// PutAzureVirtualNetworkGateway creates or replaces a virtual network gateway in
// place, keyed by (resourceGroup, name), so a repeat createOrUpdate PUT updates
// rather than duplicating.
//
//nolint:gocritic // hugeParam: gw mirrors the AzureNetworkGateways driver signature.
func (m *Mock) PutAzureVirtualNetworkGateway(
	_ context.Context, gw driver.AzureVirtualNetworkGateway,
) driver.AzureVirtualNetworkGateway {
	stored := cloneVNG(gw)
	m.azureVNGateways.Set(gatewayKey(gw.ResourceGroup, gw.Name), stored)

	return cloneVNG(stored)
}

// GetAzureVirtualNetworkGateway returns the gateway identified by (resourceGroup, name).
func (m *Mock) GetAzureVirtualNetworkGateway(
	_ context.Context, resourceGroup, name string,
) (driver.AzureVirtualNetworkGateway, bool) {
	gw, ok := m.azureVNGateways.Get(gatewayKey(resourceGroup, name))
	if !ok {
		return driver.AzureVirtualNetworkGateway{}, false
	}

	return cloneVNG(gw), true
}

// DeleteAzureVirtualNetworkGateway removes the gateway, reporting whether it existed.
func (m *Mock) DeleteAzureVirtualNetworkGateway(_ context.Context, resourceGroup, name string) bool {
	return m.azureVNGateways.Delete(gatewayKey(resourceGroup, name))
}

// ListAzureVirtualNetworkGateways returns the gateways in a resource group, or all
// when resourceGroup is empty (subscription-wide list), ordered by key.
func (m *Mock) ListAzureVirtualNetworkGateways(
	_ context.Context, resourceGroup string,
) []driver.AzureVirtualNetworkGateway {
	out := make([]driver.AzureVirtualNetworkGateway, 0)

	values := m.azureVNGateways.SortedValues()
	for i := range values {
		if resourceGroup != "" && !strings.EqualFold(values[i].ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, cloneVNG(values[i]))
	}

	return out
}

// Local network gateways.

// PutAzureLocalNetworkGateway creates or replaces a local network gateway in place.
//
//nolint:gocritic // hugeParam: gw mirrors the AzureNetworkGateways driver signature.
func (m *Mock) PutAzureLocalNetworkGateway(
	_ context.Context, gw driver.AzureLocalNetworkGateway,
) driver.AzureLocalNetworkGateway {
	stored := cloneLNG(gw)
	m.azureLNGateways.Set(gatewayKey(gw.ResourceGroup, gw.Name), stored)

	return cloneLNG(stored)
}

// GetAzureLocalNetworkGateway returns the local gateway identified by (resourceGroup, name).
func (m *Mock) GetAzureLocalNetworkGateway(
	_ context.Context, resourceGroup, name string,
) (driver.AzureLocalNetworkGateway, bool) {
	gw, ok := m.azureLNGateways.Get(gatewayKey(resourceGroup, name))
	if !ok {
		return driver.AzureLocalNetworkGateway{}, false
	}

	return cloneLNG(gw), true
}

// DeleteAzureLocalNetworkGateway removes the local gateway, reporting whether it existed.
func (m *Mock) DeleteAzureLocalNetworkGateway(_ context.Context, resourceGroup, name string) bool {
	return m.azureLNGateways.Delete(gatewayKey(resourceGroup, name))
}

// ListAzureLocalNetworkGateways returns the local gateways in a resource group, or
// all when resourceGroup is empty (subscription-wide list), ordered by key.
func (m *Mock) ListAzureLocalNetworkGateways(
	_ context.Context, resourceGroup string,
) []driver.AzureLocalNetworkGateway {
	out := make([]driver.AzureLocalNetworkGateway, 0)

	values := m.azureLNGateways.SortedValues()
	for i := range values {
		if resourceGroup != "" && !strings.EqualFold(values[i].ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, cloneLNG(values[i]))
	}

	return out
}

// Gateway connections.

// PutAzureVirtualNetworkGatewayConnection creates or replaces a connection in place.
//
//nolint:gocritic // hugeParam: conn mirrors the AzureNetworkGateways driver signature.
func (m *Mock) PutAzureVirtualNetworkGatewayConnection(
	_ context.Context, conn driver.AzureVirtualNetworkGatewayConnection,
) driver.AzureVirtualNetworkGatewayConnection {
	stored := cloneConn(conn)
	m.azureGWConnections.Set(gatewayKey(conn.ResourceGroup, conn.Name), stored)

	return cloneConn(stored)
}

// GetAzureVirtualNetworkGatewayConnection returns the connection identified by (resourceGroup, name).
func (m *Mock) GetAzureVirtualNetworkGatewayConnection(
	_ context.Context, resourceGroup, name string,
) (driver.AzureVirtualNetworkGatewayConnection, bool) {
	conn, ok := m.azureGWConnections.Get(gatewayKey(resourceGroup, name))
	if !ok {
		return driver.AzureVirtualNetworkGatewayConnection{}, false
	}

	return cloneConn(conn), true
}

// DeleteAzureVirtualNetworkGatewayConnection removes the connection, reporting whether it existed.
func (m *Mock) DeleteAzureVirtualNetworkGatewayConnection(_ context.Context, resourceGroup, name string) bool {
	return m.azureGWConnections.Delete(gatewayKey(resourceGroup, name))
}

// ListAzureVirtualNetworkGatewayConnections returns the connections in a resource
// group, or all when resourceGroup is empty (subscription-wide list), ordered by key.
func (m *Mock) ListAzureVirtualNetworkGatewayConnections(
	_ context.Context, resourceGroup string,
) []driver.AzureVirtualNetworkGatewayConnection {
	out := make([]driver.AzureVirtualNetworkGatewayConnection, 0)

	values := m.azureGWConnections.SortedValues()
	for i := range values {
		if resourceGroup != "" && !strings.EqualFold(values[i].ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, cloneConn(values[i]))
	}

	return out
}

// Clone helpers deep-copy the tag map and any slices so stored and returned
// values never alias a caller's containers.

//nolint:gocritic // hugeParam: gw mirrors the AzureNetworkGateways driver signature.
func cloneVNG(gw driver.AzureVirtualNetworkGateway) driver.AzureVirtualNetworkGateway {
	out := gw
	out.Tags = maybeCopyTags(gw.Tags)

	if len(gw.IPConfigurations) > 0 {
		out.IPConfigurations = append([]driver.AzureGatewayIPConfiguration(nil), gw.IPConfigurations...)
	}

	out.BgpSettings = cloneBgp(gw.BgpSettings)

	return out
}

//nolint:gocritic // hugeParam: gw mirrors the AzureNetworkGateways driver signature.
func cloneLNG(gw driver.AzureLocalNetworkGateway) driver.AzureLocalNetworkGateway {
	out := gw
	out.Tags = maybeCopyTags(gw.Tags)

	if len(gw.AddressPrefixes) > 0 {
		out.AddressPrefixes = append([]string(nil), gw.AddressPrefixes...)
	}

	out.BgpSettings = cloneBgp(gw.BgpSettings)

	return out
}

//nolint:gocritic // hugeParam: conn mirrors the AzureNetworkGateways driver signature.
func cloneConn(conn driver.AzureVirtualNetworkGatewayConnection) driver.AzureVirtualNetworkGatewayConnection {
	out := conn
	out.Tags = maybeCopyTags(conn.Tags)

	return out
}

func cloneBgp(in *driver.AzureGatewayBgpSettings) *driver.AzureGatewayBgpSettings {
	if in == nil {
		return nil
	}

	cp := *in

	return &cp
}

// maybeCopyTags copies a non-empty tag map and leaves an empty one nil, so a
// clone never aliases the caller's map yet an absent map stays absent.
func maybeCopyTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	return copyTags(tags)
}
