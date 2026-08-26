package vnet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// respStatus extracts the HTTP status code from an SDK error, failing the
// test if err isn't an *azcore.ResponseError.
func respStatus(t *testing.T, err error) int {
	t.Helper()

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("error is not *azcore.ResponseError: %v", err)
	}

	return respErr.StatusCode
}

// createTestVNet creates a vnet with the given address prefix and no subnets.
func createTestVNet(t *testing.T, ctx context.Context, vnets *armnetwork.VirtualNetworksClient, rg, name, prefix string) {
	t.Helper()

	p, err := vnets.BeginCreateOrUpdate(ctx, rg, name, armnetwork.VirtualNetwork{
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

// Finding: DeleteSubnet has no in-use guard — a standalone subnet delete
// bypassed the check the whole-vnet-delete path already had.
func TestSDKDeleteSubnetInUseBlocked(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nics, _ := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-subdel", "10.0.0.0/16")

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-subdel", "default", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	pollDone(t, subP)

	subnetID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-subdel/subnets/default"

	nicP, err := nics.BeginCreateOrUpdate(ctx, "rg-1", "nic-subdel", armnetwork.Interface{
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

	_, err = subnets.BeginDelete(ctx, "rg-1", "vnet-subdel", "default", nil)
	if err == nil {
		t.Fatal("delete subnet in use: want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("delete subnet in use: status = %d, want 400", got)
	}
}

// Finding: CreateSubnet did zero CIDR validation — a prefix outside the
// vnet's address space was silently accepted.
func TestSDKCreateSubnetOutsideVNetAddressSpaceRejected(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-oob", "10.0.0.0/16")

	_, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-oob", "outside", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("11.0.0.0/24")},
	}, nil)
	if err == nil {
		t.Fatal("create subnet outside vnet address space: want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("create subnet outside vnet address space: status = %d, want 400", got)
	}
}

// Finding: CreateSubnet did zero CIDR validation — a prefix overlapping a
// sibling subnet was silently accepted.
func TestSDKCreateSubnetOverlappingSiblingRejected(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-overlap", "10.0.0.0/16")

	p, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-overlap", "sub-a", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet sub-a: %v", err)
	}

	pollDone(t, p)

	_, err = subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-overlap", "sub-b", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.128/25")},
	}, nil)
	if err == nil {
		t.Fatal("create overlapping subnet: want error, got nil")
	}

	if got := respStatus(t, err); got != 400 {
		t.Fatalf("create overlapping subnet: status = %d, want 400", got)
	}
}

// Finding: PUT VirtualNetwork with a REDUCED subnets[] array must delete the
// omitted previously-existing subnet (authoritative replace), matching real
// ARM whole-VNet PUT semantics.
func TestSDKVNetPutReducedSubnetsDeletesOmitted(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)

	p, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-shrink", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			Subnets: []*armnetwork.Subnet{
				{Name: to.Ptr("keep"), Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")}},
				{Name: to.Ptr("drop"), Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.2.0/24")}},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet: %v", err)
	}

	pollDone(t, p)

	if _, err := subnets.Get(ctx, "rg-1", "vnet-shrink", "drop", nil); err != nil {
		t.Fatalf("subnet drop should exist before the reduced PUT: %v", err)
	}

	p2, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-shrink", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			Subnets: []*armnetwork.Subnet{
				{Name: to.Ptr("keep"), Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")}},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("reduced PUT: %v", err)
	}

	pollDone(t, p2)

	if _, err := subnets.Get(ctx, "rg-1", "vnet-shrink", "keep", nil); err != nil {
		t.Fatalf("subnet keep should survive the reduced PUT: %v", err)
	}

	_, err = subnets.Get(ctx, "rg-1", "vnet-shrink", "drop", nil)
	if err == nil {
		t.Fatal("subnet drop should be gone after the reduced PUT")
	}

	if got := respStatus(t, err); got != 404 {
		t.Fatalf("subnet drop Get after reduced PUT: status = %d, want 404", got)
	}
}

// Azure added an explicit carve-out ("Azure Virtual Network now supports
// updates without subnet property"): a whole-VNet PUT whose body omits the
// subnets property entirely (as opposed to sending an explicit empty array)
// must leave existing subnets untouched.
func TestSDKVNetPutOmittedSubnetsPreservesExisting(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-preserve", "10.0.0.0/16")

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-preserve", "standalone", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("create standalone subnet: %v", err)
	}

	pollDone(t, subP)

	// A tags-only PUT whose body carries no subnets property at all (the
	// VirtualNetworkPropertiesFormat.Subnets field is left nil).
	p, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-preserve", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("tags-only PUT: %v", err)
	}

	pollDone(t, p)

	if _, err := subnets.Get(ctx, "rg-1", "vnet-preserve", "standalone", nil); err != nil {
		t.Fatalf("subnet standalone should survive an omitted-subnets PUT: %v", err)
	}
}

