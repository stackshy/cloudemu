// Deep-audit round-2 regression (N1/N2): the securityRules and
// virtualNetworkPeerings SUB-resources resolved their parent (NSG / vnet) by
// name only, while the whole-resource GET/PUT/DELETE paths were already
// RG-scoped (#623). With two same-named parents in different resource groups, a
// sub-resource op addressed to one group silently hit the other group's parent.
// A sub-resource op must be scoped to its parent's resource group: an op under
// the wrong group is a 404, and a sub-resource created under one group is not
// visible under a same-named parent in another group.

package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSecurityRuleSubResourceIsResourceGroupScoped proves a securityRule op on a
// same-named NSG is scoped to the NSG's resource group: a rule PUT under rgB
// touches only rgB's NSG (rgA's NSG rules are unchanged), the rule is not
// visible under rgA's same-named NSG, and an op under a group holding no such
// NSG is a 404.
func TestSecurityRuleSubResourceIsResourceGroupScoped(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	nsgs, err := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	rules, err := armnetwork.NewSecurityRulesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Two NSGs share the name "nsg-shared" across rgA and rgB — a legal Azure
	// pattern (the same Terraform module applied to two resource groups).
	for _, rg := range []string{"rgA", "rgB"} {
		p, cerr := nsgs.BeginCreateOrUpdate(ctx, rg, "nsg-shared", armnetwork.SecurityGroup{
			Location: to.Ptr("eastus"),
		}, nil)
		if cerr != nil {
			t.Fatalf("create nsg-shared in %s: %v", rg, cerr)
		}

		pollDone(t, p)
	}

	// A rule created under rgB/nsg-shared must land on rgB's NSG only.
	rp, err := rules.BeginCreateOrUpdate(ctx, "rgB", "nsg-shared", "only-in-b", armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(int32(200)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
			Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
			SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
			SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("22"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create rule only-in-b under rgB: %v", err)
	}

	pollDone(t, rp)

	// Correct RG: the rule is readable under rgB.
	if _, gerr := rules.Get(ctx, "rgB", "nsg-shared", "only-in-b", nil); gerr != nil {
		t.Fatalf("Get only-in-b under rgB: %v", gerr)
	}

	// Wrong RG (N1 bug): the rule must NOT be visible under rgA's same-named NSG.
	if _, gerr := rules.Get(ctx, "rgA", "nsg-shared", "only-in-b", nil); gerr == nil {
		t.Fatal("Get only-in-b under rgA succeeded; rule created in rgB leaked into rgA's NSG")
	} else if code := respStatus(t, gerr); code != 404 {
		t.Fatalf("Get only-in-b under rgA: status %d, want 404", code)
	}

	// The whole-NSG GET under rgA must show no custom rules — rgB's rule never
	// touched rgA's NSG.
	gotA, err := nsgs.Get(ctx, "rgA", "nsg-shared", nil)
	if err != nil {
		t.Fatalf("nsg Get rgA: %v", err)
	}

	if len(gotA.Properties.SecurityRules) != 0 {
		t.Fatalf("rgA nsg-shared custom rules = %d, want 0 (rgB's rule must not appear)", len(gotA.Properties.SecurityRules))
	}

	// The rule IS present on rgB's NSG.
	gotB, err := nsgs.Get(ctx, "rgB", "nsg-shared", nil)
	if err != nil {
		t.Fatalf("nsg Get rgB: %v", err)
	}

	if len(gotB.Properties.SecurityRules) != 1 || gotB.Properties.SecurityRules[0].Name == nil ||
		*gotB.Properties.SecurityRules[0].Name != "only-in-b" {
		t.Fatalf("rgB nsg-shared rules = %+v, want [only-in-b]", gotB.Properties.SecurityRules)
	}

	// An op addressed to a group with no such NSG is a 404, not a silent hit on
	// another group's NSG.
	if _, gerr := rules.Get(ctx, "rgC", "nsg-shared", "only-in-b", nil); gerr == nil {
		t.Fatal("Get under rgC (no nsg-shared) succeeded; want 404")
	} else if code := respStatus(t, gerr); code != 404 {
		t.Fatalf("Get under rgC: status %d, want 404", code)
	}

	// A standalone DELETE under the wrong RG must not remove rgB's rule.
	if _, derr := rules.BeginDelete(ctx, "rgA", "nsg-shared", "only-in-b", nil); derr == nil {
		t.Fatal("Delete only-in-b under rgA succeeded; want 404 (rule lives in rgB)")
	} else if code := respStatus(t, derr); code != 404 {
		t.Fatalf("Delete only-in-b under rgA: status %d, want 404", code)
	}

	if _, gerr := rules.Get(ctx, "rgB", "nsg-shared", "only-in-b", nil); gerr != nil {
		t.Fatalf("rgB's rule was removed by a wrong-RG delete: %v", gerr)
	}
}

// TestVNetPeeringSubResourceIsResourceGroupScoped proves a peering op on a
// same-named vnet is scoped to the vnet's resource group: a peering PUT under
// rgB touches only rgB's vnet, is not visible under rgA's same-named vnet, and
// an op under a group holding no such vnet is a 404.
func TestVNetPeeringSubResourceIsResourceGroupScoped(t *testing.T) {
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

	// Two vnets share the name "vnet-shared" across rgA and rgB.
	createTestVNet(t, ctx, vnets, "rgA", "vnet-shared", "10.0.0.0/16")
	createTestVNet(t, ctx, vnets, "rgB", "vnet-shared", "10.1.0.0/16")
	// A remote vnet in rgB for the peering to point at.
	createTestVNet(t, ctx, vnets, "rgB", "vnet-remote", "10.2.0.0/16")

	remoteID := "/subscriptions/sub-1/resourceGroups/rgB/providers/Microsoft.Network/virtualNetworks/vnet-remote"

	// A peering created under rgB/vnet-shared must land on rgB's vnet only.
	p, err := peerings.BeginCreateOrUpdate(ctx, "rgB", "vnet-shared", "shared-peer", armnetwork.VirtualNetworkPeering{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			RemoteVirtualNetwork: &armnetwork.SubResource{ID: to.Ptr(remoteID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create peering shared-peer under rgB: %v", err)
	}

	pollDone(t, p)

	// Correct RG: the peering is readable under rgB.
	if _, gerr := peerings.Get(ctx, "rgB", "vnet-shared", "shared-peer", nil); gerr != nil {
		t.Fatalf("Get shared-peer under rgB: %v", gerr)
	}

	// Wrong RG (N2 bug): the peering must NOT be visible under rgA's same-named vnet.
	if _, gerr := peerings.Get(ctx, "rgA", "vnet-shared", "shared-peer", nil); gerr == nil {
		t.Fatal("Get shared-peer under rgA succeeded; peering created in rgB leaked into rgA's vnet")
	} else if code := respStatus(t, gerr); code != 404 {
		t.Fatalf("Get shared-peer under rgA: status %d, want 404", code)
	}

	// The list under rgA/vnet-shared must be empty — rgB's peering never touched it.
	var rgANames []string

	pagerA := peerings.NewListPager("rgA", "vnet-shared", nil)
	for pagerA.More() {
		page, perr := pagerA.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list peerings rgA: %v", perr)
		}

		for _, pr := range page.Value {
			if pr.Name != nil {
				rgANames = append(rgANames, *pr.Name)
			}
		}
	}

	if len(rgANames) != 0 {
		t.Fatalf("rgA vnet-shared peerings = %v, want none (rgB's peering must not appear)", rgANames)
	}

	// The peering IS present on rgB's vnet.
	var rgBNames []string

	pagerB := peerings.NewListPager("rgB", "vnet-shared", nil)
	for pagerB.More() {
		page, perr := pagerB.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list peerings rgB: %v", perr)
		}

		for _, pr := range page.Value {
			if pr.Name != nil {
				rgBNames = append(rgBNames, *pr.Name)
			}
		}
	}

	if len(rgBNames) != 1 || rgBNames[0] != "shared-peer" {
		t.Fatalf("rgB vnet-shared peerings = %v, want [shared-peer]", rgBNames)
	}

	// An op addressed to a group with no such vnet is a 404.
	if _, gerr := peerings.Get(ctx, "rgC", "vnet-shared", "shared-peer", nil); gerr == nil {
		t.Fatal("Get peering under rgC (no vnet-shared) succeeded; want 404")
	} else if code := respStatus(t, gerr); code != 404 {
		t.Fatalf("Get peering under rgC: status %d, want 404", code)
	}

	// A DELETE under the wrong RG must not remove rgB's peering.
	if _, derr := peerings.BeginDelete(ctx, "rgA", "vnet-shared", "shared-peer", nil); derr == nil {
		t.Fatal("Delete shared-peer under rgA succeeded; want 404 (peering lives in rgB)")
	} else if code := respStatus(t, derr); code != 404 {
		t.Fatalf("Delete shared-peer under rgA: status %d, want 404", code)
	}

	if _, gerr := peerings.Get(ctx, "rgB", "vnet-shared", "shared-peer", nil); gerr != nil {
		t.Fatalf("rgB's peering was removed by a wrong-RG delete: %v", gerr)
	}
}
