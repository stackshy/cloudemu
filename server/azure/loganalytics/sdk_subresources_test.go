package loganalytics_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newTestOptions stands up a server backed by a fresh Azure Log Analytics driver
// and returns arm.ClientOptions pointed at it, so any armoperationalinsights
// client can be built against the same estate.
func newTestOptions(t *testing.T) *arm.ClientOptions {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{LogAnalytics: cloudP.LogAnalytics})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

func createWorkspace(t *testing.T, client *armoperationalinsights.WorkspacesClient, name, location string) {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, name, armoperationalinsights.Workspace{
		Location: to.Ptr(location),
		Properties: &armoperationalinsights.WorkspaceProperties{
			SKU: &armoperationalinsights.WorkspaceSKU{Name: to.Ptr(armoperationalinsights.WorkspaceSKUNameEnumPerGB2018)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate(%s): %v", name, err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone(%s): %v", name, err)
	}
}

// TestSDKWorkspaceLocationSKUCustomerID covers findings 4 (location echoed on
// Get), 8 (customerId is a real GUID, not the ARM id) and 11 (SKU defaulting).
func TestSDKWorkspaceLocationSKUCustomerID(t *testing.T) {
	opts := newTestOptions(t)
	ctx := context.Background()

	client, err := armoperationalinsights.NewWorkspacesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewWorkspacesClient: %v", err)
	}

	createWorkspace(t, client, "ws-meta", "eastus2")

	got, err := client.Get(ctx, testRG, "ws-meta", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Location == nil || *got.Location != "eastus2" {
		t.Fatalf("location = %v, want eastus2", got.Location)
	}

	if got.Properties == nil || got.Properties.SKU == nil || got.Properties.SKU.Name == nil ||
		string(*got.Properties.SKU.Name) != "PerGB2018" {
		t.Fatalf("sku = %+v, want PerGB2018", got.Properties)
	}

	cid := ""
	if got.Properties.CustomerID != nil {
		cid = *got.Properties.CustomerID
	}

	if len(cid) != 36 || strings.Count(cid, "-") != 4 {
		t.Fatalf("customerId = %q, want a GUID", cid)
	}

	if got.ID != nil && cid == *got.ID {
		t.Fatalf("customerId must differ from the ARM id")
	}
}

// TestSDKSavedSearchLifecycle covers finding 5: workspace child resources are
// stored and served, not misrouted to the workspace body.
func TestSDKSavedSearchLifecycle(t *testing.T) {
	opts := newTestOptions(t)
	ctx := context.Background()

	wsClient, err := armoperationalinsights.NewWorkspacesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewWorkspacesClient: %v", err)
	}

	createWorkspace(t, wsClient, "ws-ss", "eastus")

	ssClient, err := armoperationalinsights.NewSavedSearchesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewSavedSearchesClient: %v", err)
	}

	created, err := ssClient.CreateOrUpdate(ctx, testRG, "ws-ss", "search-1", armoperationalinsights.SavedSearch{
		Properties: &armoperationalinsights.SavedSearchProperties{
			Category:    to.Ptr("cat"),
			DisplayName: to.Ptr("My search"),
			Query:       to.Ptr("Heartbeat | take 10"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	if created.Name == nil || *created.Name != "search-1" {
		t.Fatalf("saved search name = %v, want search-1", created.Name)
	}

	if created.Type == nil || !strings.Contains(*created.Type, "savedSearches") {
		t.Fatalf("saved search type = %v, want savedSearches", created.Type)
	}

	got, err := ssClient.Get(ctx, testRG, "ws-ss", "search-1", nil)
	if err != nil {
		t.Fatalf("Get saved search: %v", err)
	}

	if got.Properties == nil || got.Properties.Query == nil || *got.Properties.Query != "Heartbeat | take 10" {
		t.Fatalf("query = %+v, want round-tripped", got.Properties)
	}

	list, err := ssClient.ListByWorkspace(ctx, testRG, "ws-ss", nil)
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}

	if len(list.Value) != 1 || list.Value[0].Name == nil || *list.Value[0].Name != "search-1" {
		t.Fatalf("list = %+v, want [search-1]", list.Value)
	}
}

// TestSDKSharedKeys covers finding 6: GetSharedKeys returns stable ingestion
// keys instead of 405.
func TestSDKSharedKeys(t *testing.T) {
	opts := newTestOptions(t)
	ctx := context.Background()

	wsClient, err := armoperationalinsights.NewWorkspacesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewWorkspacesClient: %v", err)
	}

	createWorkspace(t, wsClient, "ws-keys", "eastus")

	skClient, err := armoperationalinsights.NewSharedKeysClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewSharedKeysClient: %v", err)
	}

	keys, err := skClient.GetSharedKeys(ctx, testRG, "ws-keys", nil)
	if err != nil {
		t.Fatalf("GetSharedKeys: %v", err)
	}

	if keys.PrimarySharedKey == nil || *keys.PrimarySharedKey == "" {
		t.Fatalf("primary shared key missing")
	}

	if keys.SecondarySharedKey == nil || *keys.SecondarySharedKey == "" {
		t.Fatalf("secondary shared key missing")
	}

	if *keys.PrimarySharedKey == *keys.SecondarySharedKey {
		t.Fatalf("primary and secondary keys must differ")
	}

	again, err := skClient.GetSharedKeys(ctx, testRG, "ws-keys", nil)
	if err != nil {
		t.Fatalf("GetSharedKeys (repeat): %v", err)
	}

	if *again.PrimarySharedKey != *keys.PrimarySharedKey {
		t.Fatalf("shared key not stable across calls")
	}
}
