// Real-SDK round-trip tests for the Cosmos Mongo-API ARM control plane: the live
// armcosmos MongoDBResourcesClient drives mongodbDatabases, collections and their
// throughputSettings end-to-end, proving IaC/`az`-style management of the Mongo
// data model and that it shares the one backend with the account control plane
// (and stays disjoint from the SQL ARM plane on the same server).

package cosmosdb_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// mongoEnv bundles the Mongo + account ARM clients and the live server.
type mongoEnv struct {
	mongo *armcosmos.MongoDBResourcesClient
	acct  *armcosmos.DatabaseAccountsClient
	ts    *httptest.Server
}

func newMongoEnv(t *testing.T) mongoEnv {
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

	mongoClient, err := armcosmos.NewMongoDBResourcesClient("sub-1", armFakeCred{}, opts)
	require.NoError(t, err)

	acctClient, err := armcosmos.NewDatabaseAccountsClient("sub-1", armFakeCred{}, opts)
	require.NoError(t, err)

	return mongoEnv{mongo: mongoClient, acct: acctClient, ts: ts}
}

func (e mongoEnv) createAccount(t *testing.T, rg, name, region string) {
	t.Helper()

	poller, err := e.acct.BeginCreateOrUpdate(context.Background(), rg, name, armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr(region),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindMongoDB),
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

func (e mongoEnv) putDatabase(t *testing.T, rg, acct, db string, opts *armcosmos.CreateUpdateOptions) {
	t.Helper()

	poller, err := e.mongo.BeginCreateUpdateMongoDBDatabase(context.Background(), rg, acct, db,
		armcosmos.MongoDBDatabaseCreateUpdateParameters{
			Properties: &armcosmos.MongoDBDatabaseCreateUpdateProperties{
				Resource: &armcosmos.MongoDBDatabaseResource{ID: to.Ptr(db)},
				Options:  opts,
			},
		}, nil)
	require.NoError(t, err)

	_, err = poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)
}

func (e mongoEnv) putCollection(
	t *testing.T, rg, acct, db string, res *armcosmos.MongoDBCollectionResource, opts *armcosmos.CreateUpdateOptions,
) armcosmos.MongoDBCollectionGetResults {
	t.Helper()

	poller, err := e.mongo.BeginCreateUpdateMongoDBCollection(context.Background(), rg, acct, db, *res.ID,
		armcosmos.MongoDBCollectionCreateUpdateParameters{
			Properties: &armcosmos.MongoDBCollectionCreateUpdateProperties{Resource: res, Options: opts},
		}, nil)
	require.NoError(t, err)

	out, err := poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)

	return out.MongoDBCollectionGetResults
}

