// Deep-audit regression (N5): Microsoft.Network resources (virtual networks,
// network security groups, subnets) were globally name-addressable, so a GET or
// DELETE under the WRONG resource group returned 200 and even echoed the wrong
// group back into the response id. A resource must be scoped to the group it was
// created under: a read under a different group is a 404, and a same-named
// resource in a second group is an independent object.

package vnet_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func statusOf(t *testing.T, err error) int {
	t.Helper()

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected an azcore.ResponseError, got %v", err)
	}

	return respErr.StatusCode
}

// TestVNetGetIsResourceGroupScoped proves a virtual network created in one
// resource group is not readable — and not deletable — under another, and that
// a same-named vnet in a second group is a distinct resource with its own id.
func TestVNetGetIsResourceGroupScoped(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Network: cloudP.VNet}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	body := armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}

	p, cerr := client.BeginCreateOrUpdate(ctx, "rgA", "vx", body, nil)
	if cerr != nil {
		t.Fatalf("create in rgA: %v", cerr)
	}

	pollDone(t, p)

	// GET under the wrong resource group is a 404, not a wrong-group 200.
	if _, gerr := client.Get(ctx, "rgB", "vx", nil); gerr == nil {
		t.Fatal("GET vx under rgB returned success; expected 404 (cross-RG read)")
	} else if code := statusOf(t, gerr); code != 404 {
		t.Fatalf("GET vx under rgB: status %d, want 404", code)
	}

	// GET under the right group works and reports rgA in its id.
	got, gerr := client.Get(ctx, "rgA", "vx", nil)
	if gerr != nil {
		t.Fatalf("GET vx under rgA: %v", gerr)
	}

	if got.ID == nil || !strings.Contains(strings.ToLower(*got.ID), "/resourcegroups/rga/") {
		t.Fatalf("GET vx under rgA: id %v does not carry rgA", got.ID)
	}

	// A same-named vnet in a second group is an independent resource.
	p2, cerr := client.BeginCreateOrUpdate(ctx, "rgB", "vx", body, nil)
	if cerr != nil {
		t.Fatalf("create in rgB: %v", cerr)
	}

	pollDone(t, p2)

	if _, gerr := client.Get(ctx, "rgB", "vx", nil); gerr != nil {
		t.Fatalf("GET vx under rgB after create: %v", gerr)
	}

	// An RG-scoped list returns only that group's vnet, not the same-named one
	// from the other group.
	var rgAvnets int

	pager := client.NewListPager("rgA", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list rgA: %v", perr)
		}

		for _, v := range page.Value {
			if v.Name != nil && *v.Name == "vx" {
				rgAvnets++
			}
		}
	}

	if rgAvnets != 1 {
		t.Fatalf("RG-scoped list of rgA returned %d vx entries, want 1", rgAvnets)
	}

	// DELETE under the wrong group must not remove the other group's vnet.
	if _, derr := client.BeginDelete(ctx, "rgB", "vx", nil); derr != nil {
		t.Fatalf("delete vx under rgB: %v", derr)
	}

	if _, gerr := client.Get(ctx, "rgA", "vx", nil); gerr != nil {
		t.Fatalf("vx in rgA was removed by deleting vx in rgB: %v", gerr)
	}
}

// TestNSGGetIsResourceGroupScoped proves the same resource-group scoping for
// network security groups.
func TestNSGGetIsResourceGroupScoped(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Network: cloudP.VNet}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	p, cerr := client.BeginCreateOrUpdate(ctx, "rgA", "nsgx", armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
	}, nil)
	if cerr != nil {
		t.Fatalf("create nsg in rgA: %v", cerr)
	}

	pollDone(t, p)

	if _, gerr := client.Get(ctx, "rgB", "nsgx", nil); gerr == nil {
		t.Fatal("GET nsgx under rgB returned success; expected 404 (cross-RG read)")
	} else if code := statusOf(t, gerr); code != 404 {
		t.Fatalf("GET nsgx under rgB: status %d, want 404", code)
	}

	if _, gerr := client.Get(ctx, "rgA", "nsgx", nil); gerr != nil {
		t.Fatalf("GET nsgx under rgA: %v", gerr)
	}
}
