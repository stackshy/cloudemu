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

// TestSDKNetworkInterfaceRoundTrip drives the real armnetwork InterfacesClient
// through the subnet→NIC bridge from the audit: create a vnet + subnet, then a
// NIC that references the subnet, and confirm a private IP is allocated from
// the subnet prefix, the resource round-trips, PUT is idempotent, and delete
// works.
func TestSDKNetworkInterfaceRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)
	poll := &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}

	// Seed a vnet + subnet the NIC will bind to.
	vnetClient, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	vp, err := vnetClient.BeginCreateOrUpdate(ctx, "rg-1", "vnet-1", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("vnet create: %v", err)
	}

	if _, err = vp.PollUntilDone(ctx, poll); err != nil {
		t.Fatalf("vnet poll: %v", err)
	}

	subnetClient, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	sp, err := subnetClient.BeginCreateOrUpdate(ctx, "rg-1", "vnet-1", "subnet-1", armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.1.0/24")},
	}, nil)
	if err != nil {
		t.Fatalf("subnet create: %v", err)
	}

	if _, err = sp.PollUntilDone(ctx, poll); err != nil {
		t.Fatalf("subnet poll: %v", err)
	}

	subnetID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-1/subnets/subnet-1"

	nicClient, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	nicBody := armnetwork.Interface{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet:                    &armnetwork.Subnet{ID: to.Ptr(subnetID)},
					PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
					Primary:                   to.Ptr(true),
				},
			}},
		},
	}

	np, err := nicClient.BeginCreateOrUpdate(ctx, "rg-1", "nic1", nicBody, nil)
	if err != nil {
		t.Fatalf("nic BeginCreateOrUpdate: %v", err)
	}

	created, err := np.PollUntilDone(ctx, poll)
	if err != nil {
		t.Fatalf("nic poll: %v", err)
	}

	if created.Name == nil || *created.Name != "nic1" {
		t.Fatalf("name=%v want nic1", created.Name)
	}

	if created.Location == nil || *created.Location != "westus2" {
		t.Errorf("location=%v want westus2 (submitted region must round-trip)", created.Location)
	}

	if got := provisioningState(created.Properties); got != "Succeeded" {
		t.Errorf("provisioningState=%q want Succeeded", got)
	}

	ip := privateIP(created.Properties)
	if ip != "10.0.1.4" {
		t.Errorf("privateIPAddress=%q want 10.0.1.4 (first assignable host in 10.0.1.0/24)", ip)
	}

	if sid := subnetRef(created.Properties); sid != subnetID {
		t.Errorf("ipConfig subnet id=%q want %q", sid, subnetID)
	}

	// GET round-trips the same NIC + allocated IP.
	got, err := nicClient.Get(ctx, "rg-1", "nic1", nil)
	if err != nil {
		t.Fatalf("nic Get: %v", err)
	}

	if privateIP(got.Interface.Properties) != "10.0.1.4" {
		t.Errorf("GET privateIP=%q want 10.0.1.4", privateIP(got.Interface.Properties))
	}

	if got.Interface.Tags["env"] == nil || *got.Interface.Tags["env"] != "prod" {
		t.Errorf("GET tags[env]=%v want prod", got.Interface.Tags["env"])
	}

	// Idempotent PUT: a second create-or-update updates in place, no duplicate.
	np2, err := nicClient.BeginCreateOrUpdate(ctx, "rg-1", "nic1", nicBody, nil)
	if err != nil {
		t.Fatalf("nic re-PUT: %v", err)
	}

	if _, err = np2.PollUntilDone(ctx, poll); err != nil {
		t.Fatalf("nic re-PUT poll: %v", err)
	}

	count := 0
	pager := nicClient.NewListPager("rg-1", nil)

	for pager.More() {
		page, pErr := pager.NextPage(ctx)
		if pErr != nil {
			t.Fatalf("nic list: %v", pErr)
		}

		count += len(page.Value)
	}

	if count != 1 {
		t.Errorf("List after idempotent re-PUT returned %d NICs, want 1", count)
	}

	// Delete.
	dp, err := nicClient.BeginDelete(ctx, "rg-1", "nic1", nil)
	if err != nil {
		t.Fatalf("nic BeginDelete: %v", err)
	}

	if _, err = dp.PollUntilDone(ctx, poll); err != nil {
		t.Fatalf("nic delete poll: %v", err)
	}

	if _, err = nicClient.Get(ctx, "rg-1", "nic1", nil); err == nil {
		t.Error("Get after delete succeeded, want NotFound")
	}
}

