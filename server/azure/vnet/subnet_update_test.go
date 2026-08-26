package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// Finding: a subnet CreateOrUpdate was create-only — re-PUTting an existing
// subnet with a new addressPrefix silently dropped the change (GET returned the
// old CIDR). Real ARM replaces the addressPrefix in place.
func TestSDKSubnetUpdateAddressPrefix(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-cidr", "10.0.0.0/16")

	p, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-cidr", "sn1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	pollDone(t, p)

	p2, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-cidr", "sn1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.5.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("re-PUT subnet with new CIDR: %v", err)
	}

	pollDone(t, p2)

	got, err := subnets.Get(ctx, "rg-1", "vnet-cidr", "sn1", nil)
	if err != nil {
		t.Fatalf("subnet Get: %v", err)
	}

	if got.Properties == nil || got.Properties.AddressPrefix == nil || *got.Properties.AddressPrefix != "10.0.5.0/24" {
		t.Fatalf("subnet addressPrefix after re-PUT = %+v, want 10.0.5.0/24", got.Properties)
	}
}

// Finding: an associated NSG could never be removed — re-PUTting a subnet
// WITHOUT networkSecurityGroup left the association in place. Real ARM's
// CreateOrUpdate is a full replacement: an omitted networkSecurityGroup clears
// the association (the azurerm_subnet_network_security_group_association delete
// path).
func TestSDKSubnetNSGDisassociation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nsgs, _ := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)

	nsgP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-disassoc", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create nsg: %v", err)
	}

	pollDone(t, nsgP)

	nsgID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkSecurityGroups/nsg-disassoc"

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-disassoc", "10.0.0.0/16")

	p, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-disassoc", "sn1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:        to.Ptr("10.0.1.0/24"),
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(nsgID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("associate NSG: %v", err)
	}

	assoc := pollDone(t, p)
	if assoc.Properties == nil || assoc.Properties.NetworkSecurityGroup == nil {
		t.Fatal("subnet did not carry the NSG association after the first PUT")
	}

	// Re-PUT WITHOUT networkSecurityGroup: the association must be cleared.
	p2, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-disassoc", "sn1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("re-PUT subnet without NSG: %v", err)
	}

	pollDone(t, p2)

	got, err := subnets.Get(ctx, "rg-1", "vnet-disassoc", "sn1", nil)
	if err != nil {
		t.Fatalf("subnet Get: %v", err)
	}

	if got.Properties != nil && got.Properties.NetworkSecurityGroup != nil {
		t.Fatalf("subnet NSG after disassociating re-PUT = %+v, want none", got.Properties.NetworkSecurityGroup)
	}
}

// Finding: deleting one side of a two-way VNet peering left the surviving
// reciprocal side stuck reporting Connected. Real ARM transitions the orphaned
// side to Disconnected.
func TestSDKPeeringDeleteDisconnectsReciprocal(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	peerings, _ := armnetwork.NewVirtualNetworkPeeringsClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-a", "10.0.0.0/16")
	createTestVNet(t, ctx, vnets, "rg-1", "vnet-b", "10.1.0.0/16")

	vnetAID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vnet-a"
	vnetBID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vnet-b"

	pa, err := peerings.BeginCreateOrUpdate(ctx, "rg-1", "vnet-a", "a-to-b", armnetwork.VirtualNetworkPeering{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			RemoteVirtualNetwork: &armnetwork.SubResource{ID: to.Ptr(vnetBID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create a-to-b: %v", err)
	}

	pollDone(t, pa)

	pb, err := peerings.BeginCreateOrUpdate(ctx, "rg-1", "vnet-b", "b-to-a", armnetwork.VirtualNetworkPeering{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			RemoteVirtualNetwork: &armnetwork.SubResource{ID: to.Ptr(vnetAID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create b-to-a: %v", err)
	}

	pollDone(t, pb)

	// Delete the b-to-a side; the surviving a-to-b must become Disconnected.
	dp, err := peerings.BeginDelete(ctx, "rg-1", "vnet-b", "b-to-a", nil)
	if err != nil {
		t.Fatalf("delete b-to-a: %v", err)
	}

	pollDone(t, dp)

	aSide, err := peerings.Get(ctx, "rg-1", "vnet-a", "a-to-b", nil)
	if err != nil {
		t.Fatalf("Get a-to-b after reciprocal delete: %v", err)
	}

	if aSide.Properties == nil || aSide.Properties.PeeringState == nil ||
		*aSide.Properties.PeeringState != armnetwork.VirtualNetworkPeeringStateDisconnected {
		t.Fatalf("surviving peering state = %v, want Disconnected", aSide.Properties.PeeringState)
	}
}

// Finding: NSG references were resolved by name only, not resource-group
// scoped — a NIC in rgA could reference an NSG that exists only in rgB and be
// accepted. The reference must resolve within the NSG id's own resource group.
func TestSDKNICNSGCrossRGRejected(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nsgs, _ := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)
	nics, _ := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)

	// The NSG lives only in rgB.
	nsgP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-b", "nsg-x", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create nsg in rg-b: %v", err)
	}

	pollDone(t, nsgP)

	createTestVNet(t, ctx, vnets, "rg-a", "vnet-a", "10.0.0.0/16")

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-a", "vnet-a", "default", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	pollDone(t, subP)

	subnetID := "/subscriptions/sub-1/resourceGroups/rg-a/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-a/subnets/default"

	// The NIC in rg-a references the NSG by its rg-a id, where it does not exist.
	badNSGID := "/subscriptions/sub-1/resourceGroups/rg-a/providers/Microsoft.Network/networkSecurityGroups/nsg-x"

	_, err = nics.BeginCreateOrUpdate(ctx, "rg-a", "nic-x", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(badNSGID)},
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet: &armnetwork.Subnet{ID: to.Ptr(subnetID)},
				},
			}},
		},
	}, nil)
	if err == nil {
		t.Fatal("NIC referencing an NSG absent in its own RG: want error, got nil")
	}

	if got := respStatus(t, err); got != 404 {
		t.Fatalf("cross-RG NSG reference: status = %d, want 404", got)
	}
}

// Companion to TestSDKNICNSGCrossRGRejected: a subnet in rg-a referencing an
// NSG that exists only in rg-b must likewise be rejected as not found.
func TestSDKSubnetNSGCrossRGRejected(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nsgs, _ := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)

	nsgP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-b", "nsg-y", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create nsg in rg-b: %v", err)
	}

	pollDone(t, nsgP)

	createTestVNet(t, ctx, vnets, "rg-a", "vnet-sub", "10.0.0.0/16")

	badNSGID := "/subscriptions/sub-1/resourceGroups/rg-a/providers/Microsoft.Network/networkSecurityGroups/nsg-y"

	_, err = subnets.BeginCreateOrUpdate(ctx, "rg-a", "vnet-sub", "default", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:        to.Ptr("10.0.1.0/24"),
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(badNSGID)},
		},
	}, nil)
	if err == nil {
		t.Fatal("subnet referencing an NSG absent in its own RG: want error, got nil")
	}

	if got := respStatus(t, err); got != 404 {
		t.Fatalf("cross-RG NSG reference: status = %d, want 404", got)
	}
}
