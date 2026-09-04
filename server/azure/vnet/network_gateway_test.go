package vnet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKNetworkGatewaysRoundTrip drives the real armnetwork gateway clients
// through their Begin* pollers end-to-end: it stands up the prerequisites (a
// public IP + a GatewaySubnet), then creates a virtual network gateway, a local
// network gateway and an IPsec connection referencing both, and asserts get/list
// round-trip them and delete makes a subsequent get 404. Every op used to fall
// through to the vnet 501 default. The key regression it guards is the create
// LRO: the pollers must complete rather than hang.
func TestSDKNetworkGatewaysRoundTrip(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	const rg = "rg-gw"

	subnetID := seedGatewayPrerequisites(t, ctx, opts, rg)

	vngClient, err := armnetwork.NewVirtualNetworkGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pipID := "/subscriptions/sub-1/resourceGroups/" + rg + "/providers/Microsoft.Network/publicIPAddresses/gw-pip"

	vngCreate, err := vngClient.BeginCreateOrUpdate(ctx, rg, "vng-1", armnetwork.VirtualNetworkGateway{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkGatewayPropertiesFormat{
			GatewayType: to.Ptr(armnetwork.VirtualNetworkGatewayTypeVPN),
			VPNType:     to.Ptr(armnetwork.VPNTypeRouteBased),
			SKU: &armnetwork.VirtualNetworkGatewaySKU{
				Name: to.Ptr(armnetwork.VirtualNetworkGatewaySKUNameVPNGw1),
				Tier: to.Ptr(armnetwork.VirtualNetworkGatewaySKUTierVPNGw1),
			},
			EnableBgp: to.Ptr(false),
			IPConfigurations: []*armnetwork.VirtualNetworkGatewayIPConfiguration{
				{
					Name: to.Ptr("gwipconfig"),
					Properties: &armnetwork.VirtualNetworkGatewayIPConfigurationPropertiesFormat{
						PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
						Subnet:                    &armnetwork.SubResource{ID: to.Ptr(subnetID)},
						PublicIPAddress:           &armnetwork.SubResource{ID: to.Ptr(pipID)},
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("vng BeginCreateOrUpdate: %v", err)
	}

	vng := pollDone(t, vngCreate)

	if vng.Name == nil || *vng.Name != "vng-1" {
		t.Fatalf("vng name=%v want vng-1", vng.Name)
	}

	if vng.Properties == nil || vng.Properties.GatewayType == nil ||
		*vng.Properties.GatewayType != armnetwork.VirtualNetworkGatewayTypeVPN {
		t.Errorf("vng gatewayType=%v want Vpn", vng.Properties)
	}

	if vng.Properties.ProvisioningState == nil ||
		*vng.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Errorf("vng provisioningState=%v want Succeeded", vng.Properties.ProvisioningState)
	}

	if len(vng.Properties.IPConfigurations) != 1 ||
		vng.Properties.IPConfigurations[0].Properties.Subnet == nil ||
		*vng.Properties.IPConfigurations[0].Properties.Subnet.ID != subnetID {
		t.Errorf("vng ipConfigurations=%+v want subnet %s", vng.Properties.IPConfigurations, subnetID)
	}

	vngID := *vng.ID

	// Local network gateway (the on-prem side).
	lngClient, err := armnetwork.NewLocalNetworkGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	lngCreate, err := lngClient.BeginCreateOrUpdate(ctx, rg, "lng-1", armnetwork.LocalNetworkGateway{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LocalNetworkGatewayPropertiesFormat{
			GatewayIPAddress:         to.Ptr("203.0.113.10"),
			LocalNetworkAddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("192.168.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("lng BeginCreateOrUpdate: %v", err)
	}

	lng := pollDone(t, lngCreate)

	if lng.Properties == nil || lng.Properties.GatewayIPAddress == nil ||
		*lng.Properties.GatewayIPAddress != "203.0.113.10" {
		t.Errorf("lng gatewayIpAddress=%v want 203.0.113.10", lng.Properties)
	}

	if len(lng.Properties.LocalNetworkAddressSpace.AddressPrefixes) != 1 ||
		*lng.Properties.LocalNetworkAddressSpace.AddressPrefixes[0] != "192.168.0.0/16" {
		t.Errorf("lng addressPrefixes=%+v want [192.168.0.0/16]", lng.Properties.LocalNetworkAddressSpace)
	}

	lngID := *lng.ID

	// Connection joining the two gateways.
	connClient, err := armnetwork.NewVirtualNetworkGatewayConnectionsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	connCreate, err := connClient.BeginCreateOrUpdate(ctx, rg, "conn-1", armnetwork.VirtualNetworkGatewayConnection{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkGatewayConnectionPropertiesFormat{
			ConnectionType:         to.Ptr(armnetwork.VirtualNetworkGatewayConnectionTypeIPsec),
			VirtualNetworkGateway1: &armnetwork.VirtualNetworkGateway{ID: to.Ptr(vngID)},
			LocalNetworkGateway2:   &armnetwork.LocalNetworkGateway{ID: to.Ptr(lngID)},
			SharedKey:              to.Ptr("s3cr3t-psk"),
			RoutingWeight:          to.Ptr(int32(10)),
		},
	}, nil)
	if err != nil {
		t.Fatalf("conn BeginCreateOrUpdate: %v", err)
	}

	conn := pollDone(t, connCreate)

	if conn.Properties == nil || conn.Properties.ConnectionType == nil ||
		*conn.Properties.ConnectionType != armnetwork.VirtualNetworkGatewayConnectionTypeIPsec {
		t.Errorf("conn connectionType=%v want IPsec", conn.Properties)
	}

	if conn.Properties.VirtualNetworkGateway1 == nil || conn.Properties.VirtualNetworkGateway1.ID == nil ||
		*conn.Properties.VirtualNetworkGateway1.ID != vngID {
		t.Errorf("conn virtualNetworkGateway1=%v want %s", conn.Properties.VirtualNetworkGateway1, vngID)
	}

	if conn.Properties.LocalNetworkGateway2 == nil || conn.Properties.LocalNetworkGateway2.ID == nil ||
		*conn.Properties.LocalNetworkGateway2.ID != lngID {
		t.Errorf("conn localNetworkGateway2=%v want %s", conn.Properties.LocalNetworkGateway2, lngID)
	}

	assertGatewayGetList(t, ctx, vngClient, lngClient, connClient, rg)
	assertGatewayDelete(t, ctx, vngClient, lngClient, connClient, rg)
}

// seedGatewayPrerequisites creates the public IP and the GatewaySubnet a virtual
// network gateway's ipConfiguration references, returning the subnet id.
func seedGatewayPrerequisites(t *testing.T, ctx context.Context, opts *arm.ClientOptions, rg string) string {
	t.Helper()

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pipCreate, err := pips.BeginCreateOrUpdate(ctx, rg, "gw-pip", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create public IP: %v", err)
	}

	pollDone(t, pipCreate)

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	vnetCreate, err := vnets.BeginCreateOrUpdate(ctx, rg, "vnet-gw", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.10.0.0/16")}},
			Subnets: []*armnetwork.Subnet{
				{
					Name:       to.Ptr("GatewaySubnet"),
					Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.10.255.0/27")},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet: %v", err)
	}

	pollDone(t, vnetCreate)

	return "/subscriptions/sub-1/resourceGroups/" + rg +
		"/providers/Microsoft.Network/virtualNetworks/vnet-gw/subnets/GatewaySubnet"
}

// assertGatewayGetList checks Get and the list pagers return each resource.
func assertGatewayGetList(
	t *testing.T, ctx context.Context,
	vng *armnetwork.VirtualNetworkGatewaysClient,
	lng *armnetwork.LocalNetworkGatewaysClient,
	conn *armnetwork.VirtualNetworkGatewayConnectionsClient,
	rg string,
) {
	t.Helper()

	if _, err := vng.Get(ctx, rg, "vng-1", nil); err != nil {
		t.Errorf("vng Get: %v", err)
	}

	if _, err := lng.Get(ctx, rg, "lng-1", nil); err != nil {
		t.Errorf("lng Get: %v", err)
	}

	if _, err := conn.Get(ctx, rg, "conn-1", nil); err != nil {
		t.Errorf("conn Get: %v", err)
	}

	if got := listVNGNames(t, ctx, vng, rg); len(got) != 1 || got[0] != "vng-1" {
		t.Errorf("vng list=%v want [vng-1]", got)
	}

	lngPager := lng.NewListPager(rg, nil)
	if got := drainNames(t, ctx, lngPager.More, func() ([]*string, error) {
		p, err := lngPager.NextPage(ctx)
		return lngNames(p.Value), err
	}); len(got) != 1 || *got[0] != "lng-1" {
		t.Errorf("lng list=%v want [lng-1]", derefAll(got))
	}

	connPager := conn.NewListPager(rg, nil)
	if got := drainNames(t, ctx, connPager.More, func() ([]*string, error) {
		p, err := connPager.NextPage(ctx)
		return connNames(p.Value), err
	}); len(got) != 1 || *got[0] != "conn-1" {
		t.Errorf("conn list=%v want [conn-1]", derefAll(got))
	}
}

// assertGatewayDelete deletes all three resources and confirms a follow-up Get 404s.
func assertGatewayDelete(
	t *testing.T, ctx context.Context,
	vng *armnetwork.VirtualNetworkGatewaysClient,
	lng *armnetwork.LocalNetworkGatewaysClient,
	conn *armnetwork.VirtualNetworkGatewayConnectionsClient,
	rg string,
) {
	t.Helper()

	connDelete, err := conn.BeginDelete(ctx, rg, "conn-1", nil)
	if err != nil {
		t.Fatalf("conn BeginDelete: %v", err)
	}

	pollDone(t, connDelete)

	vngDelete, err := vng.BeginDelete(ctx, rg, "vng-1", nil)
	if err != nil {
		t.Fatalf("vng BeginDelete: %v", err)
	}

	pollDone(t, vngDelete)

	lngDelete, err := lng.BeginDelete(ctx, rg, "lng-1", nil)
	if err != nil {
		t.Fatalf("lng BeginDelete: %v", err)
	}

	pollDone(t, lngDelete)

	_, err = vng.Get(ctx, rg, "vng-1", nil)
	assertNotFound(t, err, "vng Get after delete")

	_, err = lng.Get(ctx, rg, "lng-1", nil)
	assertNotFound(t, err, "lng Get after delete")

	_, err = conn.Get(ctx, rg, "conn-1", nil)
	assertNotFound(t, err, "conn Get after delete")
}

func listVNGNames(
	t *testing.T, ctx context.Context, client *armnetwork.VirtualNetworkGatewaysClient, rg string,
) []string {
	t.Helper()

	pager := client.NewListPager(rg, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("vng list: %v", err)
		}

		for _, g := range page.Value {
			if g.Name != nil {
				names = append(names, *g.Name)
			}
		}
	}

	return names
}

func drainNames(t *testing.T, _ context.Context, more func() bool, next func() ([]*string, error)) []*string {
	t.Helper()

	var out []*string

	for more() {
		names, err := next()
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		out = append(out, names...)
	}

	return out
}

func lngNames(in []*armnetwork.LocalNetworkGateway) []*string {
	out := make([]*string, 0, len(in))
	for _, g := range in {
		out = append(out, g.Name)
	}

	return out
}

func connNames(in []*armnetwork.VirtualNetworkGatewayConnection) []*string {
	out := make([]*string, 0, len(in))
	for _, c := range in {
		out = append(out, c.Name)
	}

	return out
}

func derefAll(in []*string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != nil {
			out = append(out, *s)
		}
	}

	return out
}

func assertNotFound(t *testing.T, err error, what string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: want error, got nil", what)
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Errorf("%s: want 404, got %v", what, err)
	}
}