// TestSDKMongoControlPlaneLifecycle is the full real-user flow: create account ->
// PUT database -> GET/list -> PUT collection (shard key + index) -> GET/list ->
// PUT manual throughput -> GET -> migrate to autoscale -> GET -> DELETE database
// cascades its collection.
func TestSDKMongoControlPlaneLifecycle(t *testing.T) {
	ctx := context.Background()
	env := newMongoEnv(t)
	env.createAccount(t, "rg-1", "mongo1", "eastus")

	// PUT database.
	env.putDatabase(t, "rg-1", "mongo1", "appdb", nil)

	// GET database.
	got, err := env.mongo.GetMongoDBDatabase(ctx, "rg-1", "mongo1", "appdb", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Name)
	assert.Equal(t, "appdb", *got.Name)
	require.NotNil(t, got.ID)
	assert.Contains(t, *got.ID, "/databaseAccounts/mongo1/mongodbDatabases/appdb")

	// List databases.
	assert.Contains(t, listMongoDatabaseNames(t, env, "rg-1", "mongo1"), "appdb")

	// PUT collection with a shard key + a unique TTL index.
	coll := env.putCollection(t, "rg-1", "mongo1", "appdb", &armcosmos.MongoDBCollectionResource{
		ID:       to.Ptr("orders"),
		ShardKey: map[string]*string{"customerId": to.Ptr("Hash")},
		Indexes: []*armcosmos.MongoIndex{{
			Key:     &armcosmos.MongoIndexKeys{Keys: []*string{to.Ptr("_ts")}},
			Options: &armcosmos.MongoIndexOptions{ExpireAfterSeconds: to.Ptr[int32](2592000), Unique: to.Ptr(false)},
		}},
		AnalyticalStorageTTL: to.Ptr[int32](-1),
	}, nil)
	require.NotNil(t, coll.Name)
	assert.Equal(t, "orders", *coll.Name)
	requireShardKey(t, coll, "customerId")

	// GET collection: shard key + index + analytical TTL round-trip.
	gotC, err := env.mongo.GetMongoDBCollection(ctx, "rg-1", "mongo1", "appdb", "orders", nil)
	require.NoError(t, err)
	requireShardKey(t, gotC.MongoDBCollectionGetResults, "customerId")
	require.NotNil(t, gotC.Properties.Resource.AnalyticalStorageTTL)
	assert.Equal(t, int32(-1), *gotC.Properties.Resource.AnalyticalStorageTTL)
	require.Len(t, gotC.Properties.Resource.Indexes, 1)
	require.NotNil(t, gotC.Properties.Resource.Indexes[0].Options.ExpireAfterSeconds)
	assert.Equal(t, int32(2592000), *gotC.Properties.Resource.Indexes[0].Options.ExpireAfterSeconds)

	// List collections.
	assert.Contains(t, listMongoCollectionNames(t, env, "rg-1", "mongo1", "appdb"), "orders")

	// PUT manual collection throughput.
	updateMongoCollectionThroughput(t, env, "rg-1", "mongo1", "appdb", "orders",
		&armcosmos.ThroughputSettingsResource{Throughput: to.Ptr[int32](600)})

	tp, err := env.mongo.GetMongoDBCollectionThroughput(ctx, "rg-1", "mongo1", "appdb", "orders", nil)
	require.NoError(t, err)
	require.NotNil(t, tp.Properties.Resource.Throughput)
	assert.Equal(t, int32(600), *tp.Properties.Resource.Throughput)

	// Migrate to autoscale.
	mPoller, err := env.mongo.BeginMigrateMongoDBCollectionToAutoscale(ctx, "rg-1", "mongo1", "appdb", "orders", nil)
	require.NoError(t, err)
	_, err = mPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	tp2, err := env.mongo.GetMongoDBCollectionThroughput(ctx, "rg-1", "mongo1", "appdb", "orders", nil)
	require.NoError(t, err)
	require.NotNil(t, tp2.Properties.Resource.AutoscaleSettings)
	require.NotNil(t, tp2.Properties.Resource.AutoscaleSettings.MaxThroughput)
	assert.Equal(t, int32(1000), *tp2.Properties.Resource.AutoscaleSettings.MaxThroughput)

	// DELETE database cascades the collection.
	dPoller, err := env.mongo.BeginDeleteMongoDBDatabase(ctx, "rg-1", "mongo1", "appdb", nil)
	require.NoError(t, err)
	_, err = dPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	_, err = env.mongo.GetMongoDBCollection(ctx, "rg-1", "mongo1", "appdb", "orders", nil)
	require.Error(t, err, "collection must 404 after its database is deleted")

	_, err = env.mongo.GetMongoDBDatabase(ctx, "rg-1", "mongo1", "appdb", nil)
	require.Error(t, err, "database must 404 after delete")
}

// TestSDKMongoDatabaseSharedThroughput drives database-level shared throughput
// and its migration to manual.
func TestSDKMongoDatabaseSharedThroughput(t *testing.T) {
	ctx := context.Background()
	env := newMongoEnv(t)
	env.createAccount(t, "rg-1", "mongo-shared", "eastus")

	env.putDatabase(t, "rg-1", "mongo-shared", "shared", &armcosmos.CreateUpdateOptions{
		AutoscaleSettings: &armcosmos.AutoscaleSettings{MaxThroughput: to.Ptr[int32](4000)},
	})

	tp, err := env.mongo.GetMongoDBDatabaseThroughput(ctx, "rg-1", "mongo-shared", "shared", nil)
	require.NoError(t, err)
	require.NotNil(t, tp.Properties.Resource.AutoscaleSettings)
	require.NotNil(t, tp.Properties.Resource.AutoscaleSettings.MaxThroughput)
	assert.Equal(t, int32(4000), *tp.Properties.Resource.AutoscaleSettings.MaxThroughput)

	// Migrate database throughput to manual.
	mPoller, err := env.mongo.BeginMigrateMongoDBDatabaseToManualThroughput(ctx, "rg-1", "mongo-shared", "shared", nil)
	require.NoError(t, err)
	_, err = mPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	tp2, err := env.mongo.GetMongoDBDatabaseThroughput(ctx, "rg-1", "mongo-shared", "shared", nil)
	require.NoError(t, err)
	require.NotNil(t, tp2.Properties.Resource.Throughput)
	assert.Equal(t, int32(4000), *tp2.Properties.Resource.Throughput)
}

