package spanner

import (
	"context"
	"testing"

	spdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
)

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	mustInstance(t, src, "i", 2, 0)

	dbName := instName("i") + "/databases/orders"
	if _, _, err := src.CreateDatabase(ctx, spdriver.CreateDatabaseConfig{
		Parent:          instName("i"),
		Name:            dbName,
		ExtraStatements: []string{"CREATE TABLE t (id INT64) PRIMARY KEY(id)"},
	}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if _, _, err := src.UpdateDatabaseDdl(ctx, dbName, []string{"CREATE INDEX idx ON t(id)"}); err != nil {
		t.Fatalf("UpdateDatabaseDdl: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Identity is preserved: the instance resolves under its original name with
	// its derived capacity.
	inst, err := dst.GetInstance(ctx, instName("i"))
	if err != nil {
		t.Fatalf("GetInstance after restore: %v", err)
	}

	if inst.NodeCount != 2 || inst.ProcessingUnits != 2000 {
		t.Fatalf("restored capacity = %d/%d, want 2/2000", inst.NodeCount, inst.ProcessingUnits)
	}

	// The database and its accumulated DDL survive.
	ddl, err := dst.GetDatabaseDdl(ctx, dbName)
	if err != nil || len(ddl) != 2 {
		t.Fatalf("restored DDL = %v (%v), want 2 statements", ddl, err)
	}

	// A fresh operation after restore must not collide with the restored ids.
	if _, op, err := dst.UpdateInstance(ctx, instName("i"), spdriver.UpdateInstanceConfig{
		NodeCount: 3, FieldMask: []string{"nodecount"},
	}); err != nil || op == nil {
		t.Fatalf("UpdateInstance after restore: %v op=%+v", err, op)
	}
}
