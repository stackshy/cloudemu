package databricks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const testSub = "sub-1"

// newARMOptions spins up an httptest server backed by a fresh Azure Databricks
// provider and returns arm client options + the subscription id pointing at it.
// Callers build any armdatabricks client (Workspaces, AccessConnectors,
// PrivateEndpointConnections, PrivateLinkResources, VNetPeering,
// OutboundNetworkDependenciesEndpoints, Operations) against the same server, so
// sub-resource tests can seed a workspace and then exercise their own client.
func newARMOptions(t *testing.T) (*arm.ClientOptions, string) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Databricks: cloudP.Databricks})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)
	ensureRG(t, ts, testSub, testRG)

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
	}, testSub
}

// ensureRG creates a resource group so tests can PUT resources into it. Real
// Azure requires the group to exist first (the emulator enforces this via a
// pre-dispatch gate), so tests must provision it before their resource ops.
func ensureRG(t *testing.T, ts *httptest.Server, sub, rg string) {
	t.Helper()

	url := ts.URL + "/subscriptions/" + sub + "/resourcegroups/" + rg + "?api-version=2021-04-01"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url,
		strings.NewReader(`{"location":"eastus"}`))
	if err != nil {
		t.Fatalf("ensureRG new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("ensureRG PUT %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("ensureRG %s: unexpected status %d", url, resp.StatusCode)
	}
}

// seedWorkspace creates a workspace via the real WorkspacesClient so that
// workspace sub-resource tests (PEC, private link, peering, outbound) have a
// live parent. It returns the created workspace.
func seedWorkspace(t *testing.T, opts *arm.ClientOptions, rg, name string) armdatabricks.Workspace {
	t.Helper()

	client, err := armdatabricks.NewWorkspacesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new workspaces client: %v", err)
	}

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armdatabricks.Workspace{
		Location: to.Ptr("eastus"),
		SKU:      &armdatabricks.SKU{Name: to.Ptr("premium")},
		Properties: &armdatabricks.WorkspaceProperties{
			ManagedResourceGroupID: to.Ptr(managed),
		},
	}, nil)
	if err != nil {
		t.Fatalf("seed BeginCreateOrUpdate: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("seed PollUntilDone: %v", err)
	}

	return res.Workspace
}
