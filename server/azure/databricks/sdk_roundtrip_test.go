package databricks_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	testRG  = "rg-1"
	testWS  = "my-workspace"
	managed = "/subscriptions/sub-1/resourceGroups/databricks-managed-rg"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newWorkspacesClient(t *testing.T) *armdatabricks.WorkspacesClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Databricks: cloudP.Databricks})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armdatabricks.NewWorkspacesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return client
}

func createWorkspace(t *testing.T, client *armdatabricks.WorkspacesClient) armdatabricks.Workspace {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, testWS, armdatabricks.Workspace{
		Location: to.Ptr("eastus"),
		SKU:      &armdatabricks.SKU{Name: to.Ptr("premium")},
		Tags:     map[string]*string{"env": to.Ptr("test")},
		Properties: &armdatabricks.WorkspaceProperties{
			ManagedResourceGroupID: to.Ptr(managed),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	return res.Workspace
}

func TestSDKWorkspaceLifecycle(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	ws := createWorkspace(t, client)

	if *ws.Name != testWS {
		t.Fatalf("got name %q, want %q", *ws.Name, testWS)
	}

	if ws.Properties == nil || *ws.Properties.ProvisioningState != armdatabricks.ProvisioningStateSucceeded {
		t.Fatalf("expected Succeeded provisioning state, got %+v", ws.Properties)
	}

	if ws.Properties.WorkspaceURL == nil || *ws.Properties.WorkspaceURL == "" {
		t.Fatal("expected a workspace URL")
	}

	got, err := client.Get(ctx, testRG, testWS, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if *got.Properties.ManagedResourceGroupID != managed {
		t.Fatalf("got managed RG %q, want %q", *got.Properties.ManagedResourceGroupID, managed)
	}

	updatePoller, err := client.BeginUpdate(ctx, testRG, testWS, armdatabricks.WorkspaceUpdate{
		Tags: map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("data")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	updated, err := updatePoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("update PollUntilDone: %v", err)
	}

	if *updated.Tags["env"] != "prod" {
		t.Fatalf("expected updated tag env=prod, got %v", updated.Tags)
	}

	delPoller, err := client.BeginDelete(ctx, testRG, testWS, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PollUntilDone: %v", err)
	}

	if _, err = client.Get(ctx, testRG, testWS, nil); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSDKListWorkspaces(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	createWorkspace(t, client)

	byRG := client.NewListByResourceGroupPager(testRG, nil)

	page, err := byRG.NextPage(ctx)
	if err != nil {
		t.Fatalf("ListByResourceGroup: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d workspaces in RG, want 1", len(page.Value))
	}

	bySub := client.NewListBySubscriptionPager(nil)

	subPage, err := bySub.NextPage(ctx)
	if err != nil {
		t.Fatalf("ListBySubscription: %v", err)
	}

	if len(subPage.Value) != 1 {
		t.Fatalf("got %d workspaces in subscription, want 1", len(subPage.Value))
	}
}

func TestSDKGetWorkspaceNotFound(t *testing.T) {
	client := newWorkspacesClient(t)

	_, err := client.Get(context.Background(), testRG, "does-not-exist", nil)
	if err == nil {
		t.Fatal("expected error for missing workspace")
	}
}
