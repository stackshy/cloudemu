package loganalytics_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
)

func TestSDKListOrderingDeterministic(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	// Create workspaces deliberately out of alphabetical order.
	for _, name := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		poller, err := client.BeginCreateOrUpdate(ctx, testRG, name, armoperationalinsights.Workspace{
			Location: to.Ptr("eastus"),
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

		pager := client.NewListByResourceGroupPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				t.Fatalf("ListByResourceGroup: %v", perr)
			}

			for _, ws := range page.Value {
				names = append(names, *ws.Name)
			}
		}

		return names
	}

	first := listNames()
	if len(first) != 5 {
		t.Fatalf("first list = %v, want 5 workspaces", first)
	}

	for i := 0; i < 4; i++ {
		got := listNames()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("list #%d order = %v, want %v", i+2, got, first)
		}
	}
}
