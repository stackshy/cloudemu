// Real-SDK round-trip tests for the Cosmos SQL-API ARM control plane: the live
// armcosmos SQLResourcesClient drives sqlDatabases, containers and their
// throughputSettings end-to-end, and the tests prove the control plane and the
// azcosmos data plane share one model (a control-plane container is reachable by
// a data-plane client, and vice versa).

package cosmosdb_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

type armFakeCred struct{}

func (armFakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// sqlEnv bundles the two ARM clients and the live server so a test can also
// point a data-plane azcosmos client at the same backend.
type sqlEnv struct {
	sql   *armcosmos.SQLResourcesClient
	acct  *armcosmos.DatabaseAccountsClient
	ts    *httptest.Server
	baseU string
}

func newSQLEnv(t *testing.T) sqlEnv {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{CosmosDB: cloudP.CosmosDB})

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

	sqlClient, err := armcosmos.NewSQLResourcesClient("sub-1", armFakeCred{}, opts)
	require.NoError(t, err)

	acctClient, err := armcosmos.NewDatabaseAccountsClient("sub-1", armFakeCred{}, opts)
	require.NoError(t, err)

	return sqlEnv{sql: sqlClient, acct: acctClient, ts: ts, baseU: ts.URL}
}

func (e sqlEnv) createAccount(t *testing.T, rg, name, region string) {
	t.Helper()

	poller, err := e.acct.BeginCreateOrUpdate(context.Background(), rg, name, armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr(region),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindGlobalDocumentDB),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType: to.Ptr("Standard"),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr(region), FailoverPriority: to.Ptr[int32](0)},
			},
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)
}

func (e sqlEnv) putDatabase(t *testing.T, rg, acct, db string, opts *armcosmos.CreateUpdateOptions) armcosmos.SQLDatabaseGetResults {
	t.Helper()

	poller, err := e.sql.BeginCreateUpdateSQLDatabase(context.Background(), rg, acct, db,
		armcosmos.SQLDatabaseCreateUpdateParameters{
			Properties: &armcosmos.SQLDatabaseCreateUpdateProperties{
				Resource: &armcosmos.SQLDatabaseResource{ID: to.Ptr(db)},
				Options:  opts,
			},
		}, nil)
	require.NoError(t, err)

	res, err := poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)

	return res.SQLDatabaseGetResults
}

func (e sqlEnv) putContainer(
	t *testing.T, rg, acct, db string, res *armcosmos.SQLContainerResource, opts *armcosmos.CreateUpdateOptions,
) armcosmos.SQLContainerGetResults {
	t.Helper()

	poller, err := e.sql.BeginCreateUpdateSQLContainer(context.Background(), rg, acct, db, *res.ID,
		armcosmos.SQLContainerCreateUpdateParameters{
			Properties: &armcosmos.SQLContainerCreateUpdateProperties{Resource: res, Options: opts},
		}, nil)
	require.NoError(t, err)

	out, err := poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)

	return out.SQLContainerGetResults
}

