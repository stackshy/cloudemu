package databricks_test

import (
	"context"
	"net/http/httptest"
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
