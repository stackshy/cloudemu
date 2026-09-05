package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// createVNetForSubnet creates a parent vnet used by the standalone-subnet tests.
func createVNetForSubnet(t *testing.T, vnets *armnetwork.VirtualNetworksClient, name, space string) {
	t.Helper()

	p, err := vnets.BeginCreateOrUpdate(context.Background(), "rg-1", name, armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr(space)}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet %s: %v", name, err)
	}

	pollDone(t, p)
}

// TestSDKStandaloneSubnetAddressPrefixes drives the exact shape the azurerm
// provider sends for azurerm_subnet: the plural addressPrefixes form, with the
// singular addressPrefix left unset. Before the fix the handler read only
// addressPrefix, so validateSubnetCIDR saw an empty CIDR and rejected the PUT —
// azurerm_subnet apply failed outright. The create must succeed and the GET must
// round-trip addressPrefixes.
func TestSDKStandaloneSubnetAddressPrefixes(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	createVNetForSubnet(t, vnets, "vnet-1", "10.0.0.0/16")

	subnets, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	p, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-1", "internal", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefixes: []*string{to.Ptr("10.0.1.0/24")},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet with plural addressPrefixes: %v", err)
	}

	created := pollDone(t, p)
	assertPluralPrefix(t, created.Properties, "10.0.1.0/24")

	got, err := subnets.Get(ctx, "rg-1", "vnet-1", "internal", nil)
	if err != nil {
		t.Fatalf("get subnet: %v", err)
	}

	assertPluralPrefix(t, got.Properties, "10.0.1.0/24")

	// The parent vnet GET must also carry the subnet with its plural prefix.
	vnet, err := vnets.Get(ctx, "rg-1", "vnet-1", nil)
	if err != nil {
		t.Fatalf("get vnet: %v", err)
	}

	if vnet.Properties == nil || len(vnet.Properties.Subnets) != 1 {
		t.Fatalf("vnet should report 1 subnet, got %+v", vnet.Properties)
	}

	assertPluralPrefix(t, vnet.Properties.Subnets[0].Properties, "10.0.1.0/24")
}

// TestSDKInlineSubnetAddressPrefixes drives the shape azurerm_virtual_network
// sends for an inline subnet block: the plural addressPrefixes form. Before the
// fix the whole vnet PUT failed because the inline subnet's CIDR read empty.
func TestSDKInlineSubnetAddressPrefixes(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	p, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-inline", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			Subnets: []*armnetwork.Subnet{
				{
					Name: to.Ptr("web"),
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefixes: []*string{to.Ptr("10.0.1.0/24")},
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet with inline plural subnet: %v", err)
	}

	created := pollDone(t, p)
	if created.Properties == nil || len(created.Properties.Subnets) != 1 {
		t.Fatalf("expected 1 inline subnet, got %+v", created.Properties)
	}

	assertPluralPrefix(t, created.Properties.Subnets[0].Properties, "10.0.1.0/24")

	subnets, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	got, err := subnets.Get(ctx, "rg-1", "vnet-inline", "web", nil)
	if err != nil {
		t.Fatalf("get inline subnet: %v", err)
	}

	assertPluralPrefix(t, got.Properties, "10.0.1.0/24")
}

// TestSDKSubnetListScopedToVNet confirms SubnetsClient.List returns only the
// queried virtual network's subnets. Before the fix the handler returned every
// subnet in the subscription, re-scoped under the requested vnet — a paging
// client would see phantom subnets from unrelated vnets.
func TestSDKSubnetListScopedToVNet(t *testing.T) {
	ts := newVNetServer(t)

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	subnets, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	createVNetForSubnet(t, vnets, "vnet-a", "10.0.0.0/16")
	createVNetForSubnet(t, vnets, "vnet-b", "10.1.0.0/16")

	putSubnet(t, subnets, "vnet-a", "a-sub", "10.0.1.0/24")
	putSubnet(t, subnets, "vnet-b", "b-sub1", "10.1.1.0/24")
	putSubnet(t, subnets, "vnet-b", "b-sub2", "10.1.2.0/24")

	names := listSubnetNames(t, subnets, "vnet-a")
	if len(names) != 1 || names["a-sub"] == 0 {
		t.Fatalf("vnet-a should list exactly its own subnet [a-sub], got %v", names)
	}

	names = listSubnetNames(t, subnets, "vnet-b")
	if len(names) != 2 || names["b-sub1"] == 0 || names["b-sub2"] == 0 {
		t.Fatalf("vnet-b should list exactly [b-sub1 b-sub2], got %v", names)
	}
}

func putSubnet(t *testing.T, subnets *armnetwork.SubnetsClient, vnet, name, cidr string) {
	t.Helper()

	p, err := subnets.BeginCreateOrUpdate(context.Background(), "rg-1", vnet, name, armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefixes: []*string{to.Ptr(cidr)}},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet %s/%s: %v", vnet, name, err)
	}

	pollDone(t, p)
}

func listSubnetNames(t *testing.T, subnets *armnetwork.SubnetsClient, vnet string) map[string]int {
	t.Helper()

	names := map[string]int{}

	pager := subnets.NewListPager("rg-1", vnet, nil)
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		if err != nil {
			t.Fatalf("list subnets %s: %v", vnet, err)
		}

		for _, s := range page.Value {
			names[*s.Name]++
		}
	}

	return names
}

func assertPluralPrefix(t *testing.T, props *armnetwork.SubnetPropertiesFormat, want string) {
	t.Helper()

	if props == nil {
		t.Fatal("subnet properties nil")
	}

	if len(props.AddressPrefixes) != 1 || props.AddressPrefixes[0] == nil || *props.AddressPrefixes[0] != want {
		t.Fatalf("addressPrefixes = %v, want [%s]", props.AddressPrefixes, want)
	}
}
