package loganalytics_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// NOTE: This slice covers the Log Analytics workspace lifecycle
// (Microsoft.OperationalInsights/workspaces ARM control plane), mapped onto the
// logging driver's log-group lifecycle. The data-plane log-query / ingestion
// API (api.loganalytics.io) is a separate wire surface and is intentionally out
// of scope — put/get log-event round-trips for Azure are exercised via the AWS
// and GCP slices, which drive the same shared driver methods.

const (
	testRG  = "rg-1"
	testSub = "sub-1"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newWorkspacesClient(t *testing.T) *armoperationalinsights.WorkspacesClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{LogAnalytics: cloudP.LogAnalytics})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armoperationalinsights.NewWorkspacesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewWorkspacesClient: %v", err)
	}

	return client
}

func TestSDKLogAnalyticsWorkspaceLifecycle(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, "logs-ws", armoperationalinsights.Workspace{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
		Properties: &armoperationalinsights.WorkspaceProperties{
			RetentionInDays: to.Ptr(int32(90)),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone: %v", err)
	}

	if created.Name == nil || *created.Name != "logs-ws" {
		t.Fatalf("CreateOrUpdate name = %v, want logs-ws", created.Name)
	}

	got, err := client.Get(ctx, testRG, "logs-ws", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", got.Tags)
	}

	if got.Properties == nil || got.Properties.RetentionInDays == nil || *got.Properties.RetentionInDays != 90 {
		t.Fatalf("retention = %+v, want 90", got.Properties)
	}

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

	if len(names) != 1 || names[0] != "logs-ws" {
		t.Fatalf("list = %v, want [logs-ws]", names)
	}

	delPoller, err := client.BeginDelete(ctx, testRG, "logs-ws", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	_, err = client.Get(ctx, testRG, "logs-ws", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKLogAnalyticsSubscriptionList(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	for _, name := range []string{"ws-a", "ws-b"} {
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

	count := 0

	pager := client.NewListPager(nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("List: %v", perr)
		}

		count += len(page.Value)
	}

	if count != 2 {
		t.Fatalf("subscription list count = %d, want 2", count)
	}
}

func TestSDKLogAnalyticsErrors(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	_, err := client.Get(ctx, testRG, "missing", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}
