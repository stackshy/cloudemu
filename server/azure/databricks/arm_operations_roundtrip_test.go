package databricks_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

const (
	opNamePrefix = "Microsoft.Databricks/"
	opProvider   = "Microsoft.Databricks"
)

// listOperations exercises the subscription-less provider operations list end to
// end via the real armdatabricks OperationsClient. Note the client constructor
// takes NO subscription id: the SDK hits GET /providers/Microsoft.Databricks/operations
// with no /subscriptions/{sub} prefix, which the emulator handler special-cases.
func listOperations(t *testing.T) []*armdatabricks.Operation {
	t.Helper()

	opts, _ := newARMOptions(t)

	client, err := armdatabricks.NewOperationsClient(fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new operations client: %v", err)
	}

	ctx := context.Background()
	pager := client.NewListPager(nil)

	var ops []*armdatabricks.Operation

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		ops = append(ops, page.Value...)
	}

	return ops
}

func TestSDKOperationsList(t *testing.T) {
	ops := listOperations(t)

	if len(ops) == 0 {
		t.Fatal("expected a non-empty operations catalog")
	}

	for i, o := range ops {
		if o == nil {
			t.Fatalf("operation %d is nil", i)
		}

		if o.Name == nil || !strings.HasPrefix(*o.Name, opNamePrefix) {
			t.Fatalf("operation %d: name %v missing prefix %q", i, o.Name, opNamePrefix)
		}

		if o.Display == nil {
			t.Fatalf("operation %d (%s): missing display", i, *o.Name)
		}

		if o.Display.Provider == nil || *o.Display.Provider != opProvider {
			t.Fatalf("operation %d (%s): got provider %v, want %q", i, *o.Name, o.Display.Provider, opProvider)
		}

		if o.Display.Operation == nil || *o.Display.Operation == "" {
			t.Fatalf("operation %d (%s): empty display operation", i, *o.Name)
		}
	}
}

func TestSDKOperationsCatalogContainsKnownEntries(t *testing.T) {
	ops := listOperations(t)

	got := make(map[string]bool, len(ops))

	for _, o := range ops {
		if o != nil && o.Name != nil {
			got[*o.Name] = true
		}
	}

	want := []string{
		"Microsoft.Databricks/workspaces/read",
		"Microsoft.Databricks/workspaces/write",
		"Microsoft.Databricks/accessConnectors/write",
	}

	for _, name := range want {
		if !got[name] {
			t.Errorf("operations catalog missing %q", name)
		}
	}
}
