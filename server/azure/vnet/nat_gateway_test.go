package vnet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKNATGatewayRoundTrip guards the Microsoft.Network/natGateways wire
// handler: every op used to 501. This drives the real armnetwork
// NatGatewaysClient through create (bound to a public IP), get, subnet
// association (via the subnet's own natGateway property, the real ARM
// mechanism), and delete — checking both the subnets and publicIpAddresses
// back-references round-trip.
func TestSDKNATGatewayRoundTrip(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pp, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-nat", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
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

	natClient, err := armnetwork.NewNatGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	np, err := natClient.BeginCreateOrUpdate(ctx, "rg-1", "natgw-1", armnetwork.NatGateway{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
		Properties: &armnetwork.NatGatewayPropertiesFormat{
			IdleTimeoutInMinutes: to.Ptr(int32(10)),
			PublicIPAddresses:    []*armnetwork.SubResource{{ID: to.Ptr(pipID)}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created := pollDone(t, np)

	if created.Name == nil || *created.Name != "natgw-1" {
		t.Fatalf("name=%v want natgw-1", created.Name)
	}

	if created.Properties == nil || len(created.Properties.PublicIPAddresses) != 1 ||
		created.Properties.PublicIPAddresses[0].ID == nil || *created.Properties.PublicIPAddresses[0].ID != pipID {
		t.Errorf("publicIpAddresses=%v want [%s]", created.Properties, pipID)
	}

	// GET round-trips the public IP association.
	got, err := natClient.Get(ctx, "rg-1", "natgw-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || len(got.Properties.PublicIPAddresses) != 1 {
		t.Fatalf("Get publicIpAddresses=%v want 1 entry", got.Properties)
	}

	// Attach a subnet the real ARM way: PUT the subnet with its natGateway
	// property set, not the NAT gateway itself.
	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	vp, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-nat", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet: %v", err)
	}

	pollDone(t, vp)

	subnets, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	sp, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-nat", "subnet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	pollDone(t, sp)

	natGatewayID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/natGateways/natgw-1"

	sp2, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-nat", "subnet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: to.Ptr("10.0.1.0/24"),
			NatGateway:    &armnetwork.SubResource{ID: to.Ptr(natGatewayID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("associate subnet to natgw: %v", err)
	}

	associated := pollDone(t, sp2)

	if associated.Properties == nil || associated.Properties.NatGateway == nil ||
		associated.Properties.NatGateway.ID == nil || *associated.Properties.NatGateway.ID != natGatewayID {
		t.Errorf("subnet natGateway=%v want %s", associated.Properties, natGatewayID)
	}

	// The NAT gateway's own GET must now report the subnet back-reference.
	got, err = natClient.Get(ctx, "rg-1", "natgw-1", nil)
	if err != nil {
		t.Fatalf("Get after subnet association: %v", err)
	}

	subnetID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-nat/subnets/subnet-1"

	if got.Properties == nil || len(got.Properties.Subnets) != 1 ||
		got.Properties.Subnets[0].ID == nil || *got.Properties.Subnets[0].ID != subnetID {
		t.Errorf("natgw subnets=%v want [%s]", got.Properties, subnetID)
	}

	// Delete frees the bound public IP.
	dp, err := natClient.BeginDelete(ctx, "rg-1", "natgw-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	pollDone(t, dp)

	if _, err = natClient.Get(ctx, "rg-1", "natgw-1", nil); err == nil {
		t.Error("Get after delete succeeded, want NotFound")
	}

	dpip, err := pips.BeginDelete(ctx, "rg-1", "pip-nat", nil)
	if err != nil {
		t.Fatalf("delete freed public IP: %v", err)
	}

	pollDone(t, dpip)
}

// TestSDKNATGatewayPublicIPAlreadyBound guards the atomic association check: a
// public IP already bound to one NAT gateway is rejected on a second.
func TestSDKNATGatewayPublicIPAlreadyBound(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pp, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-shared-nat", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create pip: %v", err)
	}

	pollDone(t, pp)

	pipID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPAddresses/pip-shared-nat"

	natClient, err := armnetwork.NewNatGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	body := armnetwork.NatGateway{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.NatGatewayPropertiesFormat{
			PublicIPAddresses: []*armnetwork.SubResource{{ID: to.Ptr(pipID)}},
		},
	}

	np, err := natClient.BeginCreateOrUpdate(ctx, "rg-1", "natgw-first", body, nil)
	if err != nil {
		t.Fatalf("first natgw create: %v", err)
	}

	pollDone(t, np)

	_, err = natClient.BeginCreateOrUpdate(ctx, "rg-1", "natgw-second", body, nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("second natgw bound to the same public IP: got %v, want an ARM error", err)
	}
}

// TestSDKNATGatewayResourceGroupIsolation guards the same RG-isolation fix
// applied to NAT gateways: two resource groups may each have a NAT gateway
// named identically, and a resource-group-scoped List must only see its own.
func TestSDKNATGatewayResourceGroupIsolation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	natClient, err := armnetwork.NewNatGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	for _, rg := range []string{"rg-a", "rg-b"} {
		np, cErr := natClient.BeginCreateOrUpdate(ctx, rg, "natgw-shared", armnetwork.NatGateway{
			Location: to.Ptr("eastus"),
		}, nil)
		if cErr != nil {
			t.Fatalf("create in %s: %v", rg, cErr)
		}

		pollDone(t, np)
	}

	if _, err = natClient.Get(ctx, "rg-a", "natgw-shared", nil); err != nil {
		t.Fatalf("get rg-a: %v", err)
	}

	if _, err = natClient.Get(ctx, "rg-b", "natgw-shared", nil); err != nil {
		t.Fatalf("get rg-b: %v", err)
	}

	count := 0

	pager := natClient.NewListPager("rg-a", nil)
	for pager.More() {
		page, pErr := pager.NextPage(ctx)
		if pErr != nil {
			t.Fatalf("list rg-a: %v", pErr)
		}

		count += len(page.Value)
	}

	if count != 1 {
		t.Errorf("List(rg-a) returned %d NAT gateways, want 1 (rg-b's must not leak in)", count)
	}
}
