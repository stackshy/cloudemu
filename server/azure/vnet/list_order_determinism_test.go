package vnet_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKListOrderDeterministic confirms repeated identical LIST calls return
// the virtual networks in the same order every time. Real ARM list endpoints
// return a stable order; before the driver iterated SortedValues (not the
// random-order map All()), each call reshuffled the result, which a Terraform
// refresh or a paging client reads as spurious drift.
func TestSDKListOrderDeterministic(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()

	client, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"} {
		p, cerr := client.BeginCreateOrUpdate(ctx, "rg-1", n, armnetwork.VirtualNetwork{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			},
		}, nil)
		if cerr != nil {
			t.Fatalf("create %s: %v", n, cerr)
		}

		pollDone(t, p)
	}

	listOnce := func() []string {
		var out []string

		pager := client.NewListPager("rg-1", nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				t.Fatalf("list: %v", perr)
			}

			for _, v := range page.Value {
				out = append(out, *v.Name)
			}
		}

		return out
	}

	first := fmt.Sprint(listOnce())

	for i := 0; i < 20; i++ {
		if got := fmt.Sprint(listOnce()); got != first {
			t.Fatalf("LIST order nondeterministic: call0=%s callN=%s", first, got)
		}
	}
}
