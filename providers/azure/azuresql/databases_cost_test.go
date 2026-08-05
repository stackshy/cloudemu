package azuresql

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestCreateDatabaseCarriesSKUAndZoneRedundancy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	db, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server:        "srv1",
		Name:          "db1",
		SKUName:       "GP_Gen5_4",
		SKUTier:       "GeneralPurpose",
		ZoneRedundant: true,
	})
	requireNoError(t, err)

	assertEqual(t, "srv1", db.Server)
	assertEqual(t, "db1", db.Name)
	assertEqual(t, "GP_Gen5_4", db.SKUName)
	assertEqual(t, "GeneralPurpose", db.SKUTier)
	assertEqual(t, true, db.ZoneRedundant)
	assertNotEmpty(t, db.ARN)
}

func TestCreateDatabaseDefaultsSKU(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	db, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1"})
	requireNoError(t, err)

	assertEqual(t, "GP_Gen5_2", db.SKUName)
	assertEqual(t, "GeneralPurpose", db.SKUTier)
}

func TestDatabaseGetListRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1"}); err != nil {
		t.Fatalf("CreateDatabase srv1/db1: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db2"}); err != nil {
		t.Fatalf("CreateDatabase srv1/db2: %v", err)
	}

	// A database on a different server must not leak into srv1's listing.
	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv2", Name: "other"}); err != nil {
		t.Fatalf("CreateDatabase srv2/other: %v", err)
	}

	got, err := m.GetDatabase(ctx, "srv1", "db1")
	requireNoError(t, err)
	assertEqual(t, "db1", got.Name)
	assertEqual(t, "srv1", got.Server)

	list, err := m.ListDatabases(ctx, "srv1")
	requireNoError(t, err)
	assertEqual(t, 2, len(list))

	for _, db := range list {
		assertEqual(t, "srv1", db.Server)
	}

	other, err := m.ListDatabases(ctx, "srv2")
	requireNoError(t, err)
	assertEqual(t, 1, len(other))
}

func TestCreateDatabaseDuplicateRejected(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1"}); err == nil {
		t.Error("duplicate CreateDatabase: expected AlreadyExists")
	}
}

func TestGetAndDeleteDatabaseMissing(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.GetDatabase(ctx, "srv1", "ghost"); err == nil {
		t.Error("GetDatabase on missing database: expected NotFound")
	}

	if err := m.DeleteDatabase(ctx, "srv1", "ghost"); err == nil {
		t.Error("DeleteDatabase on missing database: expected NotFound")
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if err := m.DeleteDatabase(ctx, "srv1", "db1"); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	if _, err := m.GetDatabase(ctx, "srv1", "db1"); err == nil {
		t.Error("GetDatabase after delete: expected NotFound")
	}
}