// TestSDKSQLControlPlaneLifecycle is the full real-user flow: create account ->
// PUT database -> GET/list -> PUT container -> GET/list -> PUT manual throughput
// -> GET -> migrate to autoscale -> GET -> DELETE database cascades its
// container.
func TestSDKSQLControlPlaneLifecycle(t *testing.T) {
	ctx := context.Background()
	env := newSQLEnv(t)
	env.createAccount(t, "rg-1", "cosmos1", "eastus")

	// PUT database.
	created := env.putDatabase(t, "rg-1", "cosmos1", "appdb", nil)
	require.NotNil(t, created.Name)
	assert.Equal(t, "appdb", *created.Name)

	// GET database.
	got, err := env.sql.GetSQLDatabase(ctx, "rg-1", "cosmos1", "appdb", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Name)
	assert.Equal(t, "appdb", *got.Name)
	require.NotNil(t, got.ID)
	assert.Contains(t, *got.ID, "/databaseAccounts/cosmos1/sqlDatabases/appdb")

	// List databases.
	assert.Contains(t, listDatabaseNames(t, env, "rg-1", "cosmos1"), "appdb")

	// PUT container with a custom partition key + default TTL.
	cont := env.putContainer(t, "rg-1", "cosmos1", "appdb", &armcosmos.SQLContainerResource{
		ID:           to.Ptr("users"),
		PartitionKey: &armcosmos.ContainerPartitionKey{Paths: []*string{to.Ptr("/pk")}, Kind: to.Ptr(armcosmos.PartitionKindHash)},
		DefaultTTL:   to.Ptr[int32](3600),
	}, nil)
	require.NotNil(t, cont.Name)
	assert.Equal(t, "users", *cont.Name)
	requireContainerPartitionKey(t, cont, "/pk")

	// GET container.
	gotC, err := env.sql.GetSQLContainer(ctx, "rg-1", "cosmos1", "appdb", "users", nil)
	require.NoError(t, err)
	requireContainerPartitionKey(t, gotC.SQLContainerGetResults, "/pk")
	require.NotNil(t, gotC.Properties.Resource.DefaultTTL)
	assert.Equal(t, int32(3600), *gotC.Properties.Resource.DefaultTTL)

	// List containers.
	assert.Contains(t, listContainerNames(t, env, "rg-1", "cosmos1", "appdb"), "users")

	// PUT manual container throughput.
	updateContainerThroughput(t, env, "rg-1", "cosmos1", "appdb", "users",
		&armcosmos.ThroughputSettingsResource{Throughput: to.Ptr[int32](800)})

	tp, err := env.sql.GetSQLContainerThroughput(ctx, "rg-1", "cosmos1", "appdb", "users", nil)
	require.NoError(t, err)
	require.NotNil(t, tp.Properties.Resource.Throughput)
	assert.Equal(t, int32(800), *tp.Properties.Resource.Throughput)

	// Migrate to autoscale.
	mPoller, err := env.sql.BeginMigrateSQLContainerToAutoscale(ctx, "rg-1", "cosmos1", "appdb", "users", nil)
	require.NoError(t, err)
	_, err = mPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	tp2, err := env.sql.GetSQLContainerThroughput(ctx, "rg-1", "cosmos1", "appdb", "users", nil)
	require.NoError(t, err)
	require.NotNil(t, tp2.Properties.Resource.AutoscaleSettings)
	require.NotNil(t, tp2.Properties.Resource.AutoscaleSettings.MaxThroughput)
	assert.Equal(t, int32(1000), *tp2.Properties.Resource.AutoscaleSettings.MaxThroughput)

	// DELETE database cascades the container.
	dPoller, err := env.sql.BeginDeleteSQLDatabase(ctx, "rg-1", "cosmos1", "appdb", nil)
	require.NoError(t, err)
	_, err = dPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	_, err = env.sql.GetSQLContainer(ctx, "rg-1", "cosmos1", "appdb", "users", nil)
	require.Error(t, err, "container must 404 after its database is deleted")

	_, err = env.sql.GetSQLDatabase(ctx, "rg-1", "cosmos1", "appdb", nil)
	require.Error(t, err, "database must 404 after delete")
}

// TestSDKSQLDatabaseChildLinks pins that a SQL database resource always carries
// its _colls/_users child links on PUT and GET — Cosmos'
// SQLDatabaseGetPropertiesResource exposes them, so they must not be dropped
// (the Mongo database resource, by contrast, has neither; see
// TestSDKMongoDatabaseNoChildLinks).
func TestSDKSQLDatabaseChildLinks(t *testing.T) {
	ctx := context.Background()
	env := newSQLEnv(t)
	env.createAccount(t, "rg-1", "cosmos-links", "eastus")

	created := env.putDatabase(t, "rg-1", "cosmos-links", "linked", nil)
	require.NotNil(t, created.Properties)
	require.NotNil(t, created.Properties.Resource)
	require.NotNil(t, created.Properties.Resource.Colls)
	assert.Equal(t, "colls/", *created.Properties.Resource.Colls)
	require.NotNil(t, created.Properties.Resource.Users)
	assert.Equal(t, "users/", *created.Properties.Resource.Users)

	got, err := env.sql.GetSQLDatabase(ctx, "rg-1", "cosmos-links", "linked", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Properties.Resource.Colls)
	assert.Equal(t, "colls/", *got.Properties.Resource.Colls)
	require.NotNil(t, got.Properties.Resource.Users)
	assert.Equal(t, "users/", *got.Properties.Resource.Users)
}

