package synapse_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/synapse/armsynapse"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	subID  = "00000000-0000-0000-0000-000000000000"
	rgName = "rg-synapse"
	wsName = "test-workspace"
)

// fakeCred is a static-token credential for tests.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func clientOpts(ts *httptest.Server) *arm.ClientOptions {
	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	return &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: myCloud, Transport: ts.Client(),
		Retry: policy.RetryOptions{MaxRetries: -1},
	}}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{SubscriptionID: subID, IAM: cloudP.IAM})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

// drainPager walks an armsynapse pager to exhaustion, collecting one string per
// item via take. It abstracts over the distinct generic pager types the SDK
// returns for each list operation.
func drainPager[R any](
	t *testing.T, ctx context.Context,
	more func() bool, next func(context.Context) (R, error), take func(R) []string,
) []string {
	t.Helper()

	var names []string

	for more() {
		page, err := next(ctx)
		if err != nil {
			t.Fatalf("list page: %v", err)
		}

		names = append(names, take(page)...)
	}

	return names
}

func assertNames(t *testing.T, what string, got []string, want string) {
	t.Helper()

	if len(got) != 1 || got[0] != want {
		t.Fatalf("%s = %v, want [%s]", what, got, want)
	}
}

// TestSDKSynapseLifecycle drives the full Synapse control plane with the real
// armsynapse SDK: workspace create (poller must complete, not hang) -> SQL pool
// create/pause/resume -> Spark (big data) pool create -> integration runtime
// create/start/stop -> get/list at each level -> workspace delete.
func TestSDKSynapseLifecycle(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	wsClient, err := armsynapse.NewWorkspacesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewWorkspacesClient: %v", err)
	}

	createWorkspace(t, ctx, wsClient)
	assertWorkspaceListed(t, ctx, wsClient)

	sqlLifecycle(t, ctx, ts)
	bigDataLifecycle(t, ctx, ts)
	intRuntimeLifecycle(t, ctx, ts)

	deleteWorkspace(t, ctx, wsClient)
}

