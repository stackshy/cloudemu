package vnet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKStandaloneRouteSubresource drives the real armnetwork RoutesClient
// (the azurerm_route resource) through create/get/delete against a route table
// that already carries an inline route — the standalone route must coexist with
// the inline one on the whole-table GET, be independently gettable, and DELETE
// must remove only itself. Guards the sub-resource round-trip fix.
func TestSDKStandaloneRouteSubresource(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	rtClient, err := armnetwork.NewRouteTablesClient(rtSubID, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Route table created with one inline route.
	cp, err := rtClient.BeginCreateOrUpdate(ctx, "rg-1", "rt-sub", armnetwork.RouteTable{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.RouteTablePropertiesFormat{
			Routes: []*armnetwork.Route{{
				Name: to.Ptr("inline"),
				Properties: &armnetwork.RoutePropertiesFormat{
					AddressPrefix: to.Ptr("10.0.0.0/16"),
					NextHopType:   to.Ptr(armnetwork.RouteNextHopTypeVnetLocal),
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("route table create: %v", err)
	}

	pollDone(t, cp)

	routes, err := armnetwork.NewRoutesClient(rtSubID, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Add a standalone route via the sub-resource path.
	rp, err := routes.BeginCreateOrUpdate(ctx, "rg-1", "rt-sub", "standalone", armnetwork.Route{
		Properties: &armnetwork.RoutePropertiesFormat{
			AddressPrefix:    to.Ptr("192.168.0.0/24"),
			NextHopType:      to.Ptr(armnetwork.RouteNextHopTypeVirtualAppliance),
			NextHopIPAddress: to.Ptr("10.0.0.4"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("standalone route create: %v", err)
	}

	pollDone(t, rp)

	// Direct GET of the standalone route round-trips its fields.
	sg, err := routes.Get(ctx, "rg-1", "rt-sub", "standalone", nil)
	if err != nil {
		t.Fatalf("standalone route get: %v", err)
	}

	if sg.Properties == nil || *sg.Properties.AddressPrefix != "192.168.0.0/24" ||
		*sg.Properties.NextHopType != armnetwork.RouteNextHopTypeVirtualAppliance ||
		*sg.Properties.NextHopIPAddress != "10.0.0.4" {
		t.Fatalf("standalone route round-trip=%+v", sg.Properties)
	}

	// Whole-table GET shows BOTH the inline and the standalone route.
	whole, err := rtClient.Get(ctx, "rg-1", "rt-sub", nil)
	if err != nil {
		t.Fatalf("route table get: %v", err)
	}

	got := map[string]bool{}
	for _, r := range whole.Properties.Routes {
		got[*r.Name] = true
	}

	if !got["inline"] || !got["standalone"] {
		t.Fatalf("whole-table routes=%v want inline+standalone", got)
	}

	// DELETE removes only the standalone route; the inline route survives.
	dp, err := routes.BeginDelete(ctx, "rg-1", "rt-sub", "standalone", nil)
	if err != nil {
		t.Fatalf("standalone route delete: %v", err)
	}

	pollDone(t, dp)

	after, err := rtClient.Get(ctx, "rg-1", "rt-sub", nil)
	if err != nil {
		t.Fatalf("route table get after delete: %v", err)
	}

	if len(after.Properties.Routes) != 1 || *after.Properties.Routes[0].Name != "inline" {
		t.Fatalf("after delete routes=%v want [inline]", after.Properties.Routes)
	}
}

// TestSDKRouteDisableBgpPropagation guards that disableBgpRoutePropagation is a
// *bool that round-trips an explicit false (the echo overlay would otherwise
// swallow the zero value) and survives a tags PATCH.
func TestSDKRouteDisableBgpPropagation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	rtClient, err := armnetwork.NewRouteTablesClient(rtSubID, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		want bool
	}{{"rt-bgp-false", false}, {"rt-bgp-true", true}} {
		cp, cerr := rtClient.BeginCreateOrUpdate(ctx, "rg-1", tc.name, armnetwork.RouteTable{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.RouteTablePropertiesFormat{
				DisableBgpRoutePropagation: to.Ptr(tc.want),
			},
		}, nil)
		if cerr != nil {
			t.Fatalf("%s create: %v", tc.name, cerr)
		}

		pollDone(t, cp)

		got, gerr := rtClient.Get(ctx, "rg-1", tc.name, nil)
		if gerr != nil {
			t.Fatalf("%s get: %v", tc.name, gerr)
		}

		if got.Properties == nil || got.Properties.DisableBgpRoutePropagation == nil ||
			*got.Properties.DisableBgpRoutePropagation != tc.want {
			t.Fatalf("%s disableBgpRoutePropagation=%v want %v", tc.name, got.Properties.DisableBgpRoutePropagation, tc.want)
		}

		// A tags PATCH must preserve the flag.
		if _, uerr := rtClient.UpdateTags(ctx, "rg-1", tc.name, armnetwork.TagsObject{
			Tags: map[string]*string{"env": to.Ptr("test")},
		}, nil); uerr != nil {
			t.Fatalf("%s update tags: %v", tc.name, uerr)
		}

		after, aerr := rtClient.Get(ctx, "rg-1", tc.name, nil)
		if aerr != nil {
			t.Fatalf("%s get after tags: %v", tc.name, aerr)
		}

		if after.Properties.DisableBgpRoutePropagation == nil ||
			*after.Properties.DisableBgpRoutePropagation != tc.want {
			t.Fatalf("%s disableBgpRoutePropagation after tags=%v want %v",
				tc.name, after.Properties.DisableBgpRoutePropagation, tc.want)
		}
	}
}

// TestSDKRouteNextHopTypeValidation guards the nextHopType validation on the
// standalone route path: an unknown next-hop type is rejected, and a
// nextHopIpAddress is only permitted when the type is VirtualAppliance.
func TestSDKRouteNextHopTypeValidation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	rtClient, err := armnetwork.NewRouteTablesClient(rtSubID, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	cp, err := rtClient.BeginCreateOrUpdate(ctx, "rg-1", "rt-valid", armnetwork.RouteTable{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("route table create: %v", err)
	}

	pollDone(t, cp)

	routes, err := armnetwork.NewRoutesClient(rtSubID, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		props *armnetwork.RoutePropertiesFormat
	}{
		{
			name: "unknown-next-hop",
			props: &armnetwork.RoutePropertiesFormat{
				AddressPrefix: to.Ptr("10.0.0.0/16"),
				NextHopType:   to.Ptr(armnetwork.RouteNextHopType("BogusHop")),
			},
		},
		{
			name: "ip-on-non-virtual-appliance",
			props: &armnetwork.RoutePropertiesFormat{
				AddressPrefix:    to.Ptr("10.0.0.0/16"),
				NextHopType:      to.Ptr(armnetwork.RouteNextHopTypeInternet),
				NextHopIPAddress: to.Ptr("10.0.0.4"),
			},
		},
	} {
		_, rerr := routes.BeginCreateOrUpdate(ctx, "rg-1", "rt-valid", tc.name, armnetwork.Route{
			Properties: tc.props,
		}, nil)

		var respErr *azcore.ResponseError
		if !errors.As(rerr, &respErr) || respErr.StatusCode != 400 {
			t.Fatalf("%s: want 400, got %v", tc.name, rerr)
		}
	}
}
