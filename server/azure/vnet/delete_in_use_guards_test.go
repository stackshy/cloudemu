package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// The NSG / route table / NAT gateway delete handlers previously deleted
// unconditionally. Real ARM refuses the delete while the resource is still
// associated with a subnet (or NIC), answering 400 with an InUse* code; the
// association must be dropped first. These tests drive the real armnetwork SDK
// end to end: associate → delete blocked (resource survives) → disassociate →
// delete succeeds, plus an unassociated delete that succeeds directly.

// TestSDKDeleteNSGInUseBySubnetBlocked exercises the NSG delete-in-use guard
// against a subnet association.
func TestSDKDeleteNSGInUseBySubnetBlocked(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nsgs, _ := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-nsg", "10.0.0.0/16")

	nsgP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-guard", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create nsg: %v", err)
	}

	pollDone(t, nsgP)

	nsgID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkSecurityGroups/nsg-guard"

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-nsg", "snet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:        to.Ptr("10.0.1.0/24"),
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(nsgID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("associate subnet to nsg: %v", err)
	}

	pollDone(t, subP)

	// Delete must be refused while the subnet still references the NSG.
	_, err = nsgs.BeginDelete(ctx, "rg-1", "nsg-guard", nil)
	if err == nil {
		t.Fatal("delete NSG in use: want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("delete NSG in use: status = %d, want 400", got)
	}

	// The NSG must still exist, and the subnet must still carry its reference.
	if _, gerr := nsgs.Get(ctx, "rg-1", "nsg-guard", nil); gerr != nil {
		t.Fatalf("NSG should survive a blocked delete: %v", gerr)
	}

	sub, gerr := subnets.Get(ctx, "rg-1", "vnet-nsg", "snet-1", nil)
	if gerr != nil {
		t.Fatalf("get subnet: %v", gerr)
	}

	if sub.Properties == nil || sub.Properties.NetworkSecurityGroup == nil ||
		sub.Properties.NetworkSecurityGroup.ID == nil || *sub.Properties.NetworkSecurityGroup.ID != nsgID {
		t.Fatalf("subnet NSG reference dropped by a blocked delete: %+v", sub.Properties)
	}

	// Disassociate (PUT the subnet without the NSG reference), then delete.
	disP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-nsg", "snet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("disassociate subnet: %v", err)
	}

	pollDone(t, disP)

	delP, err := nsgs.BeginDelete(ctx, "rg-1", "nsg-guard", nil)
	if err != nil {
		t.Fatalf("delete NSG after disassociate: %v", err)
	}

	pollDone(t, delP)

	if _, gerr := nsgs.Get(ctx, "rg-1", "nsg-guard", nil); gerr == nil {
		t.Fatal("NSG should be gone after a successful delete")
	}
}

// TestSDKDeleteNSGUnassociatedSucceeds confirms an unreferenced NSG deletes
// directly.
func TestSDKDeleteNSGUnassociatedSucceeds(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	nsgs, _ := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, clientOpts(ts))

	p, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-free", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create nsg: %v", err)
	}

	pollDone(t, p)

	delP, err := nsgs.BeginDelete(ctx, "rg-1", "nsg-free", nil)
	if err != nil {
		t.Fatalf("delete unassociated NSG: %v", err)
	}

	pollDone(t, delP)
}

