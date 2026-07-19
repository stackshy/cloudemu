package notificationhubs_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"
)

func TestSDKListOrderingDeterministic(t *testing.T) {
	namespaces, _ := newClients(t)
	ctx := context.Background()

	// Create namespaces deliberately out of alphabetical order.
	for _, name := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if _, err := namespaces.CreateOrUpdate(ctx, testRG, name, armnotificationhubs.NamespaceCreateOrUpdateParameters{
			Location: to.Ptr("global"),
		}, nil); err != nil {
			t.Fatalf("Namespaces.CreateOrUpdate(%s): %v", name, err)
		}
	}

	listNames := func() []string {
		var names []string

		pager := namespaces.NewListPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				t.Fatalf("Namespaces.List: %v", perr)
			}

			for _, ns := range page.Value {
				names = append(names, *ns.Name)
			}
		}

		return names
	}

	first := listNames()
	if len(first) != 5 {
		t.Fatalf("first list = %v, want 5 namespaces", first)
	}

	for i := 0; i < 4; i++ {
		got := listNames()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("list #%d order = %v, want %v", i+2, got, first)
		}
	}
}
