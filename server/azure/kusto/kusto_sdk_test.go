package kusto_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/kusto/armkusto"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	subID       = "00000000-0000-0000-0000-000000000000"
	rgName      = "rg-adx"
	clusterName = "testadxcluster"
	dbName      = "telemetry"
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

// TestSDKKustoLifecycle drives the full Kusto control plane with the real
// armkusto SDK: cluster create (the LRO poller must complete, not hang), list,
// database create/get/list, cluster stop/start, and cluster + database delete.
func TestSDKKustoLifecycle(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	clusters, err := armkusto.NewClustersClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewClustersClient: %v", err)
	}

	createCluster(t, ctx, clusters)
	assertClusterListed(t, ctx, clusters)

	databases, err := armkusto.NewDatabasesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewDatabasesClient: %v", err)
	}

	createDatabase(t, ctx, databases)
	assertDatabaseListed(t, ctx, databases)

	stopStartCluster(t, ctx, clusters)
	deleteDatabase(t, ctx, databases)
	deleteCluster(t, ctx, clusters)
}

func createCluster(t *testing.T, ctx context.Context, c *armkusto.ClustersClient) {
	t.Helper()

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, clusterName, armkusto.Cluster{
		Location: to.Ptr("eastus"),
		SKU: &armkusto.AzureSKU{
			Name:     to.Ptr(armkusto.AzureSKUNameStandardD11V2),
			Tier:     to.Ptr(armkusto.AzureSKUTierStandard),
			Capacity: to.Ptr[int32](2),
		},
		Tags: map[string]*string{"env": to.Ptr("test")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate cluster: %v", err)
	}

	// PollUntilDone must terminate: the sync 201 body carries
	// provisioningState=Succeeded, so the poller does not hang.
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll cluster create: %v", err)
	}

	if res.Properties == nil || res.Properties.ProvisioningState == nil ||
		*res.Properties.ProvisioningState != armkusto.ProvisioningStateSucceeded {
		t.Fatalf("cluster provisioningState = %v, want Succeeded", res.Properties)
	}

	if res.Properties.State == nil || *res.Properties.State != armkusto.StateRunning {
		t.Fatalf("cluster state = %v, want Running", res.Properties)
	}

	assertClusterURIs(t, res.Properties)

	got, err := c.Get(ctx, rgName, clusterName, nil)
	if err != nil {
		t.Fatalf("Get cluster: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("cluster tags = %v, want env=test", got.Tags)
	}

	if got.SKU == nil || got.SKU.Capacity == nil || *got.SKU.Capacity != 2 {
		t.Fatalf("cluster sku capacity = %v, want 2", got.SKU)
	}
}

func assertClusterURIs(t *testing.T, props *armkusto.ClusterProperties) {
	t.Helper()

	if props.URI == nil || !strings.Contains(*props.URI, clusterName) {
		t.Fatalf("cluster uri = %v, want it to contain %q", props.URI, clusterName)
	}

	if props.DataIngestionURI == nil || !strings.Contains(*props.DataIngestionURI, "ingest-") {
		t.Fatalf("dataIngestionUri = %v, want an ingest- host", props.DataIngestionURI)
	}
}

func assertClusterListed(t *testing.T, ctx context.Context, c *armkusto.ClustersClient) {
	t.Helper()

	rgPage, err := c.NewListByResourceGroupPager(rgName, nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("ListByResourceGroup: %v", err)
	}

	if len(rgPage.Value) != 1 || *rgPage.Value[0].Name != clusterName {
		t.Fatalf("ListByResourceGroup = %v, want [%s]", clusterNames(rgPage.Value), clusterName)
	}

	subPage, err := c.NewListPager(nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(subPage.Value) != 1 || *subPage.Value[0].Name != clusterName {
		t.Fatalf("List = %v, want [%s]", clusterNames(subPage.Value), clusterName)
	}
}

func clusterNames(clusters []*armkusto.Cluster) []string {
	names := make([]string, 0, len(clusters))
	for _, c := range clusters {
		names = append(names, *c.Name)
	}

	return names
}

func createDatabase(t *testing.T, ctx context.Context, c *armkusto.DatabasesClient) {
	t.Helper()

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, clusterName, dbName, &armkusto.ReadWriteDatabase{
		Kind:     to.Ptr(armkusto.KindReadWrite),
		Location: to.Ptr("eastus"),
		Properties: &armkusto.ReadWriteDatabaseProperties{
			SoftDeletePeriod: to.Ptr("P30D"),
			HotCachePeriod:   to.Ptr("P7D"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate database: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll database create: %v", err)
	}

	got, err := c.Get(ctx, rgName, clusterName, dbName, nil)
	if err != nil {
		t.Fatalf("Get database: %v", err)
	}

	rw, ok := got.DatabaseClassification.(*armkusto.ReadWriteDatabase)
	if !ok {
		t.Fatalf("database kind = %T, want *ReadWriteDatabase", got.DatabaseClassification)
	}

	if rw.Properties == nil || rw.Properties.SoftDeletePeriod == nil || *rw.Properties.SoftDeletePeriod != "P30D" {
		t.Fatalf("softDeletePeriod = %v, want P30D", rw.Properties)
	}
}

func assertDatabaseListed(t *testing.T, ctx context.Context, c *armkusto.DatabasesClient) {
	t.Helper()

	pager := c.NewListByClusterPager(rgName, clusterName, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list databases: %v", err)
		}

		for _, db := range page.Value {
			names = append(names, *db.GetDatabase().Name)
		}
	}

	// The database name is returned parent-qualified (cluster/database).
	if len(names) != 1 || !strings.HasSuffix(names[0], dbName) {
		t.Fatalf("ListByCluster = %v, want one entry ending %q", names, dbName)
	}
}

func stopStartCluster(t *testing.T, ctx context.Context, c *armkusto.ClustersClient) {
	t.Helper()

	stop, err := c.BeginStop(ctx, rgName, clusterName, nil)
	if err != nil {
		t.Fatalf("BeginStop: %v", err)
	}

	if _, err := stop.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll stop: %v", err)
	}

	stopped, err := c.Get(ctx, rgName, clusterName, nil)
	if err != nil {
		t.Fatalf("Get after stop: %v", err)
	}

	if stopped.Properties == nil || stopped.Properties.State == nil || *stopped.Properties.State != armkusto.StateStopped {
		t.Fatalf("state after stop = %v, want Stopped", stopped.Properties)
	}

	start, err := c.BeginStart(ctx, rgName, clusterName, nil)
	if err != nil {
		t.Fatalf("BeginStart: %v", err)
	}

	if _, err := start.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll start: %v", err)
	}

	started, err := c.Get(ctx, rgName, clusterName, nil)
	if err != nil {
		t.Fatalf("Get after start: %v", err)
	}

	if started.Properties == nil || started.Properties.State == nil || *started.Properties.State != armkusto.StateRunning {
		t.Fatalf("state after start = %v, want Running", started.Properties)
	}
}

func deleteDatabase(t *testing.T, ctx context.Context, c *armkusto.DatabasesClient) {
	t.Helper()

	poller, err := c.BeginDelete(ctx, rgName, clusterName, dbName, nil)
	if err != nil {
		t.Fatalf("BeginDelete database: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll database delete: %v", err)
	}

	if _, err := c.Get(ctx, rgName, clusterName, dbName, nil); err == nil {
		t.Fatal("post-delete Get database returned nil error, want NotFound")
	}
}

func deleteCluster(t *testing.T, ctx context.Context, c *armkusto.ClustersClient) {
	t.Helper()

	poller, err := c.BeginDelete(ctx, rgName, clusterName, nil)
	if err != nil {
		t.Fatalf("BeginDelete cluster: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll cluster delete: %v", err)
	}

	if _, err := c.Get(ctx, rgName, clusterName, nil); err == nil {
		t.Fatal("post-delete Get cluster returned nil error, want NotFound")
	}
}