func createWorkspace(t *testing.T, ctx context.Context, c *armsynapse.WorkspacesClient) {
	t.Helper()

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, wsName, armsynapse.Workspace{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
		Properties: &armsynapse.WorkspaceProperties{
			DefaultDataLakeStorage: &armsynapse.DataLakeStorageAccountDetails{
				AccountURL: to.Ptr("https://lake.dfs.core.windows.net"),
				Filesystem: to.Ptr("synapsefs"),
			},
			SQLAdministratorLogin:    to.Ptr("sqladmin"),
			ManagedResourceGroupName: to.Ptr("mrg-synapse"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate workspace: %v", err)
	}

	// PollUntilDone must terminate: the sync 201 body carries
	// provisioningState=Succeeded, so the poller does not hang.
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll workspace create: %v", err)
	}

	if res.Properties == nil || res.Properties.ProvisioningState == nil ||
		*res.Properties.ProvisioningState != "Succeeded" {
		t.Fatalf("workspace provisioningState = %v, want Succeeded", res.Properties)
	}

	got, err := c.Get(ctx, rgName, wsName, nil)
	if err != nil {
		t.Fatalf("Get workspace: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("workspace tags = %v, want env=test", got.Tags)
	}

	if got.Properties == nil || got.Properties.SQLAdministratorLogin == nil ||
		*got.Properties.SQLAdministratorLogin != "sqladmin" {
		t.Fatalf("sqlAdministratorLogin = %v, want sqladmin", got.Properties)
	}

	if len(got.Properties.ConnectivityEndpoints) == 0 {
		t.Fatalf("connectivityEndpoints empty, want minted dev/web/sql endpoints")
	}
}

func assertWorkspaceListed(t *testing.T, ctx context.Context, c *armsynapse.WorkspacesClient) {
	t.Helper()

	byRG := c.NewListByResourceGroupPager(rgName, nil)
	assertNames(t, "workspaces (by RG)", drainPager(t, ctx, byRG.More, byRG.NextPage,
		func(p armsynapse.WorkspacesClientListByResourceGroupResponse) []string {
			return workspaceNames(p.Value)
		}), wsName)

	bySub := c.NewListPager(nil)
	assertNames(t, "workspaces (by sub)", drainPager(t, ctx, bySub.More, bySub.NextPage,
		func(p armsynapse.WorkspacesClientListResponse) []string { return workspaceNames(p.Value) }), wsName)
}

func workspaceNames(items []*armsynapse.Workspace) []string {
	names := make([]string, 0, len(items))
	for _, ws := range items {
		names = append(names, *ws.Name)
	}

	return names
}

func deleteWorkspace(t *testing.T, ctx context.Context, c *armsynapse.WorkspacesClient) {
	t.Helper()

	poller, err := c.BeginDelete(ctx, rgName, wsName, nil)
	if err != nil {
		t.Fatalf("BeginDelete workspace: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll workspace delete: %v", err)
	}

	if _, err := c.Get(ctx, rgName, wsName, nil); err == nil {
		t.Fatal("post-delete Get returned nil error, want NotFound")
	}
}

func sqlLifecycle(t *testing.T, ctx context.Context, ts *httptest.Server) {
	t.Helper()

	const poolName = "dwpool"

	c, err := armsynapse.NewSQLPoolsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewSQLPoolsClient: %v", err)
	}

	createPoller, err := c.BeginCreate(ctx, rgName, wsName, poolName, armsynapse.SQLPool{
		Location:   to.Ptr("eastus"),
		SKU:        &armsynapse.SKU{Name: to.Ptr("DW100c"), Tier: to.Ptr("DataWarehouse")},
		Properties: &armsynapse.SQLPoolResourceProperties{Collation: to.Ptr("SQL_Latin1_General_CP1_CI_AS")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate SQL pool: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll SQL pool create: %v", err)
	}

	got, err := c.Get(ctx, rgName, wsName, poolName, nil)
	if err != nil {
		t.Fatalf("Get SQL pool: %v", err)
	}

	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != "DW100c" {
		t.Fatalf("SQL pool sku = %v, want DW100c", got.SKU)
	}

	assertSQLStatus(t, "pause", pausePool(t, ctx, c, poolName), "Paused")
	assertSQLStatus(t, "resume", resumePool(t, ctx, c, poolName), "Online")

	pager := c.NewListByWorkspacePager(rgName, wsName, nil)
	assertNames(t, "SQL pools", drainPager(t, ctx, pager.More, pager.NextPage,
		func(p armsynapse.SQLPoolsClientListByWorkspaceResponse) []string { return sqlPoolNames(p.Value) }), poolName)

	delPoller, err := c.BeginDelete(ctx, rgName, wsName, poolName, nil)
	if err != nil {
		t.Fatalf("BeginDelete SQL pool: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll SQL pool delete: %v", err)
	}
}

func pausePool(t *testing.T, ctx context.Context, c *armsynapse.SQLPoolsClient, name string) *string {
	t.Helper()

	poller, err := c.BeginPause(ctx, rgName, wsName, name, nil)
	if err != nil {
		t.Fatalf("BeginPause SQL pool: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll SQL pool pause: %v", err)
	}

	if res.Properties == nil {
		return nil
	}

	return res.Properties.Status
}

func resumePool(t *testing.T, ctx context.Context, c *armsynapse.SQLPoolsClient, name string) *string {
	t.Helper()

	poller, err := c.BeginResume(ctx, rgName, wsName, name, nil)
	if err != nil {
		t.Fatalf("BeginResume SQL pool: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll SQL pool resume: %v", err)
	}

	if res.Properties == nil {
		return nil
	}

	return res.Properties.Status
}

func assertSQLStatus(t *testing.T, action string, got *string, want string) {
	t.Helper()

	if got == nil || *got != want {
		t.Fatalf("SQL pool status after %s = %v, want %s", action, got, want)
	}
}

func sqlPoolNames(items []*armsynapse.SQLPool) []string {
	names := make([]string, 0, len(items))
	for _, p := range items {
		names = append(names, *p.Name)
	}

	return names
}

func bigDataLifecycle(t *testing.T, ctx context.Context, ts *httptest.Server) {
	t.Helper()

	const poolName = "sparkpool"

	c, err := armsynapse.NewBigDataPoolsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewBigDataPoolsClient: %v", err)
	}

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, wsName, poolName, armsynapse.BigDataPoolResourceInfo{
		Location: to.Ptr("eastus"),
		Properties: &armsynapse.BigDataPoolResourceProperties{
			NodeCount:      to.Ptr[int32](3),
			NodeSize:       to.Ptr(armsynapse.NodeSizeSmall),
			NodeSizeFamily: to.Ptr(armsynapse.NodeSizeFamilyMemoryOptimized),
			SparkVersion:   to.Ptr("3.3"),
			AutoScale: &armsynapse.AutoScaleProperties{
				Enabled:      to.Ptr(true),
				MinNodeCount: to.Ptr[int32](3),
				MaxNodeCount: to.Ptr[int32](10),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate big data pool: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll big data pool create: %v", err)
	}

	got, err := c.Get(ctx, rgName, wsName, poolName, nil)
	if err != nil {
		t.Fatalf("Get big data pool: %v", err)
	}

	if got.Properties == nil || got.Properties.NodeCount == nil || *got.Properties.NodeCount != 3 {
		t.Fatalf("big data pool nodeCount = %v, want 3", got.Properties)
	}

	if got.Properties.AutoScale == nil || got.Properties.AutoScale.MaxNodeCount == nil ||
		*got.Properties.AutoScale.MaxNodeCount != 10 {
		t.Fatalf("big data pool autoScale = %v, want max 10", got.Properties)
	}

	pager := c.NewListByWorkspacePager(rgName, wsName, nil)
	assertNames(t, "big data pools", drainPager(t, ctx, pager.More, pager.NextPage,
		func(p armsynapse.BigDataPoolsClientListByWorkspaceResponse) []string {
			return bigDataPoolNames(p.Value)
		}), poolName)
}

func bigDataPoolNames(items []*armsynapse.BigDataPoolResourceInfo) []string {
	names := make([]string, 0, len(items))
	for _, p := range items {
		names = append(names, *p.Name)
	}

	return names
}

func intRuntimeLifecycle(t *testing.T, ctx context.Context, ts *httptest.Server) {
	t.Helper()

	const irName = "autoResolveIntegrationRuntime"

	c, err := armsynapse.NewIntegrationRuntimesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewIntegrationRuntimesClient: %v", err)
	}

	createPoller, err := c.BeginCreate(ctx, rgName, wsName, irName, armsynapse.IntegrationRuntimeResource{
		Properties: &armsynapse.ManagedIntegrationRuntime{
			Type:        to.Ptr(armsynapse.IntegrationRuntimeTypeManaged),
			Description: to.Ptr("managed IR"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate integration runtime: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll integration runtime create: %v", err)
	}

	got, err := c.Get(ctx, rgName, wsName, irName, nil)
	if err != nil {
		t.Fatalf("Get integration runtime: %v", err)
	}

	if got.Name == nil || *got.Name != irName {
		t.Fatalf("integration runtime name = %v, want %s", got.Name, irName)
	}

	if _, ok := got.Properties.(*armsynapse.ManagedIntegrationRuntime); !ok {
		t.Fatalf("integration runtime properties type = %T, want *ManagedIntegrationRuntime", got.Properties)
	}

	startIntRuntime(t, ctx, c, irName)
	stopIntRuntime(t, ctx, c, irName)

	pager := c.NewListByWorkspacePager(rgName, wsName, nil)
	assertNames(t, "integration runtimes", drainPager(t, ctx, pager.More, pager.NextPage,
		func(p armsynapse.IntegrationRuntimesClientListByWorkspaceResponse) []string {
			return intRuntimeNames(p.Value)
		}), irName)

	delPoller, err := c.BeginDelete(ctx, rgName, wsName, irName, nil)
	if err != nil {
		t.Fatalf("BeginDelete integration runtime: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll integration runtime delete: %v", err)
	}
}

func startIntRuntime(t *testing.T, ctx context.Context, c *armsynapse.IntegrationRuntimesClient, name string) {
	t.Helper()

	poller, err := c.BeginStart(ctx, rgName, wsName, name, nil)
	if err != nil {
		t.Fatalf("BeginStart integration runtime: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll integration runtime start: %v", err)
	}
}

func stopIntRuntime(t *testing.T, ctx context.Context, c *armsynapse.IntegrationRuntimesClient, name string) {
	t.Helper()

	poller, err := c.BeginStop(ctx, rgName, wsName, name, nil)
	if err != nil {
		t.Fatalf("BeginStop integration runtime: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll integration runtime stop: %v", err)
	}
}

func intRuntimeNames(items []*armsynapse.IntegrationRuntimeResource) []string {
	names := make([]string, 0, len(items))
	for _, ir := range items {
		names = append(names, *ir.Name)
	}

	return names
}
