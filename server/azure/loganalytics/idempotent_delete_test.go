package loganalytics_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newLogAnalyticsServer stands up a TLS server backed by a fresh Azure Log
// Analytics driver, returning both the server (for raw child-resource requests)
// and arm.ClientOptions pointed at it.
func newLogAnalyticsServer(t *testing.T) (*httptest.Server, *arm.ClientOptions) {
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

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	return ts, opts
}

// TestSDKWorkspaceDeleteMissingIsIdempotent asserts that deleting a workspace
// that does not exist completes cleanly. Real Azure ARM DELETE is idempotent and
// returns 204 No Content ("Resource does not exist"), so the SDK LRO poller
// finishes without a 404 error.
func TestSDKWorkspaceDeleteMissingIsIdempotent(t *testing.T) {
	_, opts := newLogAnalyticsServer(t)
	ctx := context.Background()

	client, err := armoperationalinsights.NewWorkspacesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewWorkspacesClient: %v", err)
	}

	poller, err := client.BeginDelete(ctx, testRG, "never-created", nil)
	if err != nil {
		t.Fatalf("BeginDelete on missing workspace: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete of missing workspace should be a no-op (204), got: %v", err)
	}
}

// TestChildDeleteMissingStatusMatchesAzure asserts the per-child-type
// missing-resource DELETE status, matching the Log Analytics REST reference:
// tables return 204 (idempotent), while dataExports and savedSearches return
// 404 (their references document a 404/ErrorResponse for the missing case; the
// DataExports SDK tolerates that 404, making Delete a no-op for callers).
func TestChildDeleteMissingStatusMatchesAzure(t *testing.T) {
	ts, opts := newLogAnalyticsServer(t)
	ctx := context.Background()

	client, err := armoperationalinsights.NewWorkspacesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewWorkspacesClient: %v", err)
	}

	createWorkspace(t, client, "ws-del", "eastus")

	cases := []struct {
		child string
		want  int
	}{
		{"tables", http.StatusNoContent},
		{"dataExports", http.StatusNotFound},
		{"savedSearches", http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.child, func(t *testing.T) {
			url := fmt.Sprintf(
				"%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.OperationalInsights/workspaces/ws-del/%s/missing?api-version=2022-10-01",
				ts.URL, testSub, testRG, tc.child,
			)

			req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.want {
				t.Fatalf("DELETE missing %s = %d, want %d", tc.child, resp.StatusCode, tc.want)
			}
		})
	}
}
