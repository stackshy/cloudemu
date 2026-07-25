package eventgrid_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

func TestSDKListOrderingDeterministic(t *testing.T) {
	topics := newTopicsClient(t)
	ctx := context.Background()

	// Create topics deliberately out of alphabetical order.
	for _, name := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		poller, err := topics.BeginCreateOrUpdate(ctx, testRG, name, armeventgrid.Topic{
			Location: to.Ptr("global"),
		}, nil)
		if err != nil {
			t.Fatalf("BeginCreateOrUpdate(%s): %v", name, err)
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("PollUntilDone(%s): %v", name, err)
		}
	}

	listNames := func() []string {
		var names []string

		pager := topics.NewListByResourceGroupPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				t.Fatalf("ListByResourceGroup: %v", perr)
			}

			for _, tp := range page.Value {
				names = append(names, *tp.Name)
			}
		}

		return names
	}

	first := listNames()
	if len(first) != 5 {
		t.Fatalf("first list = %v, want 5 topics", first)
	}

	for i := 0; i < 4; i++ {
		got := listNames()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("list #%d order = %v, want %v", i+2, got, first)
		}
	}
}
