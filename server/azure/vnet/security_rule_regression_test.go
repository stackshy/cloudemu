package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// Finding (BLOCKER): individual SecurityRule sub-resource CRUD
// (SecurityRulesClient / azurerm_network_security_rule) was not routed —
// routeNSG never inspected rp.SubResource, so a standalone rule op hit the
// whole-NSG handler. This verifies a standalone rule PUT mutates only the
// addressed rule and preserves siblings, and standalone Get/List/Delete work.
func TestSDKSecurityRuleSubResourceCRUD(t *testing.T) {
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

	// Seed the NSG with one rule via the whole-object PUT.
	p, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-subres", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.SecurityGroupPropertiesFormat{
			SecurityRules: []*armnetwork.SecurityRule{{
				Name: to.Ptr("seed-rule"),
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Priority: to.Ptr(int32(100)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
					Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
					SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
					SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("22"),
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nsg: %v", err)
	}

	pollDone(t, p)

	// Standalone rule PUT: add a second rule via SecurityRulesClient.
	rp, err := rules.BeginCreateOrUpdate(ctx, "rg-1", "nsg-subres", "standalone-rule", armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(int32(200)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionOutbound),
			Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolUDP),
			SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
			SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("53"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate standalone rule: %v", err)
	}

	created := pollDone(t, rp)

	if created.Name == nil || *created.Name != "standalone-rule" {
		t.Fatalf("created rule name = %v, want standalone-rule", created.Name)
	}

	// The whole-NSG GET must show both rules — the standalone PUT must not
	// have clobbered the seed rule.
	got, err := nsgs.Get(ctx, "rg-1", "nsg-subres", nil)
	if err != nil {
		t.Fatalf("nsg Get: %v", err)
	}

	if len(got.Properties.SecurityRules) != 2 {
		t.Fatalf("nsg securityRules after standalone PUT = %d, want 2 (seed + standalone preserved)", len(got.Properties.SecurityRules))
	}

	// Standalone Get.
	gotRule, err := rules.Get(ctx, "rg-1", "nsg-subres", "standalone-rule", nil)
	if err != nil {
		t.Fatalf("rule Get: %v", err)
	}

	if gotRule.Properties == nil || gotRule.Properties.DestinationPortRange == nil ||
		*gotRule.Properties.DestinationPortRange != "53" {
		t.Fatalf("rule Get properties = %+v, want destinationPortRange 53", gotRule.Properties)
	}

	// Standalone List.
	var names []string

	pager := rules.NewListPager("rg-1", "nsg-subres", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("rule list: %v", perr)
		}

		for _, r := range page.Value {
			if r.Name != nil {
				names = append(names, *r.Name)
			}
		}
	}

	if len(names) != 2 {
		t.Fatalf("securityRules list = %v, want 2 entries", names)
	}

	// Standalone Delete must remove only the addressed rule.
	dp, err := rules.BeginDelete(ctx, "rg-1", "nsg-subres", "standalone-rule", nil)
	if err != nil {
		t.Fatalf("BeginDelete rule: %v", err)
	}

	pollDone(t, dp)

	got2, err := nsgs.Get(ctx, "rg-1", "nsg-subres", nil)
	if err != nil {
		t.Fatalf("nsg Get after delete: %v", err)
	}

	if len(got2.Properties.SecurityRules) != 1 || got2.Properties.SecurityRules[0].Name == nil ||
		*got2.Properties.SecurityRules[0].Name != "seed-rule" {
		t.Fatalf("nsg securityRules after standalone delete = %+v, want only seed-rule", got2.Properties.SecurityRules)
	}
}

// Finding: no priority validation on security rules — an out-of-range
// priority and a duplicate priority within the same direction were both
// silently accepted.
func TestSDKSecurityRulePriorityValidation(t *testing.T) {
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

	nsgP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-priority", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create nsg: %v", err)
	}

	pollDone(t, nsgP)

	_, err = rules.BeginCreateOrUpdate(ctx, "rg-1", "nsg-priority", "too-high", armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(int32(5000)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
			Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
			SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
			SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("80"),
		},
	}, nil)
	if err == nil {
		t.Fatal("priority 5000 (out of [100,4096]): want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("priority 5000: status = %d, want 400", got)
	}

	p1, err := rules.BeginCreateOrUpdate(ctx, "rg-1", "nsg-priority", "rule-1", armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(int32(100)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
			Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
			SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
			SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("80"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create rule-1: %v", err)
	}

	pollDone(t, p1)

	_, err = rules.BeginCreateOrUpdate(ctx, "rg-1", "nsg-priority", "rule-2", armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(int32(100)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
			Access: to.Ptr(armnetwork.SecurityRuleAccessDeny), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
			SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
			SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("81"),
		},
	}, nil)
	if err == nil {
		t.Fatal("duplicate priority+direction (rule-2 vs rule-1): want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("duplicate priority+direction: status = %d, want 400", got)
	}

	// The same priority in the OTHER direction is fine.
	p3, err := rules.BeginCreateOrUpdate(ctx, "rg-1", "nsg-priority", "rule-3", armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(int32(100)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionOutbound),
			Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
			SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
			SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("82"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("same priority, opposite direction should be accepted: %v", err)
	}

	pollDone(t, p3)
}

// Finding: a custom rule named identically to a reserved default-rule name
// was accepted, silently shadowing the real default rule's identity.
func TestSDKSecurityRuleReservedNameCollisionRejected(t *testing.T) {
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

	nsgP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-reserved", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create nsg: %v", err)
	}

	pollDone(t, nsgP)

	_, err = rules.BeginCreateOrUpdate(ctx, "rg-1", "nsg-reserved", "DenyAllInBound", armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Priority: to.Ptr(int32(150)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
			Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
			SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
			SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("80"),
		},
	}, nil)
	if err == nil {
		t.Fatal("rule named DenyAllInBound (reserved): want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("reserved rule name: status = %d, want 400", got)
	}
}

// Finding: NSG association to Subnets and NICs was unimplemented — the
// networkSecurityGroup reference was dropped on write and never echoed, and
// the NSG's own GET never listed the associated subnet/NIC back-references.
func TestSDKNSGSubnetAndNICAssociation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nsgs, _ := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)
	nics, _ := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)

	nsgP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-assoc", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("create nsg: %v", err)
	}

	pollDone(t, nsgP)

	nsgID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkSecurityGroups/nsg-assoc"

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-assoc", "10.0.0.0/16")

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-assoc", "default", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:        to.Ptr("10.0.1.0/24"),
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(nsgID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet with NSG: %v", err)
	}

	sub := pollDone(t, subP)

	if sub.Properties == nil || sub.Properties.NetworkSecurityGroup == nil || sub.Properties.NetworkSecurityGroup.ID == nil ||
		*sub.Properties.NetworkSecurityGroup.ID != nsgID {
		t.Fatalf("subnet networkSecurityGroup = %+v, want %s", sub.Properties, nsgID)
	}

	subnetID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-assoc/subnets/default"

	nicP, err := nics.BeginCreateOrUpdate(ctx, "rg-1", "nic-assoc", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(nsgID)},
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet: &armnetwork.Subnet{ID: to.Ptr(subnetID)},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nic with NSG: %v", err)
	}

	nic := pollDone(t, nicP)

	if nic.Properties == nil || nic.Properties.NetworkSecurityGroup == nil || nic.Properties.NetworkSecurityGroup.ID == nil ||
		*nic.Properties.NetworkSecurityGroup.ID != nsgID {
		t.Fatalf("nic networkSecurityGroup = %+v, want %s", nic.Properties, nsgID)
	}

	gotNSG, err := nsgs.Get(ctx, "rg-1", "nsg-assoc", nil)
	if err != nil {
		t.Fatalf("nsg Get: %v", err)
	}

	if len(gotNSG.Properties.Subnets) != 1 {
		t.Fatalf("nsg.properties.subnets = %v, want 1 associated subnet", gotNSG.Properties.Subnets)
	}

	if len(gotNSG.Properties.NetworkInterfaces) != 1 {
		t.Fatalf("nsg.properties.networkInterfaces = %v, want 1 associated NIC", gotNSG.Properties.NetworkInterfaces)
	}
}