// TestSDKNetworkInterfaceCrossVnetSubnetScoping guards the fix for subnet names
// that collide across vnets: a NIC that references vnet-b's "subnet-1" must get
// an IP from vnet-b's address space, not vnet-a's.
func TestSDKNetworkInterfaceCrossVnetSubnetScoping(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)
	poll := &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}

	vnetClient, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	subnetClient, err := armnetwork.NewSubnetsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Two vnets, each with a subnet named "subnet-1" but in different ranges.
	for _, v := range []struct{ name, vnetCIDR, subnetCIDR string }{
		{"vnet-a", "10.0.0.0/16", "10.0.1.0/24"},
		{"vnet-b", "10.9.0.0/16", "10.9.1.0/24"},
	} {
		vp, cErr := vnetClient.BeginCreateOrUpdate(ctx, "rg-1", v.name, armnetwork.VirtualNetwork{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr(v.vnetCIDR)}},
			},
		}, nil)
		if cErr != nil {
			t.Fatalf("%s create: %v", v.name, cErr)
		}

		if _, cErr = vp.PollUntilDone(ctx, poll); cErr != nil {
			t.Fatalf("%s poll: %v", v.name, cErr)
		}

		sp, sErr := subnetClient.BeginCreateOrUpdate(ctx, "rg-1", v.name, "subnet-1", armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr(v.subnetCIDR)},
		}, nil)
		if sErr != nil {
			t.Fatalf("%s subnet create: %v", v.name, sErr)
		}

		if _, sErr = sp.PollUntilDone(ctx, poll); sErr != nil {
			t.Fatalf("%s subnet poll: %v", v.name, sErr)
		}
	}

	nicClient, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	subnetBID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-b/subnets/subnet-1"

	np, err := nicClient.BeginCreateOrUpdate(ctx, "rg-1", "nic-b", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet: &armnetwork.Subnet{ID: to.Ptr(subnetBID)},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("nic create: %v", err)
	}

	created, err := np.PollUntilDone(ctx, poll)
	if err != nil {
		t.Fatalf("nic poll: %v", err)
	}

	if ip := privateIP(created.Properties); ip != "10.9.1.4" {
		t.Errorf("privateIP=%q want 10.9.1.4 (from vnet-b's subnet, not vnet-a's 10.0.1.x)", ip)
	}
}

// TestSDKNetworkInterfaceUnknownSubnet confirms a NIC referencing a subnet that
// does not exist is rejected rather than silently created with no IP.
func TestSDKNetworkInterfaceUnknownSubnet(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	nicClient, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	ghost := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vnet-x/subnets/nope"

	_, err = nicClient.BeginCreateOrUpdate(ctx, "rg-1", "nic-bad", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet: &armnetwork.Subnet{ID: to.Ptr(ghost)},
				},
			}},
		},
	}, nil)
	if err == nil {
		t.Fatal("NIC create against a missing subnet succeeded, want error")
	}
}

// Helpers to read nested NIC response fields without repeating nil checks.

func provisioningState(p *armnetwork.InterfacePropertiesFormat) string {
	if p == nil || p.ProvisioningState == nil {
		return ""
	}

	return string(*p.ProvisioningState)
}

func privateIP(p *armnetwork.InterfacePropertiesFormat) string {
	if p == nil || len(p.IPConfigurations) == 0 {
		return ""
	}

	ipc := p.IPConfigurations[0]
	if ipc.Properties == nil || ipc.Properties.PrivateIPAddress == nil {
		return ""
	}

	return *ipc.Properties.PrivateIPAddress
}

func subnetRef(p *armnetwork.InterfacePropertiesFormat) string {
	if p == nil || len(p.IPConfigurations) == 0 {
		return ""
	}

	ipc := p.IPConfigurations[0]
	if ipc.Properties == nil || ipc.Properties.Subnet == nil || ipc.Properties.Subnet.ID == nil {
		return ""
	}

	return *ipc.Properties.Subnet.ID
}
