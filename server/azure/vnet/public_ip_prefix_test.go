package vnet_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKPublicIPPrefixRoundTrip drives the real armnetwork PublicIPPrefixesClient
// through its Begin* pollers: create synthesizes an ipPrefix CIDR of the requested
// size, get/list round-trip it with the top-level sku, and delete makes a
// subsequent get 404. Every op used to fall through to the vnet 501 default.
func TestSDKPublicIPPrefixRoundTrip(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	client, err := armnetwork.NewPublicIPPrefixesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	const prefixLength = int32(28)

	cp, err := client.BeginCreateOrUpdate(ctx, "rg-1", "pfx-1", armnetwork.PublicIPPrefix{
		Location: to.Ptr("eastus"),
		SKU: &armnetwork.PublicIPPrefixSKU{
			Name: to.Ptr(armnetwork.PublicIPPrefixSKUNameStandard),
			Tier: to.Ptr(armnetwork.PublicIPPrefixSKUTierRegional),
		},
		Properties: &armnetwork.PublicIPPrefixPropertiesFormat{
			PrefixLength: to.Ptr(prefixLength),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created := pollDone(t, cp)

	if created.Name == nil || *created.Name != "pfx-1" {
		t.Fatalf("name=%v want pfx-1", created.Name)
	}

	if created.Properties == nil || created.Properties.IPPrefix == nil {
		t.Fatalf("missing synthesized ipPrefix: %+v", created.Properties)
	}

	assertPrefixSize(t, *created.Properties.IPPrefix, prefixLength)

	if created.Properties.PrefixLength == nil || *created.Properties.PrefixLength != prefixLength {
		t.Errorf("prefixLength=%v want %d", created.Properties.PrefixLength, prefixLength)
	}

	if created.SKU == nil || created.SKU.Name == nil || *created.SKU.Name != armnetwork.PublicIPPrefixSKUNameStandard {
		t.Errorf("sku name=%v want Standard", created.SKU)
	}

	if created.SKU == nil || created.SKU.Tier == nil || *created.SKU.Tier != armnetwork.PublicIPPrefixSKUTierRegional {
		t.Errorf("sku tier=%v want Regional", created.SKU)
	}

	// GET round-trips the synthesized CIDR unchanged.
	got, err := client.Get(ctx, "rg-1", "pfx-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.IPPrefix == nil ||
		*got.Properties.IPPrefix != *created.Properties.IPPrefix {
		t.Errorf("get ipPrefix=%v want %v", got.Properties, *created.Properties.IPPrefix)
	}

	// LIST includes the prefix.
	pager := client.NewListPager("rg-1", nil)

	var names []string

	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list: %v", perr)
		}

		for _, p := range page.Value {
			if p.Name != nil {
				names = append(names, *p.Name)
			}
		}
	}

	if len(names) != 1 || names[0] != "pfx-1" {
		t.Errorf("list=%v want [pfx-1]", names)
	}

	// DELETE, then GET should 404.
	dp, err := client.BeginDelete(ctx, "rg-1", "pfx-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	pollDone(t, dp)

	_, err = client.Get(ctx, "rg-1", "pfx-1", nil)
	if err == nil {
		t.Fatal("Get after delete: want error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Errorf("Get after delete: want 404, got %v", err)
	}
}

// TestSDKPublicIPPrefixPublicIPBackReference verifies the read-only
// publicIPAddresses[] back-reference: a public IP created with a publicIPPrefix
// ref shows up on the prefix's GET, and the public IP echoes the prefix.
func TestSDKPublicIPPrefixPublicIPBackReference(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	prefixes, err := armnetwork.NewPublicIPPrefixesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	cp, err := prefixes.BeginCreateOrUpdate(ctx, "rg-1", "pfx-ref", armnetwork.PublicIPPrefix{
		Location:   to.Ptr("eastus"),
		SKU:        &armnetwork.PublicIPPrefixSKU{Name: to.Ptr(armnetwork.PublicIPPrefixSKUNameStandard)},
		Properties: &armnetwork.PublicIPPrefixPropertiesFormat{PrefixLength: to.Ptr(int32(30))},
	}, nil)
	if err != nil {
		t.Fatalf("create prefix: %v", err)
	}

	pollDone(t, cp)

	prefixID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPPrefixes/pfx-ref"

	pips, err := armnetwork.NewPublicIPAddressesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pp, err := pips.BeginCreateOrUpdate(ctx, "rg-1", "pip-from-pfx", armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			PublicIPPrefix:           &armnetwork.SubResource{ID: to.Ptr(prefixID)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create pip: %v", err)
	}

	createdPIP := pollDone(t, pp)

	// The public IP echoes the prefix it was drawn from.
	if createdPIP.Properties == nil || createdPIP.Properties.PublicIPPrefix == nil ||
		createdPIP.Properties.PublicIPPrefix.ID == nil || *createdPIP.Properties.PublicIPPrefix.ID != prefixID {
		t.Errorf("pip publicIPPrefix=%v want %s", createdPIP.Properties, prefixID)
	}

	// The prefix's read-only publicIPAddresses[] lists the public IP.
	got, err := prefixes.Get(ctx, "rg-1", "pfx-ref", nil)
	if err != nil {
		t.Fatalf("get prefix: %v", err)
	}

	pipID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPAddresses/pip-from-pfx"

	if got.Properties == nil || len(got.Properties.PublicIPAddresses) != 1 ||
		got.Properties.PublicIPAddresses[0].ID == nil || *got.Properties.PublicIPAddresses[0].ID != pipID {
		t.Errorf("prefix publicIPAddresses=%+v want [%s]", got.Properties, pipID)
	}
}

// assertPrefixSize checks the CIDR mask equals prefixLength and the address is a
// well-formed dotted quad.
func assertPrefixSize(t *testing.T, cidr string, prefixLength int32) {
	t.Helper()

	addr, mask, ok := strings.Cut(cidr, "/")
	if !ok {
		t.Fatalf("ipPrefix %q is not a CIDR", cidr)
	}

	if mask != strconv.Itoa(int(prefixLength)) {
		t.Errorf("ipPrefix %q mask=%s want /%d", cidr, mask, prefixLength)
	}

	if strings.Count(addr, ".") != 3 {
		t.Errorf("ipPrefix %q address is not a dotted quad", cidr)
	}
}
