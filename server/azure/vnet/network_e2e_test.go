package vnet_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newVNetServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func pollDone[T any](t *testing.T, p *runtime.Poller[T]) T {
	t.Helper()

	res, err := p.PollUntilDone(context.Background(), &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}

	return res
}

// Findings #2 (idempotent PUT), #13 (all address prefixes), #14 (location),
// #19 (etag).
func TestSDKVNetIdempotentAddressSpaceLocation(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	client, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	body := armnetwork.VirtualNetwork{
		Location: to.Ptr("westus2"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{
				AddressPrefixes: []*string{to.Ptr("10.0.0.0/16"), to.Ptr("10.1.0.0/16")},
			},
		},
	}

	for i := 0; i < 2; i++ {
		p, cerr := client.BeginCreateOrUpdate(ctx, "rg-1", "vnet-idem", body, nil)
		if cerr != nil {
			t.Fatalf("create %d: %v", i, cerr)
		}

		pollDone(t, p)
	}

	var count int

	pager := client.NewListPager("rg-1", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list: %v", perr)
		}

		for _, v := range page.Value {
			if v.Name != nil && *v.Name == "vnet-idem" {
				count++
			}
		}
	}

	if count != 1 {
		t.Fatalf("idempotent PUT created %d vnet-idem entries, want 1", count)
	}

	got, err := client.Get(ctx, "rg-1", "vnet-idem", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Location == nil || *got.Location != "westus2" {
		t.Fatalf("location = %v, want westus2", got.Location)
	}

	if got.Properties == nil || got.Properties.AddressSpace == nil ||
		len(got.Properties.AddressSpace.AddressPrefixes) != 2 {
		t.Fatalf("addressPrefixes = %+v, want 2 entries", got.Properties)
	}

	if got.Etag == nil || !isWeakETag(*got.Etag) {
		t.Fatalf("vnet etag = %v, want weak-validator form W/\"...\"", got.Etag)
	}
}

// isWeakETag reports whether s is in the weak-validator form W/"..." that the
// real ARM API emits for Microsoft.Network resources (VNets/NSGs/subnets).
func isWeakETag(s string) bool {
	return len(s) >= 4 && s[:3] == `W/"` && s[len(s)-1] == '"'
}

// Findings #17 (inline subnets materialized) and #12 (subnet cascade on delete).
func TestSDKVNetInlineSubnetsCascade(t *testing.T) {
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

	p, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-sub", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			Subnets: []*armnetwork.Subnet{{
				Name:       to.Ptr("inline-sub"),
				Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet: %v", err)
	}

	pollDone(t, p)

	sub, err := subnets.Get(ctx, "rg-1", "vnet-sub", "inline-sub", nil)
	if err != nil {
		t.Fatalf("inline subnet Get: %v, want materialized child", err)
	}

	if sub.ID == nil || *sub.ID == "" {
		t.Fatalf("inline subnet id empty")
	}

	delP, err := vnets.BeginDelete(ctx, "rg-1", "vnet-sub", nil)
	if err != nil {
		t.Fatalf("delete vnet: %v", err)
	}

	pollDone(t, delP)

	_, err = subnets.Get(ctx, "rg-1", "vnet-sub", "inline-sub", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("subnet Get after vnet delete: got %v, want 404 (cascaded)", err)
	}
}

// Finding #12: deleting a vnet whose subnet is bound to a NIC is refused.
func TestSDKVNetDeleteBlockedWhenSubnetInUse(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	nics, _ := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, clientOpts(ts))

	subnetID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-inuse/subnets/default"

	p, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-inuse", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			Subnets: []*armnetwork.Subnet{{
				Name:       to.Ptr("default"),
				Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet: %v", err)
	}

	pollDone(t, p)

	nicP, err := nics.BeginCreateOrUpdate(ctx, "rg-1", "nic1", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet: &armnetwork.Subnet{ID: to.Ptr(subnetID)},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nic: %v", err)
	}

	pollDone(t, nicP)

	_, err = vnets.BeginDelete(ctx, "rg-1", "vnet-inuse", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 400 {
		t.Fatalf("delete vnet with in-use subnet: got %v, want 400", err)
	}
}

// Findings #3 (idempotent NSG PUT), #4 (custom rules persist), #5 (default
// rules), #14 (location), #19 (etag).
func TestSDKNSGRulesDefaultsIdempotent(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	client, err := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	body := armnetwork.SecurityGroup{
		Location: to.Ptr("westus2"),
		Properties: &armnetwork.SecurityGroupPropertiesFormat{
			SecurityRules: []*armnetwork.SecurityRule{{
				Name: to.Ptr("Allow-SSH"),
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Priority:                 to.Ptr(int32(100)),
					Direction:                to.Ptr(armnetwork.SecurityRuleDirectionInbound),
					Access:                   to.Ptr(armnetwork.SecurityRuleAccessAllow),
					Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolTCP),
					SourceAddressPrefix:      to.Ptr("*"),
					DestinationAddressPrefix: to.Ptr("*"),
					SourcePortRange:          to.Ptr("*"),
					DestinationPortRange:     to.Ptr("22"),
				},
			}},
		},
	}

	for i := 0; i < 2; i++ {
		p, cerr := client.BeginCreateOrUpdate(ctx, "rg-1", "nsg-rules", body, nil)
		if cerr != nil {
			t.Fatalf("create %d: %v", i, cerr)
		}

		pollDone(t, p)
	}

	var count int

	pager := client.NewListPager("rg-1", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list: %v", perr)
		}

		for _, g := range page.Value {
			if g.Name != nil && *g.Name == "nsg-rules" {
				count++
			}
		}
	}

	if count != 1 {
		t.Fatalf("idempotent NSG PUT created %d entries, want 1", count)
	}

	got, err := client.Get(ctx, "rg-1", "nsg-rules", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Location == nil || *got.Location != "westus2" {
		t.Fatalf("location = %v, want westus2", got.Location)
	}

	if got.Etag == nil || !isWeakETag(*got.Etag) {
		t.Fatalf("nsg etag = %v, want weak-validator form W/\"...\"", got.Etag)
	}

	if got.Properties == nil || len(got.Properties.SecurityRules) != 1 {
		t.Fatalf("securityRules = %+v, want 1 persisted custom rule", got.Properties)
	}

	rule := got.Properties.SecurityRules[0]
	if rule.Name == nil || *rule.Name != "Allow-SSH" {
		t.Fatalf("rule name = %v, want Allow-SSH", rule.Name)
	}

	if rule.Properties == nil || rule.Properties.DestinationPortRange == nil ||
		*rule.Properties.DestinationPortRange != "22" || rule.Properties.Priority == nil ||
		*rule.Properties.Priority != 100 {
		t.Fatalf("rule properties = %+v, want dstPort 22 / priority 100", rule.Properties)
	}

	if len(got.Properties.DefaultSecurityRules) != 6 {
		t.Fatalf("defaultSecurityRules = %d, want 6 built-ins", len(got.Properties.DefaultSecurityRules))
	}
}
