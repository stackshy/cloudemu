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

// TestSDKVNetResourceGUID verifies that a virtual network carries a non-empty
// properties.resourceGuid — the ARM-persisted identifier real Azure assigns on
// create — and that it stays stable across a repeat PUT (the same resource
// updated in place keeps its identity), while a differently-named vnet gets
// its own distinct GUID.
func TestSDKVNetResourceGUID(t *testing.T) {
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

	create := func(name, cidr string) *string {
		poller, cerr := vnetClient.BeginCreateOrUpdate(ctx, "rg-1", name,
			armnetwork.VirtualNetwork{
				Location: to.Ptr("eastus"),
				Properties: &armnetwork.VirtualNetworkPropertiesFormat{
					AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr(cidr)}},
				},
			}, nil)
		if cerr != nil {
			t.Fatalf("vnet BeginCreateOrUpdate(%s): %v", name, cerr)
		}

		res, perr := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
		if perr != nil {
			t.Fatalf("vnet poll(%s): %v", name, perr)
		}

		if res.Properties == nil || res.Properties.ResourceGUID == nil || *res.Properties.ResourceGUID == "" {
			t.Fatalf("vnet %s: resourceGuid missing or empty, properties=%+v", name, res.Properties)
		}

		return res.Properties.ResourceGUID
	}

	first := *create("vnet-guid-1", "10.0.0.0/16")

	// A repeat PUT (tag-only change) must preserve the same resourceGuid.
	poller, err := vnetClient.BeginCreateOrUpdate(ctx, "rg-1", "vnet-guid-1",
		armnetwork.VirtualNetwork{
			Location: to.Ptr("eastus"),
			Tags:     map[string]*string{"env": to.Ptr("test")},
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			},
		}, nil)
	if err != nil {
		t.Fatalf("vnet update BeginCreateOrUpdate: %v", err)
	}

	updated, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("vnet update poll: %v", err)
	}

	if updated.Properties == nil || updated.Properties.ResourceGUID == nil || *updated.Properties.ResourceGUID != first {
		t.Errorf("resourceGuid not preserved across update: before=%s after=%+v", first, updated.Properties)
	}

	second := *create("vnet-guid-2", "10.1.0.0/16")
	if second == first {
		t.Errorf("two distinct vnets got the same resourceGuid %s", first)
	}

	// A GET independently reports the same stable value.
	got, err := vnetClient.Get(ctx, "rg-1", "vnet-guid-1", nil)
	if err != nil {
		t.Fatalf("vnet Get: %v", err)
	}

	if got.Properties == nil || got.Properties.ResourceGUID == nil || *got.Properties.ResourceGUID != first {
		t.Errorf("GET resourceGuid=%+v want %s", got.Properties, first)
	}
}

// TestSDKNSGResourceGUID mirrors TestSDKVNetResourceGUID for network security
// groups: non-empty on create, stable across a repeat PUT.
func TestSDKNSGResourceGUID(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	nsgClient, err := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	poller, err := nsgClient.BeginCreateOrUpdate(ctx, "rg-1", "nsg-guid-1",
		armnetwork.SecurityGroup{Location: to.Ptr("eastus")}, nil)
	if err != nil {
		t.Fatalf("nsg BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("nsg poll: %v", err)
	}

	if created.Properties == nil || created.Properties.ResourceGUID == nil || *created.Properties.ResourceGUID == "" {
		t.Fatalf("nsg resourceGuid missing or empty, properties=%+v", created.Properties)
	}

	first := *created.Properties.ResourceGUID

	updatePoller, err := nsgClient.BeginCreateOrUpdate(ctx, "rg-1", "nsg-guid-1",
		armnetwork.SecurityGroup{
			Location: to.Ptr("eastus"),
			Tags:     map[string]*string{"env": to.Ptr("test")},
		}, nil)
	if err != nil {
		t.Fatalf("nsg update BeginCreateOrUpdate: %v", err)
	}

	updated, err := updatePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("nsg update poll: %v", err)
	}

	if updated.Properties == nil || updated.Properties.ResourceGUID == nil || *updated.Properties.ResourceGUID != first {
		t.Errorf("nsg resourceGuid not preserved across update: before=%s after=%+v", first, updated.Properties)
	}
}

// TestSDKPublicIPResourceGUID mirrors TestSDKVNetResourceGUID for public IP
// addresses: non-empty on create, stable across a tags-only PATCH.
func TestSDKPublicIPResourceGUID(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	pipClient, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	poller, err := pipClient.BeginCreateOrUpdate(ctx, "rg-1", "pip-guid-1",
		armnetwork.PublicIPAddress{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			},
		}, nil)
	if err != nil {
		t.Fatalf("publicIP BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("publicIP poll: %v", err)
	}

	if created.Properties == nil || created.Properties.ResourceGUID == nil || *created.Properties.ResourceGUID == "" {
		t.Fatalf("publicIP resourceGuid missing or empty, properties=%+v", created.Properties)
	}

	first := *created.Properties.ResourceGUID

	if _, err := pipClient.UpdateTags(ctx, "rg-1", "pip-guid-1",
		armnetwork.TagsObject{Tags: map[string]*string{"env": to.Ptr("test")}}, nil); err != nil {
		t.Fatalf("publicIP UpdateTags: %v", err)
	}

	got, err := pipClient.Get(ctx, "rg-1", "pip-guid-1", nil)
	if err != nil {
		t.Fatalf("publicIP Get: %v", err)
	}

	if got.Properties == nil || got.Properties.ResourceGUID == nil || *got.Properties.ResourceGUID != first {
		t.Errorf("publicIP resourceGuid not preserved across PATCH: before=%s after=%+v", first, got.Properties)
	}
}
