package servicebus_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newNamespacesClient(t *testing.T, ts *httptest.Server) *armservicebus.NamespacesClient {
	t.Helper()

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}
	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: myCloud, Transport: ts.Client(),
		Retry: policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armservicebus.NewClientFactory(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("ClientFactory: %v", err)
	}
	return cf.NewNamespacesClient()
}

func createNS(t *testing.T, c *armservicebus.NamespacesClient, rg, name string, tags map[string]*string) {
	t.Helper()
	ctx := context.Background()
	poller, err := c.BeginCreateOrUpdate(ctx, rg, name, armservicebus.SBNamespace{
		Location: to.Ptr("eastus"),
		Tags:     tags,
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s/%s: %v", rg, name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll %s/%s: %v", rg, name, err)
	}
}

// TestSDKNamespaceTagsAndScopedListing is the regression for #278: namespaces
// created with tags must return them on Get, and ListByResourceGroup must
// return only the namespaces in that resource group.
func TestSDKNamespaceTagsAndScopedListing(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{ServiceBus: cloudP.ServiceBus})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	c := newNamespacesClient(t, ts)
	ctx := context.Background()

	createNS(t, c, "rg-a", "ns-a", map[string]*string{"env": to.Ptr("prod")})
	createNS(t, c, "rg-a", "ns-a2", nil)
	createNS(t, c, "rg-b", "ns-b", nil)

	// Tags survive create → Get.
	got, err := c.Get(ctx, "rg-a", "ns-a", nil)
	if err != nil {
		t.Fatalf("Get ns-a: %v", err)
	}
	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" {
		t.Fatalf("ns-a tags = %v, want env=prod", got.Tags)
	}

	// ListByResourceGroup is scoped to its resource group.
	listRG := func(rg string) []string {
		var names []string
		pager := c.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("list %s: %v", rg, err)
			}
			for _, ns := range page.Value {
				names = append(names, *ns.Name)
			}
		}
		return names
	}

	gotA := listRG("rg-a")
	if len(gotA) != 2 {
		t.Fatalf("rg-a list = %v, want its own 2 namespaces", gotA)
	}
	gotB := listRG("rg-b")
	if len(gotB) != 1 || gotB[0] != "ns-b" {
		t.Fatalf("rg-b list = %v, want [ns-b]", gotB)
	}

	// Get from the wrong resource group must 404 — ns-a lives in rg-a.
	if _, err := c.Get(ctx, "rg-b", "ns-a", nil); err == nil {
		t.Fatal("Get ns-a via rg-b returned nil error, want NotFound")
	}

	// Delete from the wrong resource group must not remove the namespace.
	wrongDel, err := c.BeginDelete(ctx, "rg-b", "ns-a", nil)
	if err == nil {
		_, _ = wrongDel.PollUntilDone(ctx, nil)
	}
	if _, err := c.Get(ctx, "rg-a", "ns-a", nil); err != nil {
		t.Fatalf("ns-a should survive a wrong-group delete, but Get failed: %v", err)
	}

	// A deleted namespace disappears from Get.
	delPoller, err := c.BeginDelete(ctx, "rg-b", "ns-b", nil)
	if err != nil {
		t.Fatalf("BeginDelete ns-b: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete ns-b: %v", err)
	}
	if _, err := c.Get(ctx, "rg-b", "ns-b", nil); err == nil {
		t.Fatal("Get after delete returned nil error, want NotFound")
	}
}
