package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// Finding (BLOCKER): VirtualNetworkPeerings PUT/GET/DELETE were not routed as
// a sub-resource — routeVNet never inspected rp.SubResource for
// "virtualNetworkPeerings", so a standalone peering op hit the whole-VNet
// handler keyed on the parent VNet's own name. A peering DELETE therefore
// deleted the entire virtual network. This verifies a standalone peering
// PUT/GET/List/DELETE mutate only the addressed peering and preserve the
// parent VNet (and its sibling peerings).
func TestSDKVNetPeeringSubResourceCRUD(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	peerings, err := armnetwork.NewVirtualNetworkPeeringsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-a", "10.0.0.0/16")
	createTestVNet(t, ctx, vnets, "rg-1", "vnet-b", "10.1.0.0/16")

	remoteID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vnet-b"

	p, err := peerings.BeginCreateOrUpdate(ctx, "rg-1", "vnet-a", "a-to-b", armnetwork.VirtualNetworkPeering{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			RemoteVirtualNetwork:  &armnetwork.SubResource{ID: to.Ptr(remoteID)},
			AllowForwardedTraffic: to.Ptr(true),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate peering: %v", err)
	}

	created := pollDone(t, p)

	if created.Name == nil || *created.Name != "a-to-b" {
		t.Fatalf("created peering name = %v, want a-to-b", created.Name)
	}

	// Only one side exists so far: real ARM reports Initiated, not Connected,
	// until the reciprocal peering on vnet-b also exists.
	if created.Properties == nil || created.Properties.PeeringState == nil ||
		*created.Properties.PeeringState != armnetwork.VirtualNetworkPeeringStateInitiated {
		t.Fatalf("one-sided peering state = %v, want Initiated", created.Properties.PeeringState)
	}

	// The parent VNet must still exist and be untouched by the peering PUT —
	// this is the BLOCKER regression: a peering op must never alias the VNet.
	gotVNet, err := vnets.Get(ctx, "rg-1", "vnet-a", nil)
	if err != nil {
		t.Fatalf("vnet-a Get after peering create: %v", err)
	}

	if gotVNet.Properties == nil || gotVNet.Properties.AddressSpace == nil ||
		len(gotVNet.Properties.AddressSpace.AddressPrefixes) != 1 ||
		*gotVNet.Properties.AddressSpace.AddressPrefixes[0] != "10.0.0.0/16" {
		t.Fatalf("vnet-a survived peering create with wrong address space: %+v", gotVNet.Properties)
	}

	// Standalone Get.
	gotPeering, err := peerings.Get(ctx, "rg-1", "vnet-a", "a-to-b", nil)
	if err != nil {
		t.Fatalf("peering Get: %v", err)
	}

	if gotPeering.Properties == nil || gotPeering.Properties.RemoteVirtualNetwork == nil ||
		gotPeering.Properties.RemoteVirtualNetwork.ID == nil || *gotPeering.Properties.RemoteVirtualNetwork.ID != remoteID {
		t.Fatalf("peering remoteVirtualNetwork = %+v, want %s", gotPeering.Properties, remoteID)
	}

	if gotPeering.Properties.AllowForwardedTraffic == nil || !*gotPeering.Properties.AllowForwardedTraffic {
		t.Fatal("peering allowForwardedTraffic did not round-trip")
	}

	// The reciprocal peering on vnet-b: once it exists, both sides connect.
	p2, err := peerings.BeginCreateOrUpdate(ctx, "rg-1", "vnet-b", "b-to-a", armnetwork.VirtualNetworkPeering{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			RemoteVirtualNetwork: &armnetwork.SubResource{
				ID: to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vnet-a"),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate reciprocal peering: %v", err)
	}

	reciprocal := pollDone(t, p2)

	if reciprocal.Properties == nil || reciprocal.Properties.PeeringState == nil ||
		*reciprocal.Properties.PeeringState != armnetwork.VirtualNetworkPeeringStateConnected {
		t.Fatalf("reciprocal peering state = %v, want Connected", reciprocal.Properties.PeeringState)
	}

	// The original side must now report Connected too.
	aSide, err := peerings.Get(ctx, "rg-1", "vnet-a", "a-to-b", nil)
	if err != nil {
		t.Fatalf("peering Get after reciprocal: %v", err)
	}

	if aSide.Properties == nil || aSide.Properties.PeeringState == nil ||
		*aSide.Properties.PeeringState != armnetwork.VirtualNetworkPeeringStateConnected {
		t.Fatalf("original side peering state after reciprocal = %v, want Connected", aSide.Properties.PeeringState)
	}

	// Standalone List on vnet-a must show only its own peering.
	var names []string

	pager := peerings.NewListPager("rg-1", "vnet-a", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("peering list: %v", perr)
		}

		for _, pr := range page.Value {
			if pr.Name != nil {
				names = append(names, *pr.Name)
			}
		}
	}

	if len(names) != 1 || names[0] != "a-to-b" {
		t.Fatalf("vnet-a peerings list = %v, want [a-to-b]", names)
	}

	// Standalone Delete must remove only the addressed peering and leave the
	// parent VNet (and the other VNet's peering) intact.
	dp, err := peerings.BeginDelete(ctx, "rg-1", "vnet-a", "a-to-b", nil)
	if err != nil {
		t.Fatalf("BeginDelete peering: %v", err)
	}

	pollDone(t, dp)

	if _, err := vnets.Get(ctx, "rg-1", "vnet-a", nil); err != nil {
		t.Fatalf("vnet-a must survive its peering's delete: %v", err)
	}

	_, getErr := peerings.Get(ctx, "rg-1", "vnet-a", "a-to-b", nil)
	if getErr == nil {
		t.Fatal("deleted peering a-to-b should be gone")
	}

	if got := respStatus(t, getErr); got != 404 {
		t.Fatalf("deleted peering Get: status = %d, want 404", got)
	}

	if _, err := peerings.Get(ctx, "rg-1", "vnet-b", "b-to-a", nil); err != nil {
		t.Fatalf("sibling peering on vnet-b must survive vnet-a's peering delete: %v", err)
	}
}

// Finding: a peering pointing at a nonexistent remote VNet was silently
// accepted instead of resolving to a 404, and a self-peering was accepted
// instead of being rejected.
func TestSDKVNetPeeringValidation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	peerings, _ := armnetwork.NewVirtualNetworkPeeringsClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-solo", "10.2.0.0/16")

	_, err := peerings.BeginCreateOrUpdate(ctx, "rg-1", "vnet-solo", "to-nowhere", armnetwork.VirtualNetworkPeering{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			RemoteVirtualNetwork: &armnetwork.SubResource{
				ID: to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/does-not-exist"),
			},
		},
	}, nil)
	if err == nil {
		t.Fatal("peering to nonexistent remote VNet: want error, got nil")
	}

	if got := respStatus(t, err); got != 404 {
		t.Fatalf("peering to nonexistent remote VNet: status = %d, want 404", got)
	}

	selfID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vnet-solo"

	_, err = peerings.BeginCreateOrUpdate(ctx, "rg-1", "vnet-solo", "to-self", armnetwork.VirtualNetworkPeering{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			RemoteVirtualNetwork: &armnetwork.SubResource{ID: to.Ptr(selfID)},
		},
	}, nil)
	if err == nil {
		t.Fatal("self-peering: want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("self-peering: status = %d, want 400", got)
	}
}
