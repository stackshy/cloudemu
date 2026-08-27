package sql_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// guidPattern matches a canonical 8-4-4-4-12 hex GUID.
var guidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const vCoreCapacity = int32(4)

// createServer creates a logical server the database tests hang databases off.
func createServer(ctx context.Context, t *testing.T, servers *armsql.ServersClient, name string) {
	t.Helper()

	poller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", name, armsql.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Server BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Server PollUntilDone: %v", err)
	}
}

// createDB creates a database and returns its create response.
func createDB(
	ctx context.Context, t *testing.T, dbs *armsql.DatabasesClient, srv, name string, sku *armsql.SKU,
) armsql.DatabasesClientCreateOrUpdateResponse {
	t.Helper()

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", srv, name, armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      sku,
	}, nil)
	if err != nil {
		t.Fatalf("DB BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("DB PollUntilDone: %v", err)
	}

	return resp
}

// TestSDKAzureSQLDatabaseVCoreCapacity covers B1: a vCore SKU's capacity must
// round-trip on both sku.capacity and properties.currentSku.capacity.
func TestSDKAzureSQLDatabaseVCoreCapacity(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	createServer(ctx, t, servers, "srv1")

	sku := &armsql.SKU{
		Name:     to.Ptr("GP_Gen5_4"),
		Tier:     to.Ptr("GeneralPurpose"),
		Capacity: to.Ptr(vCoreCapacity),
	}
	createDB(ctx, t, dbs, "srv1", "appdb", sku)

	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Database.SKU == nil || got.Database.SKU.Capacity == nil {
		t.Fatal("expected sku.capacity set on GET")
	}

	if *got.Database.SKU.Capacity != vCoreCapacity {
		t.Fatalf("sku.capacity = %d, want %d", *got.Database.SKU.Capacity, vCoreCapacity)
	}

	if got.Database.Properties == nil || got.Database.Properties.CurrentSKU == nil ||
		got.Database.Properties.CurrentSKU.Capacity == nil {
		t.Fatal("expected properties.currentSku.capacity set on GET")
	}

	if *got.Database.Properties.CurrentSKU.Capacity != vCoreCapacity {
		t.Fatalf("currentSku.capacity = %d, want %d", *got.Database.Properties.CurrentSKU.Capacity, vCoreCapacity)
	}
}

// TestSDKAzureSQLDeleteIdempotent covers B2: deleting an absent database or
// server succeeds (ARM DELETE is idempotent), while deleting an existing
// database still works.
func TestSDKAzureSQLDeleteIdempotent(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	createServer(ctx, t, servers, "srv1")

	// DELETE a nonexistent database -> success, not 404.
	dbDel, err := dbs.BeginDelete(ctx, "rg-1", "srv1", "ghost", nil)
	if err != nil {
		t.Fatalf("DB BeginDelete (absent): %v", err)
	}

	if _, err := dbDel.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("DB delete of absent database should succeed: %v", err)
	}

	// DELETE a nonexistent server -> success, not 404.
	srvDel, err := servers.BeginDelete(ctx, "rg-1", "ghost-srv", nil)
	if err != nil {
		t.Fatalf("Server BeginDelete (absent): %v", err)
	}

	if _, err := srvDel.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server delete of absent server should succeed: %v", err)
	}

	// DELETE an EXISTING database still works.
	createDB(ctx, t, dbs, "srv1", "appdb", &armsql.SKU{Name: to.Ptr("S0")})

	existingDel, err := dbs.BeginDelete(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("DB BeginDelete (existing): %v", err)
	}

	if _, err := existingDel.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("existing DB delete poll: %v", err)
	}

	if _, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil); err == nil {
		t.Fatal("expected NotFound after deleting existing database")
	}
}

// TestSDKAzureSQLDatabaseIDGUID covers B3: properties.databaseId is a GUID,
// stable across repeated GETs and distinct per database.
func TestSDKAzureSQLDatabaseIDGUID(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	createServer(ctx, t, servers, "srv1")
	createDB(ctx, t, dbs, "srv1", "appdb", &armsql.SKU{Name: to.Ptr("S0")})
	createDB(ctx, t, dbs, "srv1", "otherdb", &armsql.SKU{Name: to.Ptr("S0")})

	id1 := databaseID(ctx, t, dbs, "appdb")
	id2 := databaseID(ctx, t, dbs, "appdb")
	id3 := databaseID(ctx, t, dbs, "otherdb")

	if !guidPattern.MatchString(id1) {
		t.Fatalf("databaseId %q is not a GUID", id1)
	}

	if id1 != id2 {
		t.Fatalf("databaseId not stable across GETs: %q vs %q", id1, id2)
	}

	if id1 == id3 {
		t.Fatalf("distinct databases share databaseId %q", id1)
	}
}

// databaseID fetches a database's properties.databaseId.
func databaseID(ctx context.Context, t *testing.T, dbs *armsql.DatabasesClient, name string) string {
	t.Helper()

	got, err := dbs.Get(ctx, "rg-1", "srv1", name, nil)
	if err != nil {
		t.Fatalf("Get %s: %v", name, err)
	}

	if got.Database.Properties == nil || got.Database.Properties.DatabaseID == nil {
		t.Fatalf("expected databaseId set on %s", name)
	}

	return *got.Database.Properties.DatabaseID
}
