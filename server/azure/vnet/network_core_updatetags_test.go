package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// The core Azure VNet-family resources (virtualNetworks, networkSecurityGroups,
// routeTables, publicIPAddresses, natGateways, networkInterfaces) each expose a
// synchronous armnetwork *Client.UpdateTags PATCH. Resource-level UpdateTags
// REPLACES the tag collection wholesale (it does not merge): the supplied tag
// becomes the only tag, the create-time tag is gone, the resource's other
// properties survive, the full resource is returned, a raw tags:{} PATCH wipes
// every tag, and UpdateTags on a missing resource is a 404. (subnets are not
// independently taggable in Azure, so they have no UpdateTags path.)

func TestSDKVirtualNetworkUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	client, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	cp, err := client.BeginCreateOrUpdate(ctx, "rg-1", "vnet-tag", armnetwork.VirtualNetwork{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pollDone(t, cp)

	updated, err := client.UpdateTags(ctx, "rg-1", "vnet-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	assertTag(t, updated.Tags, "team", "net")
	assertNoTag(t, updated.Tags, "env")
	assertTagCount(t, updated.Tags, 1)

	if updated.Location == nil || *updated.Location != "westus2" {
		t.Errorf("location=%v want westus2 (property must survive UpdateTags)", updated.Location)
	}

	if updated.Properties == nil || updated.Properties.AddressSpace == nil ||
		len(updated.Properties.AddressSpace.AddressPrefixes) != 1 ||
		*updated.Properties.AddressSpace.AddressPrefixes[0] != "10.0.0.0/16" {
		t.Errorf("addressSpace=%v want [10.0.0.0/16] (property must survive UpdateTags)", updated.Properties)
	}

	got, err := client.Get(ctx, "rg-1", "vnet-tag", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertTag(t, got.Tags, "team", "net")
	assertNoTag(t, got.Tags, "env")

	wiped := rawTagsPatch(t, ts,
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vnet-tag",
		`{"tags":{}}`)
	assertTagCount(t, ptrTags(wiped), 0)

	_, err = client.UpdateTags(ctx, "rg-1", "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "virtual network UpdateTags on missing")
}

func TestSDKNetworkSecurityGroupUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	client, err := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	cp, err := client.BeginCreateOrUpdate(ctx, "rg-1", "nsg-tag", armnetwork.SecurityGroup{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pollDone(t, cp)

	updated, err := client.UpdateTags(ctx, "rg-1", "nsg-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	assertTag(t, updated.Tags, "team", "net")
	assertNoTag(t, updated.Tags, "env")
	assertTagCount(t, updated.Tags, 1)

	if updated.Location == nil || *updated.Location != "westus2" {
		t.Errorf("location=%v want westus2 (property must survive UpdateTags)", updated.Location)
	}

	wiped := rawTagsPatch(t, ts,
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkSecurityGroups/nsg-tag",
		`{"tags":{}}`)
	assertTagCount(t, ptrTags(wiped), 0)

	_, err = client.UpdateTags(ctx, "rg-1", "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "network security group UpdateTags on missing")
}

func TestSDKRouteTableUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	client, err := armnetwork.NewRouteTablesClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	cp, err := client.BeginCreateOrUpdate(ctx, "rg-1", "rt-tag", armnetwork.RouteTable{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.RouteTablePropertiesFormat{
			Routes: []*armnetwork.Route{{
				Name: to.Ptr("r1"),
				Properties: &armnetwork.RoutePropertiesFormat{
					AddressPrefix: to.Ptr("10.1.0.0/16"),
					NextHopType:   to.Ptr(armnetwork.RouteNextHopTypeVnetLocal),
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pollDone(t, cp)

	updated, err := client.UpdateTags(ctx, "rg-1", "rt-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	assertTag(t, updated.Tags, "team", "net")
	assertNoTag(t, updated.Tags, "env")
	assertTagCount(t, updated.Tags, 1)

	if updated.Properties == nil || len(updated.Properties.Routes) != 1 ||
		updated.Properties.Routes[0].Properties == nil ||
		updated.Properties.Routes[0].Properties.AddressPrefix == nil ||
		*updated.Properties.Routes[0].Properties.AddressPrefix != "10.1.0.0/16" {
		t.Errorf("routes=%v want one 10.1.0.0/16 route (property must survive UpdateTags)", updated.Properties)
	}

	wiped := rawTagsPatch(t, ts,
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/routeTables/rt-tag",
		`{"tags":{}}`)
	assertTagCount(t, ptrTags(wiped), 0)

	_, err = client.UpdateTags(ctx, "rg-1", "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "route table UpdateTags on missing")
}

func TestSDKPublicIPAddressUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	client, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	cp, err := client.BeginCreateOrUpdate(ctx, "rg-1", "pip-tag", armnetwork.PublicIPAddress{
		Location: to.Ptr("westus2"),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pollDone(t, cp)

	updated, err := client.UpdateTags(ctx, "rg-1", "pip-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	assertTag(t, updated.Tags, "team", "net")
	assertNoTag(t, updated.Tags, "env")
	assertTagCount(t, updated.Tags, 1)

	if updated.SKU == nil || updated.SKU.Name == nil ||
		*updated.SKU.Name != armnetwork.PublicIPAddressSKUNameStandard {
		t.Errorf("sku=%v want Standard (property must survive UpdateTags)", updated.SKU)
	}

	if updated.Properties == nil || updated.Properties.PublicIPAllocationMethod == nil ||
		*updated.Properties.PublicIPAllocationMethod != armnetwork.IPAllocationMethodStatic {
		t.Errorf("allocationMethod=%v want Static (property must survive UpdateTags)", updated.Properties)
	}

	wiped := rawTagsPatch(t, ts,
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPAddresses/pip-tag",
		`{"tags":{}}`)
	assertTagCount(t, ptrTags(wiped), 0)

	_, err = client.UpdateTags(ctx, "rg-1", "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "public IP address UpdateTags on missing")
}

func TestSDKNatGatewayUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pp, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-nat", armnetwork.PublicIPAddress{
		Location: to.Ptr("westus2"),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create pip: %v", err)
	}

	pollDone(t, pp)

	pipID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPAddresses/pip-nat"

	client, err := armnetwork.NewNatGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	np, err := client.BeginCreateOrUpdate(ctx, "rg-1", "nat-tag", armnetwork.NatGateway{
		Location: to.Ptr("westus2"),
		SKU:      &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.NatGatewayPropertiesFormat{
			PublicIPAddresses: []*armnetwork.SubResource{{ID: to.Ptr(pipID)}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pollDone(t, np)

	updated, err := client.UpdateTags(ctx, "rg-1", "nat-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	assertTag(t, updated.Tags, "team", "net")
	assertNoTag(t, updated.Tags, "env")
	assertTagCount(t, updated.Tags, 1)

	// The bound public IP survives the tag replacement.
	if updated.Properties == nil || len(updated.Properties.PublicIPAddresses) != 1 ||
		updated.Properties.PublicIPAddresses[0].ID == nil ||
		*updated.Properties.PublicIPAddresses[0].ID != pipID {
		t.Errorf("publicIpAddresses=%v want [%s] (property must survive UpdateTags)", updated.Properties, pipID)
	}

	wiped := rawTagsPatch(t, ts,
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/natGateways/nat-tag",
		`{"tags":{}}`)
	assertTagCount(t, ptrTags(wiped), 0)

	_, err = client.UpdateTags(ctx, "rg-1", "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "NAT gateway UpdateTags on missing")
}

func TestSDKNetworkInterfaceUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	subnetID := seedNICSubnet(t, ctx, opts)

	client, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	np, err := client.BeginCreateOrUpdate(ctx, "rg-1", "nic-tag", armnetwork.Interface{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet:                    &armnetwork.Subnet{ID: to.Ptr(subnetID)},
					PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
					Primary:                   to.Ptr(true),
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pollDone(t, np)

	updated, err := client.UpdateTags(ctx, "rg-1", "nic-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	assertTag(t, updated.Tags, "team", "net")
	assertNoTag(t, updated.Tags, "env")
	assertTagCount(t, updated.Tags, 1)

	// The ipConfiguration (subnet binding) survives the tag replacement.
	if updated.Properties == nil || len(updated.Properties.IPConfigurations) != 1 ||
		updated.Properties.IPConfigurations[0].Properties == nil ||
		updated.Properties.IPConfigurations[0].Properties.Subnet == nil ||
		updated.Properties.IPConfigurations[0].Properties.Subnet.ID == nil ||
		*updated.Properties.IPConfigurations[0].Properties.Subnet.ID != subnetID {
		t.Errorf("ipConfigurations=%v want subnet %s (property must survive UpdateTags)", updated.Properties, subnetID)
	}

	wiped := rawTagsPatch(t, ts,
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkInterfaces/nic-tag",
		`{"tags":{}}`)
	assertTagCount(t, ptrTags(wiped), 0)

	_, err = client.UpdateTags(ctx, "rg-1", "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "network interface UpdateTags on missing")
}

// seedNICSubnet creates a vnet + subnet a NIC can bind to and returns the
// subnet's ARM resource id.
func seedNICSubnet(t *testing.T, ctx context.Context, opts *arm.ClientOptions) string {
	t.Helper()

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	vp, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-nic", armnetwork.VirtualNetwork{
		Location: to.Ptr("westus2"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("vnet create: %v", err)
	}

	pollDone(t, vp)

	subnets, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	sp, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-nic", "subnet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("subnet create: %v", err)
	}

	pollDone(t, sp)

	return "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-nic/subnets/subnet-1"
}
