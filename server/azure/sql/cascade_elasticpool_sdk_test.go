package sql_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// TestSDKAzureSQLServerDeleteCascadesDatabases is the BLOCKER regression: real
// Azure documents the logical server as "a logical container with strong
// lifetime semantics - delete a server and it deletes its databases and
// elastic pools" (learn.microsoft.com/azure/azure-sql/database/logical-servers).
// Before the fix, DeleteCluster left every database created via the
// Databases capability (what a real armsql.DatabasesClient drives) orphaned in
// the store, so it kept answering Get/List after the parent server was gone.
func TestSDKAzureSQLServerDeleteCascadesDatabases(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()
	mustCreateSQLServer(t, cf)

	dbs := cf.NewDatabasesClient()

	for _, name := range []string{"db1", "db2"} {
		poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", name, armsql.Database{
			Location: to.Ptr("eastus"),
		}, nil)
		if err != nil {
			t.Fatalf("BeginCreateOrUpdate %s: %v", name, err)
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("PollUntilDone %s: %v", name, err)
		}
	}

	page, err := dbs.NewListByServerPager("rg-1", "srv1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("List before delete: %v", err)
	}

	if len(page.Value) != 2 {
		t.Fatalf("got %d databases before server delete, want 2", len(page.Value))
	}

	delPoller, err := cf.NewServersClient().BeginDelete(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("server BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server delete PollUntilDone: %v", err)
	}

	// Each database must be gone too, not just unreachable via the deleted
	// server's listing.
	for _, name := range []string{"db1", "db2"} {
		if _, err := dbs.Get(ctx, "rg-1", "srv1", name, nil); err == nil {
			t.Errorf("Get %s after server delete: expected NotFound", name)
		} else if got := statusOf(t, err); got != http.StatusNotFound {
			t.Errorf("Get %s after server delete: status %d, want 404", name, got)
		}
	}

	// Re-creating the server and a same-named database must start clean, not
	// resurrect the deleted database's record.
	mustCreateSQLServer(t, cf)

	rePoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "db1", armsql.Database{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("re-create db1 BeginCreateOrUpdate: %v", err)
	}

	reResp, err := rePoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("re-create db1 PollUntilDone: %v", err)
	}

	if reResp.Database.Properties == nil || reResp.Database.Properties.ElasticPoolID != nil {
		t.Errorf("re-created db1 carries a stale elasticPoolId: %+v", reResp.Database.Properties)
	}
}

// TestSDKAzureSQLDatabaseElasticPoolMembership is the HIGH regression: setting
// databaseProperties.elasticPoolId through the Databases API must actually
// bind the database to the pool (persisted + echoed on read), and the pool
// must then refuse deletion while that database is still a member — matching
// real Azure's ElasticPoolNotEmpty error
// (learn.microsoft.com/rest/api/sql/elastic-pools/delete: "Request to delete
// an elastic pool that is not empty").
func TestSDKAzureSQLDatabaseElasticPoolMembership(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()
	mustCreateSQLServer(t, cf)

	ep := cf.NewElasticPoolsClient()

	epPoller, err := ep.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "pool1", armsql.ElasticPool{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("StandardPool"), Tier: to.Ptr("Standard")},
	}, nil)
	if err != nil {
		t.Fatalf("pool BeginCreateOrUpdate: %v", err)
	}

	if _, err := epPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool PollUntilDone: %v", err)
	}

	dbs := cf.NewDatabasesClient()

	dbPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "pooleddb", armsql.Database{
		Location:   to.Ptr("eastus"),
		Properties: &armsql.DatabaseProperties{ElasticPoolID: to.Ptr("pool1")},
	}, nil)
	if err != nil {
		t.Fatalf("db BeginCreateOrUpdate: %v", err)
	}

	dbResp, err := dbPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("db PollUntilDone: %v", err)
	}

	if dbResp.Database.Properties == nil || dbResp.Database.Properties.ElasticPoolID == nil ||
		*dbResp.Database.Properties.ElasticPoolID != "pool1" {
		t.Fatalf("elasticPoolId not echoed on create: %+v", dbResp.Database.Properties)
	}

	got, err := dbs.Get(ctx, "rg-1", "srv1", "pooleddb", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Database.Properties == nil || got.Database.Properties.ElasticPoolID == nil ||
		*got.Database.Properties.ElasticPoolID != "pool1" {
		t.Fatalf("elasticPoolId not persisted: %+v", got.Database.Properties)
	}

	// A pool with a member database refuses deletion.
	if _, err := ep.BeginDelete(ctx, "rg-1", "srv1", "pool1", nil); err == nil {
		t.Error("delete non-empty pool: expected error")
	} else {
		var re *azcore.ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected azcore.ResponseError, got %T: %v", err, err)
		}

		if re.StatusCode != http.StatusConflict {
			t.Errorf("delete non-empty pool: status %d, want 409", re.StatusCode)
		}
	}

	// Once the member database is gone, the pool deletes cleanly.
	delDBPoller, err := dbs.BeginDelete(ctx, "rg-1", "srv1", "pooleddb", nil)
	if err != nil {
		t.Fatalf("db BeginDelete: %v", err)
	}

	if _, err := delDBPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("db delete PollUntilDone: %v", err)
	}

	delPoolPoller, err := ep.BeginDelete(ctx, "rg-1", "srv1", "pool1", nil)
	if err != nil {
		t.Fatalf("pool BeginDelete after db removed: %v", err)
	}

	if _, err := delPoolPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool delete PollUntilDone: %v", err)
	}
}