// TestSDKSQLDatabaseSharedThroughput drives database-level shared throughput and
// its migration to manual.
func TestSDKSQLDatabaseSharedThroughput(t *testing.T) {
	ctx := context.Background()
	env := newSQLEnv(t)
	env.createAccount(t, "rg-1", "cosmos-shared", "eastus")

	env.putDatabase(t, "rg-1", "cosmos-shared", "shared", &armcosmos.CreateUpdateOptions{
		AutoscaleSettings: &armcosmos.AutoscaleSettings{MaxThroughput: to.Ptr[int32](4000)},
	})

	tp, err := env.sql.GetSQLDatabaseThroughput(ctx, "rg-1", "cosmos-shared", "shared", nil)
	require.NoError(t, err)
	require.NotNil(t, tp.Properties.Resource.AutoscaleSettings)
	require.NotNil(t, tp.Properties.Resource.AutoscaleSettings.MaxThroughput)
	assert.Equal(t, int32(4000), *tp.Properties.Resource.AutoscaleSettings.MaxThroughput)

	// Migrate database throughput to manual.
	mPoller, err := env.sql.BeginMigrateSQLDatabaseToManualThroughput(ctx, "rg-1", "cosmos-shared", "shared", nil)
	require.NoError(t, err)
	_, err = mPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	tp2, err := env.sql.GetSQLDatabaseThroughput(ctx, "rg-1", "cosmos-shared", "shared", nil)
	require.NoError(t, err)
	require.NotNil(t, tp2.Properties.Resource.Throughput)
	assert.Equal(t, int32(4000), *tp2.Properties.Resource.Throughput)
}

// TestSDKSQLContainerWithoutDatabase asserts a container cannot be created under
// a database that does not exist (ARM ParentResourceNotFound).
func TestSDKSQLContainerWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	env := newSQLEnv(t)
	env.createAccount(t, "rg-1", "cosmos2", "eastus")

	poller, err := env.sql.BeginCreateUpdateSQLContainer(ctx, "rg-1", "cosmos2", "ghostdb", "c1",
		armcosmos.SQLContainerCreateUpdateParameters{
			Properties: &armcosmos.SQLContainerCreateUpdateProperties{
				Resource: &armcosmos.SQLContainerResource{
					ID:           to.Ptr("c1"),
					PartitionKey: &armcosmos.ContainerPartitionKey{Paths: []*string{to.Ptr("/pk")}},
				},
			},
		}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	require.Error(t, err, "creating a container without its database must fail")
}

// TestSDKSQLMissingAccount asserts sqlDatabases operations under a non-existent
// account are rejected.
func TestSDKSQLMissingAccount(t *testing.T) {
	ctx := context.Background()
	env := newSQLEnv(t)

	_, err := env.sql.GetSQLDatabase(ctx, "rg-1", "no-such-account", "db", nil)
	require.Error(t, err)
}