// Finding: CheckIPAddressAvailability mis-routed into the plain VNet-GET
// handler and returned a VNet body / zero result.
func TestSDKCheckIPAddressAvailability(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, _ := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	subnets, _ := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	nics, _ := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)

	createTestVNet(t, ctx, vnets, "rg-1", "vnet-checkip", "10.0.0.0/16")

	subP, err := subnets.BeginCreateOrUpdate(ctx, "rg-1", "vnet-checkip", "default", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	pollDone(t, subP)

	subnetID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-checkip/subnets/default"

	nicP, err := nics.BeginCreateOrUpdate(ctx, "rg-1", "nic-checkip", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					PrivateIPAddress:          to.Ptr("10.0.1.10"),
					PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
					Subnet:                    &armnetwork.Subnet{ID: to.Ptr(subnetID)},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nic: %v", err)
	}

	pollDone(t, nicP)

	taken, err := vnets.CheckIPAddressAvailability(ctx, "rg-1", "vnet-checkip", "10.0.1.10", nil)
	if err != nil {
		t.Fatalf("CheckIPAddressAvailability(taken): %v", err)
	}

	if taken.Available == nil || *taken.Available {
		t.Fatalf("CheckIPAddressAvailability(10.0.1.10).available = %v, want false (in use)", taken.Available)
	}

	if len(taken.AvailableIPAddresses) == 0 {
		t.Fatal("CheckIPAddressAvailability(taken) returned no alternate addresses")
	}

	free, err := vnets.CheckIPAddressAvailability(ctx, "rg-1", "vnet-checkip", "10.0.1.20", nil)
	if err != nil {
		t.Fatalf("CheckIPAddressAvailability(free): %v", err)
	}

	if free.Available == nil || !*free.Available {
		t.Fatalf("CheckIPAddressAvailability(10.0.1.20).available = %v, want true (free)", free.Available)
	}
}

// TestSDKNSGRePUTUpdatesTags guards the re-PUT idempotency fix for network
// security groups: a second CreateOrUpdate with changed tags must reflect on a
// subsequent GET (previously the found branch returned the NSG unchanged and
// dropped tag edits), while the security rules stay correct.
func TestSDKNSGRePUTUpdatesTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	nsgs, err := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	body := armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("dev")},
		Properties: &armnetwork.SecurityGroupPropertiesFormat{
			SecurityRules: []*armnetwork.SecurityRule{{
				Name: to.Ptr("allow-ssh"),
				Properties: &armnetwork.SecurityRulePropertiesFormat{
					Priority: to.Ptr(int32(100)), Direction: to.Ptr(armnetwork.SecurityRuleDirectionInbound),
					Access: to.Ptr(armnetwork.SecurityRuleAccessAllow), Protocol: to.Ptr(armnetwork.SecurityRuleProtocolTCP),
					SourceAddressPrefix: to.Ptr("*"), DestinationAddressPrefix: to.Ptr("*"),
					SourcePortRange: to.Ptr("*"), DestinationPortRange: to.Ptr("22"),
				},
			}},
		},
	}

	first, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-reput", body, nil)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	pollDone(t, first)

	// Re-PUT with a changed tag value and a new tag.
	body.Tags = map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("net")}

	second, err := nsgs.BeginCreateOrUpdate(ctx, "rg-1", "nsg-reput", body, nil)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}

	pollDone(t, second)

	got, err := nsgs.Get(ctx, "rg-1", "nsg-reput", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" {
		t.Errorf("tags[env]=%v want prod (updated by re-PUT)", got.Tags["env"])
	}

	if got.Tags["team"] == nil || *got.Tags["team"] != "net" {
		t.Errorf("tags[team]=%v want net (added by re-PUT)", got.Tags["team"])
	}

	// Rules must still be correct: the seeded allow-ssh rule survives.
	found := false

	if got.Properties != nil {
		for _, rule := range got.Properties.SecurityRules {
			if rule.Name != nil && *rule.Name == "allow-ssh" {
				found = true

				if rule.Properties == nil || rule.Properties.DestinationPortRange == nil ||
					*rule.Properties.DestinationPortRange != "22" {
					t.Errorf("allow-ssh destinationPortRange=%v want 22", rule.Properties)
				}
			}
		}
	}

	if !found {
		t.Error("allow-ssh security rule missing after re-PUT")
	}
}
