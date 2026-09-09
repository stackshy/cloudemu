package sql_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// TestSDKAzureSQLDatabaseSKUFamilyAndTier verifies that a database create which
// supplies only the service-objective (SKU) name reads back the tier, hardware
// family and vCore capacity real Azure derives from it — so armsql / az CLI /
// Resource Graph see a vCore GP_Gen5_2 as GeneralPurpose/Gen5/2 and a DTU S0 as
// Standard (not the old hardcoded GeneralPurpose for every database).
func TestSDKAzureSQLDatabaseSKUFamilyAndTier(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	createServer(ctx, t, servers, "srv1")

	// vCore: name alone must yield tier + family + capacity.
	createDB(ctx, t, dbs, "srv1", "vcdb", &armsql.SKU{Name: to.Ptr("GP_Gen5_2")})

	got, err := dbs.Get(ctx, "rg-1", "srv1", "vcdb", nil)
	if err != nil {
		t.Fatalf("Get vcdb: %v", err)
	}

	assertSKU(t, got.Database.SKU, "GeneralPurpose", "Gen5", 2)
	assertSKU(t, got.Database.Properties.CurrentSKU, "GeneralPurpose", "Gen5", 2)

	// DTU: S0 is Standard tier, no hardware family.
	createDB(ctx, t, dbs, "srv1", "dtudb", &armsql.SKU{Name: to.Ptr("S0")})

	gotDTU, err := dbs.Get(ctx, "rg-1", "srv1", "dtudb", nil)
	if err != nil {
		t.Fatalf("Get dtudb: %v", err)
	}

	if gotDTU.Database.SKU == nil || gotDTU.Database.SKU.Tier == nil || *gotDTU.Database.SKU.Tier != "Standard" {
		t.Fatalf("dtu sku.tier = %v, want Standard", gotDTU.Database.SKU)
	}

	if gotDTU.Database.SKU.Family != nil {
		t.Fatalf("dtu sku.family = %v, want unset", *gotDTU.Database.SKU.Family)
	}
}

// assertSKU checks a returned armsql SKU's tier/family/capacity.
func assertSKU(t *testing.T, sku *armsql.SKU, tier, family string, capacity int32) {
	t.Helper()

	if sku == nil {
		t.Fatal("sku is nil")
	}

	if sku.Tier == nil || *sku.Tier != tier {
		t.Errorf("sku.tier = %v, want %s", sku.Tier, tier)
	}

	if sku.Family == nil || *sku.Family != family {
		t.Errorf("sku.family = %v, want %s", sku.Family, family)
	}

	if sku.Capacity == nil || *sku.Capacity != capacity {
		t.Errorf("sku.capacity = %v, want %d", sku.Capacity, capacity)
	}
}
