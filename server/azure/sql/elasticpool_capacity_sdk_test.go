package sql_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

const (
	poolCapacity        = int32(4)
	poolCapacityUpdated = int32(8)
)

// createPool creates an elastic pool with the given SKU and waits for the
// long-running create to finish.
func createPool(ctx context.Context, t *testing.T, ep *armsql.ElasticPoolsClient, name string, sku *armsql.SKU) {
	t.Helper()

	poller, err := ep.BeginCreateOrUpdate(ctx, "rg-1", "srv1", name, armsql.ElasticPool{
		Location: to.Ptr("eastus"),
		SKU:      sku,
	}, nil)
	if err != nil {
		t.Fatalf("pool BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool PollUntilDone: %v", err)
	}
}

// TestSDKAzureSQLElasticPoolCapacityRoundTrip covers the elastic-pool analogue
// of the database B1 fix: a pool's sku.capacity must round-trip on GET and
// List. azurerm_mssql_elasticpool makes sku.capacity REQUIRED, so dropping it
// causes perpetual Terraform drift.
func TestSDKAzureSQLElasticPoolCapacityRoundTrip(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()
	mustCreateSQLServer(t, cf)

	ep := cf.NewElasticPoolsClient()

	createPool(ctx, t, ep, "pool1", &armsql.SKU{
		Name:     to.Ptr("GP_Gen5"),
		Tier:     to.Ptr("GeneralPurpose"),
		Capacity: to.Ptr(poolCapacity),
	})

	got, err := ep.Get(ctx, "rg-1", "srv1", "pool1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ElasticPool.SKU == nil || got.ElasticPool.SKU.Capacity == nil {
		t.Fatal("expected sku.capacity set on GET")
	}

	if *got.ElasticPool.SKU.Capacity != poolCapacity {
		t.Fatalf("sku.capacity = %d, want %d", *got.ElasticPool.SKU.Capacity, poolCapacity)
	}

	// List must reflect the capacity too.
	page, err := ep.NewListByServerPager("rg-1", "srv1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d pools, want 1", len(page.Value))
	}

	if page.Value[0].SKU == nil || page.Value[0].SKU.Capacity == nil ||
		*page.Value[0].SKU.Capacity != poolCapacity {
		t.Fatalf("list sku.capacity = %+v, want %d", page.Value[0].SKU, poolCapacity)
	}
}

// TestSDKAzureSQLElasticPoolCapacityPatch covers PATCH semantics: a PATCH that
// changes capacity applies it, and a later PATCH that omits capacity preserves
// the existing value (guarded merge).
func TestSDKAzureSQLElasticPoolCapacityPatch(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()
	mustCreateSQLServer(t, cf)

	ep := cf.NewElasticPoolsClient()

	createPool(ctx, t, ep, "pool1", &armsql.SKU{
		Name:     to.Ptr("GP_Gen5"),
		Tier:     to.Ptr("GeneralPurpose"),
		Capacity: to.Ptr(poolCapacity),
	})

	// PATCH changing capacity applies.
	updPoller, err := ep.BeginUpdate(ctx, "rg-1", "srv1", "pool1", armsql.ElasticPoolUpdate{
		SKU: &armsql.SKU{Capacity: to.Ptr(poolCapacityUpdated)},
	}, nil)
	if err != nil {
		t.Fatalf("pool BeginUpdate: %v", err)
	}

	if _, err := updPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool update PollUntilDone: %v", err)
	}

	got, err := ep.Get(ctx, "rg-1", "srv1", "pool1", nil)
	if err != nil {
		t.Fatalf("Get after capacity PATCH: %v", err)
	}

	if got.ElasticPool.SKU == nil || got.ElasticPool.SKU.Capacity == nil ||
		*got.ElasticPool.SKU.Capacity != poolCapacityUpdated {
		t.Fatalf("sku.capacity after PATCH = %+v, want %d", got.ElasticPool.SKU, poolCapacityUpdated)
	}

	// PATCH omitting capacity preserves the existing value.
	preservePoller, err := ep.BeginUpdate(ctx, "rg-1", "srv1", "pool1", armsql.ElasticPoolUpdate{
		Properties: &armsql.ElasticPoolUpdateProperties{MaxSizeBytes: to.Ptr(int64(1 << 30))},
	}, nil)
	if err != nil {
		t.Fatalf("pool BeginUpdate (preserve): %v", err)
	}

	if _, err := preservePoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool update (preserve) PollUntilDone: %v", err)
	}

	got, err = ep.Get(ctx, "rg-1", "srv1", "pool1", nil)
	if err != nil {
		t.Fatalf("Get after preserve PATCH: %v", err)
	}

	if got.ElasticPool.SKU == nil || got.ElasticPool.SKU.Capacity == nil ||
		*got.ElasticPool.SKU.Capacity != poolCapacityUpdated {
		t.Fatalf("sku.capacity after capacity-omitting PATCH = %+v, want preserved %d",
			got.ElasticPool.SKU, poolCapacityUpdated)
	}
}

// TestSDKAzureSQLServerVersionDefault covers G3: a server create that omits
// properties.version reports Azure's synthesized default "12.0", while an
// explicit version round-trips unchanged.
func TestSDKAzureSQLServerVersionDefault(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()
	servers := cf.NewServersClient()

	// Create omitting version -> GET reports "12.0".
	poller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "noversion", armsql.Server{
		Location: to.Ptr("eastus"),
		Properties: &armsql.ServerProperties{
			AdministratorLogin:         to.Ptr("admin"),
			AdministratorLoginPassword: to.Ptr("Sup3rs3cret!"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("server BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server PollUntilDone: %v", err)
	}

	got, err := servers.Get(ctx, "rg-1", "noversion", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Server.Properties == nil || got.Server.Properties.Version == nil {
		t.Fatal("expected version set on GET")
	}

	if *got.Server.Properties.Version != "12.0" {
		t.Fatalf("version = %q, want %q", *got.Server.Properties.Version, "12.0")
	}

	// Explicit version round-trips unchanged.
	exPoller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "withversion", armsql.Server{
		Location: to.Ptr("eastus"),
		Properties: &armsql.ServerProperties{
			AdministratorLogin:         to.Ptr("admin"),
			AdministratorLoginPassword: to.Ptr("Sup3rs3cret!"),
			Version:                    to.Ptr("2.0"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("explicit server BeginCreateOrUpdate: %v", err)
	}

	if _, err := exPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("explicit server PollUntilDone: %v", err)
	}

	gotEx, err := servers.Get(ctx, "rg-1", "withversion", nil)
	if err != nil {
		t.Fatalf("Get explicit: %v", err)
	}

	if gotEx.Server.Properties == nil || gotEx.Server.Properties.Version == nil ||
		*gotEx.Server.Properties.Version != "2.0" {
		t.Fatalf("explicit version = %+v, want %q", gotEx.Server.Properties, "2.0")
	}
}