// TestSDKSQLControlPlaneUnifiesDataPlane proves the two planes share one model:
// a container created through the ARM control plane is reachable (and writable)
// by a data-plane azcosmos client, and a database created on the data plane is
// visible through an ARM GET.
func TestSDKSQLControlPlaneUnifiesDataPlane(t *testing.T) {
	ctx := context.Background()
	env := newSQLEnv(t)
	env.createAccount(t, "rg-1", "cosmos3", "eastus")

	// Control plane: create database + container.
	env.putDatabase(t, "rg-1", "cosmos3", "shared", nil)
	env.putContainer(t, "rg-1", "cosmos3", "shared", &armcosmos.SQLContainerResource{
		ID:           to.Ptr("events"),
		PartitionKey: &armcosmos.ContainerPartitionKey{Paths: []*string{to.Ptr("/pk")}, Kind: to.Ptr(armcosmos.PartitionKindHash)},
	}, nil)

	// Data plane: the same container is reachable and writable.
	dp := newDataPlaneClient(t, env, "cosmos3")

	cont, err := dp.NewContainer("shared", "events")
	require.NoError(t, err)

	doc := map[string]any{"id": "e1", "pk": "team-a", "kind": "click"}
	docBytes, _ := json.Marshal(doc)

	_, err = cont.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), docBytes, nil)
	require.NoError(t, err, "a control-plane container must accept data-plane writes")

	// Reverse: a data-plane-created database is visible to the ARM control plane.
	_, err = dp.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "dpdb"}, nil)
	require.NoError(t, err)

	got, err := env.sql.GetSQLDatabase(ctx, "rg-1", "cosmos3", "dpdb", nil)
	require.NoError(t, err, "a data-plane database must be visible via ARM GET")
	require.NotNil(t, got.Name)
	assert.Equal(t, "dpdb", *got.Name)
}

// Helpers --------------------------------------------------------------------

func newDataPlaneClient(t *testing.T, env sqlEnv, account string) *azcosmos.Client {
	t.Helper()

	cred, err := azcosmos.NewKeyCredential(fakeKey)
	require.NoError(t, err)

	opts := &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: env.ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	// The account's documentEndpoint carries the account as a path segment
	// (baseURL/{account}/), which the data plane peels back off.
	client, err := azcosmos.NewClientWithKey(env.baseU+"/"+account, cred, opts)
	require.NoError(t, err)

	return client
}

func listDatabaseNames(t *testing.T, env sqlEnv, rg, acct string) []string {
	t.Helper()

	names := []string{}
	pager := env.sql.NewListSQLDatabasesPager(rg, acct, nil)

	for pager.More() {
		page, err := pager.NextPage(context.Background())
		require.NoError(t, err)

		for _, d := range page.Value {
			if d.Name != nil {
				names = append(names, *d.Name)
			}
		}
	}

	return names
}

func listContainerNames(t *testing.T, env sqlEnv, rg, acct, db string) []string {
	t.Helper()

	names := []string{}
	pager := env.sql.NewListSQLContainersPager(rg, acct, db, nil)

	for pager.More() {
		page, err := pager.NextPage(context.Background())
		require.NoError(t, err)

		for _, c := range page.Value {
			if c.Name != nil {
				names = append(names, *c.Name)
			}
		}
	}

	return names
}

func updateContainerThroughput(
	t *testing.T, env sqlEnv, rg, acct, db, container string, res *armcosmos.ThroughputSettingsResource,
) {
	t.Helper()

	poller, err := env.sql.BeginUpdateSQLContainerThroughput(context.Background(), rg, acct, db, container,
		armcosmos.ThroughputSettingsUpdateParameters{
			Properties: &armcosmos.ThroughputSettingsUpdateProperties{Resource: res},
		}, nil)
	require.NoError(t, err)

	_, err = poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)
}

func requireContainerPartitionKey(t *testing.T, c armcosmos.SQLContainerGetResults, wantPath string) {
	t.Helper()

	require.NotNil(t, c.Properties)
	require.NotNil(t, c.Properties.Resource)
	require.NotNil(t, c.Properties.Resource.PartitionKey)
	require.Len(t, c.Properties.Resource.PartitionKey.Paths, 1)
	require.NotNil(t, c.Properties.Resource.PartitionKey.Paths[0])
	assert.Equal(t, wantPath, *c.Properties.Resource.PartitionKey.Paths[0])
}
