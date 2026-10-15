package vnet_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

func TestSDKStandardPublicIPMustBeStatic(t *testing.T) {
	ts := newVNetServer(t)
	opts := clientOpts(ts)

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	_, err = pips.BeginCreateOrUpdate(context.Background(), "rg-1", "pip-dyn", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
		},
	}, nil)

	var re *azcore.ResponseError
	if !errors.As(err, &re) || re.StatusCode != http.StatusBadRequest ||
		re.ErrorCode != "StandardSkuPublicIPAddressesCannotBeDynamic" {
		t.Fatalf("err = %v, want 400 StandardSkuPublicIPAddressesCannotBeDynamic", err)
	}
}

func TestSDKEmptyPeeringListIsArray(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	vnets, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	p, err := vnets.BeginCreateOrUpdate(ctx, "rg-1", "vn-empty", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.9.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create vnet: %v", err)
	}

	pollDone(t, p)

	resp, err := ts.Client().Get(ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/" +
		"virtualNetworks/vn-empty/virtualNetworkPeerings?api-version=2024-05-01")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if got := strings.TrimSpace(string(raw)); got != `{"value":[]}` {
		t.Fatalf("empty peering list = %q, want {\"value\":[]}", got)
	}
}
