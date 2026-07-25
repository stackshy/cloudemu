package dns_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

func TestSDKListOrderingDeterministic(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	// Create zones deliberately out of alphabetical order.
	for _, name := range []string{"zeta.com", "alpha.com", "mid.com", "beta.com", "omega.com"} {
		if _, err := zones.CreateOrUpdate(ctx, testRG, name, armdns.Zone{
			Location: to.Ptr("global"),
		}, nil); err != nil {
			t.Fatalf("Zones.CreateOrUpdate(%s): %v", name, err)
		}
	}

	listNames := func() []string {
		var names []string

		pager := zones.NewListByResourceGroupPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				t.Fatalf("ListByResourceGroup: %v", perr)
			}

			for _, z := range page.Value {
				names = append(names, *z.Name)
			}
		}

		return names
	}

	first := listNames()
	if len(first) != 5 {
		t.Fatalf("first list = %v, want 5 zones", first)
	}

	for i := 0; i < 4; i++ {
		got := listNames()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("list #%d order = %v, want %v", i+2, got, first)
		}
	}
}