// TestSDKMongoUnshardedCollection asserts a collection created with no shard key
// is unsharded (no shardKey reported back).
func TestSDKMongoUnshardedCollection(t *testing.T) {
	ctx := context.Background()
	env := newMongoEnv(t)
	env.createAccount(t, "rg-1", "mongo-u", "eastus")
	env.putDatabase(t, "rg-1", "mongo-u", "db", nil)

	env.putCollection(t, "rg-1", "mongo-u", "db", &armcosmos.MongoDBCollectionResource{ID: to.Ptr("logs")}, nil)

	got, err := env.mongo.GetMongoDBCollection(ctx, "rg-1", "mongo-u", "db", "logs", nil)
	require.NoError(t, err)
	assert.Empty(t, got.Properties.Resource.ShardKey, "an unsharded collection reports no shard key")
}

// TestSDKMongoCollectionWithoutDatabase asserts a collection cannot be created
// under a database that does not exist (ARM ParentResourceNotFound).
func TestSDKMongoCollectionWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	env := newMongoEnv(t)
	env.createAccount(t, "rg-1", "mongo2", "eastus")

	poller, err := env.mongo.BeginCreateUpdateMongoDBCollection(ctx, "rg-1", "mongo2", "ghostdb", "c1",
		armcosmos.MongoDBCollectionCreateUpdateParameters{
			Properties: &armcosmos.MongoDBCollectionCreateUpdateProperties{
				Resource: &armcosmos.MongoDBCollectionResource{ID: to.Ptr("c1")},
			},
		}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	require.Error(t, err, "creating a collection without its database must fail")
}

// TestSDKMongoMissingAccount asserts mongodbDatabases operations under a
// non-existent account are rejected.
func TestSDKMongoMissingAccount(t *testing.T) {
	ctx := context.Background()
	env := newMongoEnv(t)

	_, err := env.mongo.GetMongoDBDatabase(ctx, "rg-1", "no-such-account", "db", nil)
	require.Error(t, err)
}

// Helpers --------------------------------------------------------------------

func listMongoDatabaseNames(t *testing.T, env mongoEnv, rg, acct string) []string {
	t.Helper()

	names := []string{}
	pager := env.mongo.NewListMongoDBDatabasesPager(rg, acct, nil)

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

func listMongoCollectionNames(t *testing.T, env mongoEnv, rg, acct, db string) []string {
	t.Helper()

	names := []string{}
	pager := env.mongo.NewListMongoDBCollectionsPager(rg, acct, db, nil)

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

func updateMongoCollectionThroughput(
	t *testing.T, env mongoEnv, rg, acct, db, collection string, res *armcosmos.ThroughputSettingsResource,
) {
	t.Helper()

	poller, err := env.mongo.BeginUpdateMongoDBCollectionThroughput(context.Background(), rg, acct, db, collection,
		armcosmos.ThroughputSettingsUpdateParameters{
			Properties: &armcosmos.ThroughputSettingsUpdateProperties{Resource: res},
		}, nil)
	require.NoError(t, err)

	_, err = poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)
}

func requireShardKey(t *testing.T, c armcosmos.MongoDBCollectionGetResults, wantField string) {
	t.Helper()

	require.NotNil(t, c.Properties)
	require.NotNil(t, c.Properties.Resource)
	require.Contains(t, c.Properties.Resource.ShardKey, wantField)
}
