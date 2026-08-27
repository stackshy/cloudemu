package vnet_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

const rtSubID = "sub-1"

func routeTableARMID(rg, name string) string {
	return "/subscriptions/" + rtSubID + "/resourceGroups/" + rg +
		"/providers/Microsoft.Network/routeTables/" + name
}

// TestSDKRouteTableRoundTrip drives the real armnetwork RouteTablesClient
// through create (with two routes), get (routes echoed) and list — the
// Microsoft.Network/routeTables wire handler that used to 501. Cases (a) + (e).
func TestSDKRouteTableRoundTrip(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	client, err := armnetwork.NewRouteTablesClient(rtSubID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	cp, err := client.BeginCreateOrUpdate(ctx, "rg-1", "rt-1", armnetwork.RouteTable{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.RouteTablePropertiesFormat{
			Routes: []*armnetwork.Route{
				{
					Name: to.Ptr("to-appliance"),
					Properties: &armnetwork.RoutePropertiesFormat{
						AddressPrefix:    to.Ptr("10.1.0.0/16"),
						NextHopType:      to.Ptr(armnetwork.RouteNextHopTypeVirtualAppliance),
						NextHopIPAddress: to.Ptr("10.0.0.4"),
					},
				},
				{
					Name: to.Ptr("to-internet"),
					Properties: &armnetwork.RoutePropertiesFormat{
						AddressPrefix: to.Ptr("0.0.0.0/0"),
						NextHopType:   to.Ptr(armnetwork.RouteNextHopTypeInternet),
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	pollDone(t, cp)

	got, err := client.Get(ctx, "rg-1", "rt-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertRoutesRoundTrip(t, got.Properties)

	// (e) list route tables in the resource group.
	pager := client.NewListPager("rg-1", nil)

	var names []string

	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list: %v", perr)
		}

		for _, rt := range page.Value {
			names = append(names, *rt.Name)
		}
	}

	if len(names) != 1 || names[0] != "rt-1" {
		t.Fatalf("list names=%v want [rt-1]", names)
	}
}

func assertRoutesRoundTrip(t *testing.T, props *armnetwork.RouteTablePropertiesFormat) {
	t.Helper()

	if props == nil || len(props.Routes) != 2 {
		t.Fatalf("routes=%v want 2 entries", props)
	}

	byName := map[string]*armnetwork.RoutePropertiesFormat{}
	for _, r := range props.Routes {
		byName[*r.Name] = r.Properties
	}

	appl := byName["to-appliance"]
	if appl == nil || *appl.AddressPrefix != "10.1.0.0/16" ||
		*appl.NextHopType != armnetwork.RouteNextHopTypeVirtualAppliance || *appl.NextHopIPAddress != "10.0.0.4" {
		t.Errorf("to-appliance route=%+v", appl)
	}

	inet := byName["to-internet"]
	if inet == nil || *inet.AddressPrefix != "0.0.0.0/0" || *inet.NextHopType != armnetwork.RouteNextHopTypeInternet {
		t.Errorf("to-internet route=%+v", inet)
	}
}

// TestSDKSubnetRouteTableAssociation drives azurerm_subnet_route_table_association:
// a subnet PUT carrying a routeTable id reflects it on GET (the route table
// staying resolvable), and a subsequent PUT omitting it clears the association.
// Cases (b) + (c).
func TestSDKSubnetRouteTableAssociation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	rtID := routeTableARMID("rg-1", "rt-assoc")
	createRouteTable(t, ts, "rg-1", "rt-assoc")
	createVNet(t, ts, "rg-1", "vnet-1", "10.0.0.0/16")

	subnets, err := armnetwork.NewSubnetsClient(rtSubID, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	// (b) associate the route table via the subnet's own routeTable property.
	sp, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-1", "snet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: to.Ptr("10.0.1.0/24"),
			RouteTable:    &armnetwork.RouteTable{ID: to.Ptr(rtID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("subnet create: %v", err)
	}

	pollDone(t, sp)

	got, err := subnets.Get(ctx, "rg-1", "vnet-1", "snet-1", nil)
	if err != nil {
		t.Fatalf("subnet get: %v", err)
	}

	if got.Properties == nil || got.Properties.RouteTable == nil || *got.Properties.RouteTable.ID != rtID {
		t.Fatalf("subnet routeTable=%v want %s", got.Properties, rtID)
	}

	// Route table still resolvable after the association.
	rtClient, _ := armnetwork.NewRouteTablesClient(rtSubID, fakeCred{}, opts)
	if _, gerr := rtClient.Get(ctx, "rg-1", "rt-assoc", nil); gerr != nil {
		t.Fatalf("route table Get after assoc: %v", gerr)
	}

	// (c) disassociate: PUT the subnet again without a routeTable clears it.
	cp, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-1", "snet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: to.Ptr("10.0.1.0/24"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("subnet update: %v", err)
	}

	pollDone(t, cp)

	cleared, err := subnets.Get(ctx, "rg-1", "vnet-1", "snet-1", nil)
	if err != nil {
		t.Fatalf("subnet get after clear: %v", err)
	}

	if cleared.Properties != nil && cleared.Properties.RouteTable != nil {
		t.Fatalf("routeTable still present after clear: %v", cleared.Properties.RouteTable)
	}
}

// TestSDKSubnetRouteTableNotFound checks that a subnet referencing a route table
// that does not exist is rejected (the NSG-analog 404 ResourceNotFound). Case (d).
func TestSDKSubnetRouteTableNotFound(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	createVNet(t, ts, "rg-1", "vnet-nf", "10.0.0.0/16")

	subnets, err := armnetwork.NewSubnetsClient(rtSubID, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	_, err = subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-nf", "snet-nf", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: to.Ptr("10.0.1.0/24"),
			RouteTable:    &armnetwork.RouteTable{ID: to.Ptr(routeTableARMID("rg-1", "ghost"))},
		},
	}, nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("want 404 for missing route table, got %v", err)
	}
}

func createRouteTable(t *testing.T, ts *httptest.Server, rg, name string) {
	t.Helper()

	client, err := armnetwork.NewRouteTablesClient(rtSubID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	p, err := client.BeginCreateOrUpdate(context.Background(), rg, name, armnetwork.RouteTable{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create route table %s: %v", name, err)
	}

	pollDone(t, p)
}

func createVNet(t *testing.T, ts *httptest.Server, rg, name, prefix string) {
	t.Helper()

	client, err := armnetwork.NewVirtualNetworksClient(rtSubID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	p, err := client.BeginCreateOrUpdate(context.Background(), rg, name, armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr(prefix)}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet %s: %v", name, err)
	}

	pollDone(t, p)
}
