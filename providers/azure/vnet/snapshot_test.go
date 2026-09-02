package vnet

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the VNet mock serializes its entire state
// and restores it into a fresh mock identity-preservingly: re-snapshotting the
// restored mock yields byte-identical JSON (so every store round-tripped,
// including the ARM metadata and NIC stores), and the seeded resources come back
// under their original ids.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()
	vpcID := createTestVPC(t, src)

	subnet, err := src.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	sg, err := src.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg1", VPCID: vpcID})
	require.NoError(t, err)

	nic, err := src.CreateOrUpdateNetworkInterface(ctx, "rg1", "nic1", driver.AzureNICConfig{
		Location: "eastus",
		IPConfigs: []driver.AzureIPConfig{
			{Name: "ipcfg1", SubnetID: subnet.ID, AllocationMethod: "Dynamic"},
		},
	})
	require.NoError(t, err)

	src.PutAzureApplicationSecurityGroup(ctx, driver.AzureApplicationSecurityGroup{
		Name: "asg1", ResourceGroup: "rg1", Location: "eastus", Tags: map[string]string{"env": "prod"},
	})

	storedPrefix := src.PutAzurePublicIPPrefix(ctx, driver.AzurePublicIPPrefix{
		Name: "pfx1", ResourceGroup: "rg1", Location: "eastus", PrefixLength: 28,
		SKUName: "Standard", SKUTier: "Regional", Tags: map[string]string{"team": "net"},
	})

	// Seed the full site-to-site VPN surface so the round-trip proves each of the
	// three gateway stores is included in the snapshot dump/restore lists — the
	// test would pass even with a store dropped if nothing referenced it.
	src.PutAzureVirtualNetworkGateway(ctx, driver.AzureVirtualNetworkGateway{
		Name: "vng1", ResourceGroup: "rg1", Location: "eastus",
		GatewayType: "Vpn", VPNType: "RouteBased", SKUName: "VpnGw1", SKUTier: "VpnGw1",
		BgpSettings: &driver.AzureGatewayBgpSettings{ASN: 65000, BgpPeeringAddress: "10.0.255.254"},
		Tags:        map[string]string{"role": "hub"},
	})

	src.PutAzureLocalNetworkGateway(ctx, driver.AzureLocalNetworkGateway{
		Name: "lng1", ResourceGroup: "rg1", Location: "eastus",
		GatewayIPAddress: "203.0.113.10", AddressPrefixes: []string{"192.168.0.0/16"},
	})

	src.PutAzureVirtualNetworkGatewayConnection(ctx, driver.AzureVirtualNetworkGatewayConnection{
		Name: "conn1", ResourceGroup: "rg1", Location: "eastus",
		ConnectionType:           "IPsec",
		VirtualNetworkGateway1ID: "/subscriptions/sub-1/resourceGroups/rg1/providers/Microsoft.Network/virtualNetworkGateways/vng1",
		LocalNetworkGateway2ID:   "/subscriptions/sub-1/resourceGroups/rg1/providers/Microsoft.Network/localNetworkGateways/lng1",
		SharedKey:                "psk-secret", RoutingWeight: 10,
	})

	// Seed the Private Link surface so the round-trip proves the private endpoint
	// and private link service stores are included in the snapshot dump/restore
	// lists — the test would pass even with a store dropped if nothing referenced it.
	plsID := "/subscriptions/sub-1/resourceGroups/rg1/providers/Microsoft.Network/privateLinkServices/pls1"

	src.PutAzurePrivateEndpoint(ctx, driver.AzurePrivateEndpoint{
		Name: "pe1", ResourceGroup: "rg1", Location: "eastus",
		SubnetID: subnet.ID,
		PrivateLinkServiceConnections: []driver.AzurePrivateLinkServiceConnection{
			{Name: "conn", PrivateLinkServiceID: plsID, GroupIDs: []string{"blob"}, Status: "Approved"},
		},
		Tags: map[string]string{"env": "prod"},
	})

	src.PutAzurePrivateLinkService(ctx, driver.AzurePrivateLinkService{
		Name: "pls1", ResourceGroup: "rg1", Location: "eastus",
		LoadBalancerFrontendIDs: []string{"/subscriptions/sub-1/resourceGroups/rg1/providers/Microsoft.Network/loadBalancers/lb/frontendIPConfigurations/fe"},
		IPConfigurations: []driver.AzurePrivateLinkServiceIPConfiguration{
			{Name: "ipcfg", SubnetID: subnet.ID, PrivateIPAllocationMethod: "Dynamic", Primary: true},
		},
		VisibilitySubscriptions: []string{"sub-1"}, EnableProxyProtocol: true,
	})

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(data2), "snapshot must be stable across restore")

	subnets, err := dst.DescribeSubnets(ctx, []string{subnet.ID})
	require.NoError(t, err)
	require.Len(t, subnets, 1)
	assert.Equal(t, vpcID, subnets[0].VPCID, "subnet keeps its VNet cross-reference")

	sgs, err := dst.DescribeSecurityGroups(ctx, []string{sg.ID})
	require.NoError(t, err)
	require.Len(t, sgs, 1)
	assert.Equal(t, sg.ID, sgs[0].ID)

	// The NIC survives under its ARM key with its subnet cross-reference intact.
	got, err := dst.GetNetworkInterface(ctx, "rg1", "nic1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.IPConfigs, 1)
	assert.Equal(t, subnet.ID, got.IPConfigs[0].SubnetID)
	assert.Equal(t, nic.MACAddress, got.MACAddress)

	// The application security group survives under its ARM addressing pair.
	asg, ok := dst.GetAzureApplicationSecurityGroup(ctx, "rg1", "asg1")
	require.True(t, ok, "ASG must survive snapshot/restore")
	assert.Equal(t, "prod", asg.Tags["env"])

	// The public IP prefix survives with its synthesized CIDR and sku intact.
	pfx, ok := dst.GetAzurePublicIPPrefix(ctx, "rg1", "pfx1")
	require.True(t, ok, "public IP prefix must survive snapshot/restore")
	assert.Equal(t, storedPrefix.IPPrefix, pfx.IPPrefix, "synthesized ipPrefix must round-trip")
	assert.Equal(t, int32(28), pfx.PrefixLength)
	assert.Equal(t, "Standard", pfx.SKUName)
	assert.Equal(t, "net", pfx.Tags["team"])

	// The virtual network gateway survives with its type, sku and BGP settings.
	vng, ok := dst.GetAzureVirtualNetworkGateway(ctx, "rg1", "vng1")
	require.True(t, ok, "virtual network gateway must survive snapshot/restore")
	assert.Equal(t, "Vpn", vng.GatewayType)
	assert.Equal(t, "VpnGw1", vng.SKUName)
	assert.Equal(t, "hub", vng.Tags["role"])
	require.NotNil(t, vng.BgpSettings, "BGP settings must round-trip")
	assert.Equal(t, int64(65000), vng.BgpSettings.ASN)

	// The local network gateway survives with its on-prem IP and address space.
	lng, ok := dst.GetAzureLocalNetworkGateway(ctx, "rg1", "lng1")
	require.True(t, ok, "local network gateway must survive snapshot/restore")
	assert.Equal(t, "203.0.113.10", lng.GatewayIPAddress)
	assert.Equal(t, []string{"192.168.0.0/16"}, lng.AddressPrefixes)

	// The connection survives with its gateway references and shared key.
	conn, ok := dst.GetAzureVirtualNetworkGatewayConnection(ctx, "rg1", "conn1")
	require.True(t, ok, "gateway connection must survive snapshot/restore")
	assert.Equal(t, "IPsec", conn.ConnectionType)
	assert.Equal(t, "psk-secret", conn.SharedKey)
	assert.Contains(t, conn.VirtualNetworkGateway1ID, "virtualNetworkGateways/vng1")
	assert.Contains(t, conn.LocalNetworkGateway2ID, "localNetworkGateways/lng1")

	// The private endpoint survives with its subnet ref and connection.
	pe, ok := dst.GetAzurePrivateEndpoint(ctx, "rg1", "pe1")
	require.True(t, ok, "private endpoint must survive snapshot/restore")
	assert.Equal(t, subnet.ID, pe.SubnetID, "private endpoint keeps its subnet cross-reference")
	assert.Equal(t, "prod", pe.Tags["env"])
	require.Len(t, pe.PrivateLinkServiceConnections, 1)
	assert.Equal(t, plsID, pe.PrivateLinkServiceConnections[0].PrivateLinkServiceID)
	assert.Equal(t, []string{"blob"}, pe.PrivateLinkServiceConnections[0].GroupIDs)

	// The private link service survives with its ip config and visibility.
	pls, ok := dst.GetAzurePrivateLinkService(ctx, "rg1", "pls1")
	require.True(t, ok, "private link service must survive snapshot/restore")
	assert.True(t, pls.EnableProxyProtocol)
	assert.Equal(t, []string{"sub-1"}, pls.VisibilitySubscriptions)
	require.Len(t, pls.IPConfigurations, 1)
	assert.Equal(t, subnet.ID, pls.IPConfigurations[0].SubnetID)
	assert.True(t, pls.IPConfigurations[0].Primary)
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()
	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(data2))
}
