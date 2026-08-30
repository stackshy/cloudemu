package servicebus_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"
)

// TestSDKListQueuesPaginated is the regression for the unresolvable nextLink
// placeholder: a namespace with more than one page of queues (>100) must be
// fully enumerable by the armservicebus pager, which follows each nextLink as an
// absolute URL. Every created queue must come back exactly once across the
// pages, and the listing must actually span more than a single page.
func TestSDKListQueuesPaginated(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	createNS(t, cf.NewNamespacesClient(), rgName, nsName, nil)

	queues := cf.NewQueuesClient()

	const total = 150
	for i := range total {
		name := fmt.Sprintf("q-%03d", i)
		if _, err := queues.CreateOrUpdate(ctx, rgName, nsName, name,
			armservicebus.SBQueue{}, nil); err != nil {
			t.Fatalf("create queue %s: %v", name, err)
		}
	}

	seen := map[string]int{}
	pages := 0

	pager := queues.NewListByNamespacePager(rgName, nsName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list page %d: %v", pages, err)
		}

		pages++

		for _, q := range page.Value {
			if q.Name == nil {
				t.Fatal("queue in page has nil name")
			}

			seen[*q.Name]++
		}
	}

	if pages < 2 {
		t.Fatalf("listed %d queues in %d page(s); want the collection to span multiple pages", len(seen), pages)
	}

	if len(seen) != total {
		t.Fatalf("saw %d distinct queues across %d pages, want %d", len(seen), pages, total)
	}

	for i := range total {
		name := fmt.Sprintf("q-%03d", i)
		if seen[name] != 1 {
			t.Fatalf("queue %s appeared %d times across pages, want exactly 1", name, seen[name])
		}
	}
}