// Finding: no effective-security-rules endpoint —
// InterfacesClient.BeginListEffectiveNetworkSecurityGroups was absent.
func TestSDKEffectiveNetworkSecurityGroups(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nsgs, _ := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)
	nics, _ := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)

	nicNSGP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-nic-eff", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.SecurityGroupPropertiesFormat{
			SecurityRules: []*armnetwork.SecurityRule{{
				Name: to.Ptr("allow-nic"),
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Priority: to.Ptr(int32(100)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
					Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
					SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
					SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("22"),
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nic-level nsg: %v", err)
	}

	pollDone(t, nicNSGP)

	subnetNSGP, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-subnet-eff", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.SecurityGroupPropertiesFormat{
			SecurityRules: []*armnetwork.SecurityRule{{
				Name: to.Ptr("allow-subnet"),
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Priority: to.Ptr(int32(100)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
					Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
					SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
					SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("443"),
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet-level nsg: %v", err)
	}

	pollDone(t, subnetNSGP)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-eff", "10.0.0.0/16")

	subnetNSGID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkSecurityGroups/nsg-subnet-eff"

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-eff", "default", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:        to.Ptr("10.0.1.0/24"),
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(subnetNSGID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	pollDone(t, subP)

	subnetID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-eff/subnets/default"
	nicNSGID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkSecurityGroups/nsg-nic-eff"

	nicP, err := nics.BeginCreateOrUpdate(ctx, "rg-1", "nic-eff", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(nicNSGID)},
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Primary: to.Ptr(true),
					Subnet:  &armnetwork.Subnet{ID: to.Ptr(subnetID)},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nic: %v", err)
	}

	pollDone(t, nicP)

	effP, err := nics.BeginListEffectiveNetworkSecurityGroups(ctx, "rg-1", "nic-eff", nil)
	if err != nil {
		t.Fatalf("BeginListEffectiveNetworkSecurityGroups: %v", err)
	}

	eff := pollDone(t, effP)

	if len(eff.Value) != 2 {
		t.Fatalf("effective NSGs = %d, want 2 (NIC-level + subnet-level)", len(eff.Value))
	}

	var sawNICLevel, sawSubnetLevel, sawCustomRule, sawDefaultRule bool

	for _, g := range eff.Value {
		if g.NetworkSecurityGroup != nil && g.NetworkSecurityGroup.ID != nil {
			switch *g.NetworkSecurityGroup.ID {
			case nicNSGID:
				sawNICLevel = true
			case subnetNSGID:
				sawSubnetLevel = true
			}
		}

		for _, r := range g.EffectiveSecurityRules {
			if r.Name == nil {
				continue
			}

			switch *r.Name {
			case "securityRules/allow-nic", "securityRules/allow-subnet":
				sawCustomRule = true
			case "defaultSecurityRules/DenyAllInBound":
				sawDefaultRule = true
			}
		}
	}

	if !sawNICLevel {
		t.Error("effective NSGs missing the NIC-level NSG entry")
	}

	if !sawSubnetLevel {
		t.Error("effective NSGs missing the subnet-level NSG entry")
	}

	if !sawCustomRule {
		t.Error("effective NSGs missing a custom rule")
	}

	if !sawDefaultRule {
		t.Error("effective NSGs missing a default rule")
	}
}
