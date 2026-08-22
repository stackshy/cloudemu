package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmosforpostgresql/armcosmosforpostgresql"
	_ "github.com/lib/pq"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/internal/rtest"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/postgres"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureCosmosPostgresE2E runs the real-user flow against Azure Cosmos DB for
// PostgreSQL (Microsoft.DBforPostgreSQL/serverGroupsv2, the Citus cluster): create
// the cluster with the real Azure SDK, read the coordinator node's
// fully-qualified domain name, connect to it with a real Postgres client using
// the fixed "citus" superuser and the administratorLoginPassword, run SQL, then
// delete — all against CloudEmu backed by a real embedded Postgres (no Docker, no
// cloud account). Cosmos DB for PostgreSQL is Citus Postgres, so it reuses the
// shared Postgres DatabaseEngine.
func TestAzureCosmosPostgresE2E(t *testing.T) {
	// Default engine port (5432) — the port Azure Cosmos DB for PostgreSQL clients
	// always use; the coordinator FQDN is the only connection detail the SDK
	// surfaces.
	eng := postgres.New(0)
	t.Cleanup(func() { _ = eng.Close() })

	cloudP := cloudemu.NewAzure(config.WithDatabaseEngine(eng))
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{CosmosPostgreSQL: cloudP.CosmosPostgreSQL}))
	t.Cleanup(ts.Close)

	factory, err := armcosmosforpostgresql.NewClientFactory("sub-1", rtest.AzureFakeCred{}, rtest.ARMOpts(ts))
	if err != nil {
		t.Fatalf("client factory: %v", err)
	}

	ctx := context.Background()

	const (
		rg      = "rg-1"
		cluster = "app-citus"
		coord   = cluster + "-c" // the coordinator node the SDK derives.
		user    = "citus"        // Cosmos DB for PostgreSQL's fixed superuser.
		pass    = "Sup3rs3cret1"
		dbName  = "citus"
	)

	cc := factory.NewClustersClient()

	// 1. Create the cluster — like `az cosmosdb postgres cluster create`.
	createPoller, err := cc.BeginCreate(ctx, rg, cluster, armcosmosforpostgresql.Cluster{
		Location: to.Ptr("eastus"),
		Properties: &armcosmosforpostgresql.ClusterProperties{
			AdministratorLoginPassword: to.Ptr(pass),
			CoordinatorVCores:          to.Ptr[int32](4),
			NodeCount:                  to.Ptr[int32](2),
			CitusVersion:               to.Ptr("12.1"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	// 2. Read the coordinator endpoint the SDK reports (the real Postgres host).
	srv, err := factory.NewServersClient().Get(ctx, rg, cluster, coord, nil)
	if err != nil {
		t.Fatalf("servers Get coordinator: %v", err)
	}

	if srv.Properties == nil || srv.Properties.FullyQualifiedDomainName == nil {
		t.Fatalf("no coordinator FQDN reported: %+v", srv.ClusterServer)
	}

	host := *srv.Properties.FullyQualifiedDomainName

	// Connect exactly as a real Cosmos DB for PostgreSQL client would: the
	// coordinator FQDN from the SDK on the fixed port 5432, authenticating as the
	// "citus" superuser with the administratorLoginPassword — no out-of-band port
	// knowledge.
	dsn := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable", host, user, pass, dbName)

	// 3. Connect with a real Postgres client and run real SQL.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	if _, err := db.Exec("CREATE TABLE accounts (id int primary key, name text)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec("INSERT INTO accounts VALUES (1, 'cloudemu')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM accounts WHERE id = 1").Scan(&name); err != nil {
		t.Fatalf("select: %v", err)
	}

	if name != "cloudemu" {
		t.Fatalf("round-trip mismatch: got %q", name)
	}

	_ = db.Close()

	// 4. Delete the cluster — the real database is torn down.
	delPoller, err := cc.BeginDelete(ctx, rg, cluster, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}

	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted cluster's database to fail")
	}
}
