package vnet_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKInlineSubnetUnmodeledFieldRoundTrips is the load-bearing test for the
// echo overlay's array recursion: a vnet created with an INLINE subnet carrying
// a sub-field the handler does not model (privateEndpointNetworkPolicies) must
// reflect that sub-field on the inline subnet at GET — before the fix it was
// dropped because the overlay only recursed into maps, never array elements —
// while the modeled sub-field (addressPrefix) stays authoritative and no phantom
// subnet is injected. It also confirms a STANDALONE subnet PUT still echoes the
// same unmodeled field (the existing map/scalar path, unchanged).
func TestSDKInlineSubnetUnmodeledFieldRoundTrips(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	vnetClient, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	poller, err := vnetClient.BeginCreateOrUpdate(ctx, "rg-1", "vnet-inline",
		armnetwork.VirtualNetwork{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{
					AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")},
				},
				Subnets: []*armnetwork.Subnet{{
					Name: to.Ptr("sub-a"),
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: to.Ptr("10.0.1.0/24"),
						// Not modeled by the vnet handler's subnetResponseProps:
						// this is exactly the sub-field that used to be dropped on
						// the inline subnet while a standalone subnet PUT kept it.
						PrivateEndpointNetworkPolicies: to.Ptr(armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesEnabled),
					},
				}},
			},
		}, nil)
	if err != nil {
		t.Fatalf("vnet BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("vnet create poll: %v", err)
	}

	got, err := vnetClient.Get(ctx, "rg-1", "vnet-inline", nil)
	if err != nil {
		t.Fatalf("vnet Get: %v", err)
	}

	subs := got.Properties.Subnets
	if len(subs) != 1 {
		t.Fatalf("GET returned %d subnets, want exactly 1 (no phantom elements)", len(subs))
	}

	sub := subs[0]
	if sub.Properties == nil {
		t.Fatalf("inline subnet has no properties: %#v", sub)
	}

	// Modeled field: authoritative and correct (not duplicated/clobbered).
	if sub.Properties.AddressPrefix == nil || *sub.Properties.AddressPrefix != "10.0.1.0/24" {
		t.Errorf("inline subnet addressPrefix=%v, want 10.0.1.0/24", deref(sub.Properties.AddressPrefix))
	}

	// Unmodeled field: must now round-trip on the inline subnet (was dropped).
	if sub.Properties.PrivateEndpointNetworkPolicies == nil ||
		*sub.Properties.PrivateEndpointNetworkPolicies != armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesEnabled {
		t.Errorf("inline subnet dropped unmodeled privateEndpointNetworkPolicies: got %v",
			policyOf(sub.Properties.PrivateEndpointNetworkPolicies))
	}

	// A STANDALONE subnet PUT must still echo the same unmodeled field — the
	// pre-existing map/scalar overlay path, which this change must not regress.
	subnetClient, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	subPoller, err := subnetClient.BeginCreateOrUpdate(ctx, "rg-1", "vnet-inline", "sub-standalone",
		armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix:                  to.Ptr("10.0.2.0/24"),
				PrivateEndpointNetworkPolicies: to.Ptr(armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesDisabled),
			},
		}, nil)
	if err != nil {
		t.Fatalf("subnet BeginCreateOrUpdate: %v", err)
	}

	if _, err := subPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("subnet create poll: %v", err)
	}

	gotSub, err := subnetClient.Get(ctx, "rg-1", "vnet-inline", "sub-standalone", nil)
	if err != nil {
		t.Fatalf("subnet Get: %v", err)
	}

	if gotSub.Properties == nil || gotSub.Properties.PrivateEndpointNetworkPolicies == nil ||
		*gotSub.Properties.PrivateEndpointNetworkPolicies != armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesDisabled {
		t.Errorf("standalone subnet no longer echoes unmodeled privateEndpointNetworkPolicies: got %v",
			policyOf(subPolicy(gotSub.Properties)))
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}

	return *s
}

func policyOf(p *armnetwork.VirtualNetworkPrivateEndpointNetworkPolicies) string {
	if p == nil {
		return "<nil>"
	}

	return string(*p)
}

func subPolicy(p *armnetwork.SubnetPropertiesFormat) *armnetwork.VirtualNetworkPrivateEndpointNetworkPolicies {
	if p == nil {
		return nil
	}

	return p.PrivateEndpointNetworkPolicies
}