// TestSDKDeleteRouteTableInUseBlocked exercises the route table delete-in-use
// guard against a subnet association.
func TestSDKDeleteRouteTableInUseBlocked(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	rts, _ := armnetwork.NewRouteTablesClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-rt", "10.0.0.0/16")

	rtP, err := rts.BeginCreateOrUpdate(ctx, "rg-1", "rt-guard", armnetwork.RouteTable{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create route table: %v", err)
	}

	pollDone(t, rtP)

	rtID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/routeTables/rt-guard"

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-rt", "snet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: to.Ptr("10.0.1.0/24"),
			RouteTable:    &armnetwork.RouteTable{ID: to.Ptr(rtID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("associate subnet to route table: %v", err)
	}

	pollDone(t, subP)

	_, err = rts.BeginDelete(ctx, "rg-1", "rt-guard", nil)
	if err == nil {
		t.Fatal("delete route table in use: want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("delete route table in use: status = %d, want 400", got)
	}

	if _, gerr := rts.Get(ctx, "rg-1", "rt-guard", nil); gerr != nil {
		t.Fatalf("route table should survive a blocked delete: %v", gerr)
	}

	// Disassociate, then delete succeeds.
	disP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-rt", "snet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("disassociate subnet: %v", err)
	}

	pollDone(t, disP)

	delP, err := rts.BeginDelete(ctx, "rg-1", "rt-guard", nil)
	if err != nil {
		t.Fatalf("delete route table after disassociate: %v", err)
	}

	pollDone(t, delP)

	if _, gerr := rts.Get(ctx, "rg-1", "rt-guard", nil); gerr == nil {
		t.Fatal("route table should be gone after a successful delete")
	}
}

// TestSDKDeleteRouteTableUnassociatedSucceeds confirms an unreferenced route
// table deletes directly.
func TestSDKDeleteRouteTableUnassociatedSucceeds(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	rts, _ := armnetwork.NewRouteTablesClient("sub-1", fakeCred{}, clientOpts(ts))

	p, err := rts.BeginCreateOrUpdate(ctx, "rg-1", "rt-free", armnetwork.RouteTable{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create route table: %v", err)
	}

	pollDone(t, p)

	delP, err := rts.BeginDelete(ctx, "rg-1", "rt-free", nil)
	if err != nil {
		t.Fatalf("delete unassociated route table: %v", err)
	}

	pollDone(t, delP)
}

// TestSDKDeleteNATGatewayInUseBlocked exercises the NAT gateway delete-in-use
// guard against a subnet association.
func TestSDKDeleteNATGatewayInUseBlocked(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nats, _ := armnetwork.NewNatGatewaysClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-natgw", "10.0.0.0/16")

	natP, err := nats.BeginCreateOrUpdate(ctx, "rg-1", "natgw-guard", armnetwork.NatGateway{
		Location:   to.Ptr("eastus"),
		SKU:        &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
		Properties: &armnetwork.NatGatewayPropertiesFormat{IdleTimeoutInMinutes: to.Ptr(int32(10))},
	}, nil)
	if err != nil {
		t.Fatalf("create nat gateway: %v", err)
	}

	pollDone(t, natP)

	natID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/natGateways/natgw-guard"

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-natgw", "snet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: to.Ptr("10.0.1.0/24"),
			NatGateway:    &armnetwork.SubResource{ID: to.Ptr(natID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("associate subnet to nat gateway: %v", err)
	}

	pollDone(t, subP)

	_, err = nats.BeginDelete(ctx, "rg-1", "natgw-guard", nil)
	if err == nil {
		t.Fatal("delete nat gateway in use: want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("delete nat gateway in use: status = %d, want 400", got)
	}

	if _, gerr := nats.Get(ctx, "rg-1", "natgw-guard", nil); gerr != nil {
		t.Fatalf("nat gateway should survive a blocked delete: %v", gerr)
	}

	// Disassociate, then delete succeeds.
	disP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-natgw", "snet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("disassociate subnet: %v", err)
	}

	pollDone(t, disP)

	delP, err := nats.BeginDelete(ctx, "rg-1", "natgw-guard", nil)
	if err != nil {
		t.Fatalf("delete nat gateway after disassociate: %v", err)
	}

	pollDone(t, delP)

	if _, gerr := nats.Get(ctx, "rg-1", "natgw-guard", nil); gerr == nil {
		t.Fatal("nat gateway should be gone after a successful delete")
	}
}

// TestSDKDeleteNATGatewayUnassociatedSucceeds confirms an unreferenced NAT
// gateway deletes directly.
func TestSDKDeleteNATGatewayUnassociatedSucceeds(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	nats, _ := armnetwork.NewNatGatewaysClient("sub-1", fakeCred{}, clientOpts(ts))

	p, err := nats.BeginCreateOrUpdate(ctx, "rg-1", "natgw-free", armnetwork.NatGateway{
		Location:   to.Ptr("eastus"),
		SKU:        &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
		Properties: &armnetwork.NatGatewayPropertiesFormat{IdleTimeoutInMinutes: to.Ptr(int32(10))},
	}, nil)
	if err != nil {
		t.Fatalf("create nat gateway: %v", err)
	}

	pollDone(t, p)

	delP, err := nats.BeginDelete(ctx, "rg-1", "natgw-free", nil)
	if err != nil {
		t.Fatalf("delete unassociated nat gateway: %v", err)
	}

	pollDone(t, delP)
}
