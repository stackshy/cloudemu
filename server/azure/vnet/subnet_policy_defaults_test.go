package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKSubnetNetworkPolicyDefaults confirms a subnet created without either
// network-policy field reports the ARM defaults on GET —
// privateEndpointNetworkPolicies=Disabled and
// privateLinkServiceNetworkPolicies=Enabled (Subnets REST reference) — and that
// explicit values round-trip and a subsequent PUT that omits them resets to the
// defaults (full-replace CreateOrUpdate semantics).
func TestSDKSubnetNetworkPolicyDefaults(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	subnets, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	vp, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-pol", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	pollDone(t, vp)

	// Bare subnet, no policy fields set: GET must report the ARM defaults.
	sp, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-pol", "bare", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	pollDone(t, sp)

	assertPolicies(t, subnets, "bare",
		armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesDisabled,
		armnetwork.VirtualNetworkPrivateLinkServiceNetworkPoliciesEnabled)

	// Explicit values round-trip.
	sp2, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-pol", "explicit", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:                     to.Ptr("10.0.2.0/24"),
			PrivateEndpointNetworkPolicies:    to.Ptr(armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesEnabled),
			PrivateLinkServiceNetworkPolicies: to.Ptr(armnetwork.VirtualNetworkPrivateLinkServiceNetworkPoliciesDisabled),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	pollDone(t, sp2)

	assertPolicies(t, subnets, "explicit",
		armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesEnabled,
		armnetwork.VirtualNetworkPrivateLinkServiceNetworkPoliciesDisabled)

	// A follow-up PUT that omits the fields resets to defaults (full replace).
	sp3, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-pol", "explicit", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.2.0/24")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	pollDone(t, sp3)

	assertPolicies(t, subnets, "explicit",
		armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesDisabled,
		armnetwork.VirtualNetworkPrivateLinkServiceNetworkPoliciesEnabled)
}

// TestSDKInlineSubnetPolicyDefaults confirms an inline subnet (created through a
// vnet PUT, a distinct code path from the standalone SubnetsClient) also reports
// the ARM network-policy defaults on the vnet GET.
func TestSDKInlineSubnetPolicyDefaults(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	vp, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-inline-pol", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			Subnets: []*armnetwork.Subnet{{
				Name:       to.Ptr("inline"),
				Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	pollDone(t, vp)

	got, err := vnets.Get(ctx, "rg-1", "vnet-inline-pol", nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Properties.Subnets) != 1 {
		t.Fatalf("want 1 inline subnet, got %d", len(got.Properties.Subnets))
	}

	p := got.Properties.Subnets[0].Properties
	if p == nil || p.PrivateEndpointNetworkPolicies == nil ||
		*p.PrivateEndpointNetworkPolicies != armnetwork.VirtualNetworkPrivateEndpointNetworkPoliciesDisabled {
		t.Errorf("inline privateEndpointNetworkPolicies = %v, want Disabled", policyOf(subPolicy(p)))
	}

	if p == nil || p.PrivateLinkServiceNetworkPolicies == nil ||
		*p.PrivateLinkServiceNetworkPolicies != armnetwork.VirtualNetworkPrivateLinkServiceNetworkPoliciesEnabled {
		t.Errorf("inline privateLinkServiceNetworkPolicies = %v, want Enabled", p.PrivateLinkServiceNetworkPolicies)
	}
}

func assertPolicies(
	t *testing.T, subnets *armnetwork.SubnetsClient, name string,
	wantPENP armnetwork.VirtualNetworkPrivateEndpointNetworkPolicies,
	wantPLSNP armnetwork.VirtualNetworkPrivateLinkServiceNetworkPolicies,
) {
	t.Helper()

	got, err := subnets.Get(context.Background(), "rg-1", "vnet-pol", name, nil)
	if err != nil {
		t.Fatalf("get %s: %v", name, err)
	}

	if got.Properties == nil || got.Properties.PrivateEndpointNetworkPolicies == nil ||
		*got.Properties.PrivateEndpointNetworkPolicies != wantPENP {
		t.Errorf("%s privateEndpointNetworkPolicies = %v, want %v",
			name, policyOf(subPolicy(got.Properties)), wantPENP)
	}

	if got.Properties == nil || got.Properties.PrivateLinkServiceNetworkPolicies == nil ||
		*got.Properties.PrivateLinkServiceNetworkPolicies != wantPLSNP {
		t.Errorf("%s privateLinkServiceNetworkPolicies = %v, want %v",
			name, got.Properties.PrivateLinkServiceNetworkPolicies, wantPLSNP)
	}
}
