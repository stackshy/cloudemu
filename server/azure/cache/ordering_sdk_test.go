package cache_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
)

func TestSDKListOrderingDeterministic(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	// Create caches deliberately out of alphabetical order.
	for _, name := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		poller, err := client.BeginCreate(ctx, testRG, name, armredis.CreateParameters{
			Location: to.Ptr("eastus"),
			Properties: &armredis.CreateProperties{
				SKU: &armredis.SKU{
					Name:     to.Ptr(armredis.SKUNameStandard),
					Family:   to.Ptr(armredis.SKUFamilyC),
					Capacity: to.Ptr(int32(1)),
				},
			},
		}, nil)
		if err != nil {
			t.Fatalf("BeginCreate(%s): %v", name, err)
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("PollUntilDone(%s): %v", name, err)
		}
	}

	listNames := func() []string {
		var names []string

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				t.Fatalf("ListBySubscription: %v", perr)
			}

			for _, c := range page.Value {
				names = append(names, *c.Name)
			}
		}

		return names
	}

	first := listNames()
	if len(first) != 5 {
		t.Fatalf("first list = %v, want 5 caches", first)
	}

	for i := 0; i < 4; i++ {
		got := listNames()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("list #%d order = %v, want %v", i+2, got, first)
		}
	}
}
