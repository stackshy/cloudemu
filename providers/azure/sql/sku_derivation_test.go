package sql

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestCreateDatabaseDerivesTierFromSKUName verifies a create that supplies only
// the service-objective (SKU) name stores the tier real Azure derives from it —
// a DTU S0 is Standard (not the old hardcoded GeneralPurpose) — and fills the
// vCore capacity encoded in the name.
func TestCreateDatabaseDerivesTierFromSKUName(t *testing.T) {
	tests := []struct {
		name         string
		sku          string
		wantTier     string
		wantCapacity int
	}{
		{name: "dtu standard", sku: "S0", wantTier: "Standard"},
		{name: "dtu basic", sku: "Basic", wantTier: "Basic"},
		{name: "dtu premium", sku: "P2", wantTier: "Premium"},
		{name: "vcore general purpose", sku: "GP_Gen5_4", wantTier: "GeneralPurpose", wantCapacity: 4},
		{name: "vcore business critical", sku: "BC_Gen5_8", wantTier: "BusinessCritical", wantCapacity: 8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			ctx := context.Background()
			mustServer(t, m, "srv1")

			db, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1", SKUName: tc.sku})
			requireNoError(t, err)

			assertEqual(t, tc.sku, db.SKUName)
			assertEqual(t, tc.wantTier, db.SKUTier)
			assertEqual(t, tc.wantCapacity, db.SKUCapacity)
		})
	}
}

// TestCreateDatabaseDefaultSKUCapacity verifies the no-SKU default (GP_Gen5_2)
// carries the vCore capacity the name encodes, not a bare zero.
func TestCreateDatabaseDefaultSKUCapacity(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustServer(t, m, "srv1")

	db, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1"})
	requireNoError(t, err)

	assertEqual(t, "GP_Gen5_2", db.SKUName)
	assertEqual(t, "GeneralPurpose", db.SKUTier)
	assertEqual(t, 2, db.SKUCapacity)
}

// TestUpdateDatabaseRedeivesTierOnResize verifies a PATCH that changes the SKU
// name re-derives the tier/capacity from the new name rather than keeping the
// stale stored tier (S0 Standard → P2 Premium).
func TestUpdateDatabaseRedeivesTierOnResize(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustServer(t, m, "srv1")

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1", SKUName: "S0"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	upd, err := m.UpdateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1", SKUName: "P2"})
	requireNoError(t, err)

	assertEqual(t, "P2", upd.SKUName)
	assertEqual(t, "Premium", upd.SKUTier)

	got, err := m.GetDatabase(ctx, "srv1", "db1")
	requireNoError(t, err)
	assertEqual(t, "Premium", got.SKUTier)
}
