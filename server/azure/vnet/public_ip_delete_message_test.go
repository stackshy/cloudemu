package vnet_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKPublicIPDeleteInUseNamesResource checks the error for deleting a
// NAT-gateway-bound public IP is a 400 PublicIPAddressCannotBeDeleted naming the ARM
// resource id, not the internal allocation id.
func TestSDKPublicIPDeleteInUseNamesResource(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pp, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-busy", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create pip: %v", err)
	}

	pollDone(t, pp)

	pipID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPAddresses/pip-busy"

	nats, err := armnetwork.NewNatGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	np, err := nats.BeginCreateOrUpdate(ctx, "rg-1", "natgw-busy", armnetwork.NatGateway{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
		Properties: &armnetwork.NatGatewayPropertiesFormat{
			PublicIPAddresses: []*armnetwork.SubResource{{ID: to.Ptr(pipID)}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nat: %v", err)
	}

	pollDone(t, np)

	_, err = pips.BeginDelete(ctx, "rg-1", "pip-busy", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("want ResponseError, got %v", err)
	}

	if respErr.StatusCode != 400 || respErr.ErrorCode != "PublicIPAddressCannotBeDeleted" {
		t.Errorf("got %d %s, want 400 PublicIPAddressCannotBeDeleted", respErr.StatusCode, respErr.ErrorCode)
	}

	if strings.Contains(respErr.Error(), "ipalloc-") || !strings.Contains(respErr.Error(), pipID) {
		t.Errorf("message should name %s and not an internal id: %v", pipID, respErr)
	}
}
