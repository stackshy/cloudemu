package spanner

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	spdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
)

const (
	proj   = "p1"
	cfgURL = "projects/p1/instanceConfigs/regional-us-central1"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithAccountID(proj))

	return New(opts)
}

func instName(id string) string { return "projects/" + proj + "/instances/" + id }

func mustInstance(t *testing.T, m *Mock, id string, nodeCount, pu int64) *spdriver.Instance {
	t.Helper()

	inst, op, err := m.CreateInstance(context.Background(), spdriver.CreateInstanceConfig{
		Name:            instName(id),
		Config:          cfgURL,
		DisplayName:     id,
		NodeCount:       nodeCount,
		ProcessingUnits: pu,
	})
	if err != nil {
		t.Fatalf("CreateInstance %s: %v", id, err)
	}

	if op == nil || !op.Done {
		t.Fatalf("CreateInstance %s: want done operation, got %+v", id, op)
	}

	return inst
}

func TestCreateInstanceReadyAndCapacityDerivation(t *testing.T) {
	m := newTestMock()

	// node_count=1 must derive processing_units=1000 AND report READY at once.
	inst := mustInstance(t, m, "byNodes", 1, 0)
	if inst.State != spdriver.StateReady {
		t.Fatalf("state = %q, want READY", inst.State)
	}

	if inst.NodeCount != 1 || inst.ProcessingUnits != 1000 {
		t.Fatalf("derivation from node_count: got nodes=%d pu=%d, want 1/1000", inst.NodeCount, inst.ProcessingUnits)
	}

	if inst.Config != cfgURL || inst.DisplayName != "byNodes" {
		t.Fatalf("config/displayName not echoed: %+v", inst)
	}

	// processing_units=2000 must derive node_count=2.
	pu := mustInstance(t, m, "byPU", 0, 2000)
	if pu.NodeCount != 2 || pu.ProcessingUnits != 2000 {
		t.Fatalf("derivation from processing_units: got nodes=%d pu=%d, want 2/2000", pu.NodeCount, pu.ProcessingUnits)
	}

	// Sub-node capacity (500 PU) yields node_count=0, matching real Spanner.
	sub := mustInstance(t, m, "sub", 0, 500)
	if sub.NodeCount != 0 || sub.ProcessingUnits != 500 {
		t.Fatalf("sub-node derivation: got nodes=%d pu=%d, want 0/500", sub.NodeCount, sub.ProcessingUnits)
	}
}

func TestCreateInstanceValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, _, err := m.CreateInstance(ctx, spdriver.CreateInstanceConfig{Name: instName("noconfig")}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing config: got %v, want InvalidArgument", err)
	}

	mustInstance(t, m, "dup", 1, 0)

	if _, _, err := m.CreateInstance(ctx, spdriver.CreateInstanceConfig{Name: instName("dup"), Config: cfgURL}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate: got %v, want AlreadyExists", err)
	}
}

func TestGetListDeleteInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	mustInstance(t, m, "a", 1, 0)
	mustInstance(t, m, "b", 1, 0)

	if _, err := m.GetInstance(ctx, instName("missing")); !cerrors.IsNotFound(err) {
		t.Fatalf("get missing: got %v, want NotFound", err)
	}

	list, err := m.ListInstances(ctx, proj)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListInstances: got %d (%v), want 2", len(list), err)
	}

	// Delete cascades databases.
	if _, _, err := m.CreateDatabase(ctx, spdriver.CreateDatabaseConfig{
		Parent: instName("a"), Name: instName("a") + "/databases/d1",
	}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if err := m.DeleteInstance(ctx, instName("a")); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if _, err := m.GetDatabase(ctx, instName("a")+"/databases/d1"); !cerrors.IsNotFound(err) {
		t.Fatalf("database not cascade-deleted: %v", err)
	}

	if err := m.DeleteInstance(ctx, instName("a")); !cerrors.IsNotFound(err) {
		t.Fatalf("delete missing: got %v, want NotFound", err)
	}
}

func TestUpdateInstanceMasked(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustInstance(t, m, "u", 1, 0)

	// Mask only node_count: display name must be untouched, PU re-derived.
	inst, op, err := m.UpdateInstance(ctx, instName("u"), spdriver.UpdateInstanceConfig{
		DisplayName: "ignored", NodeCount: 3, FieldMask: []string{"nodecount"},
	})
	if err != nil || !op.Done {
		t.Fatalf("UpdateInstance: %v op=%+v", err, op)
	}

	if inst.NodeCount != 3 || inst.ProcessingUnits != 3000 {
		t.Fatalf("masked node_count update: got %d/%d, want 3/3000", inst.NodeCount, inst.ProcessingUnits)
	}

	if inst.DisplayName != "u" {
		t.Fatalf("displayName changed despite mask omission: %q", inst.DisplayName)
	}

	// Mask displayName only.
	inst, _, err = m.UpdateInstance(ctx, instName("u"), spdriver.UpdateInstanceConfig{
		DisplayName: "renamed", FieldMask: []string{"displayname"},
	})
	if err != nil {
		t.Fatalf("UpdateInstance displayName: %v", err)
	}

	if inst.DisplayName != "renamed" || inst.NodeCount != 3 {
		t.Fatalf("masked displayName update: %+v", inst)
	}
}

func TestDatabaseLifecycleAndDDL(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustInstance(t, m, "i", 1, 0)

	dbName := instName("i") + "/databases/orders"

	// Create under a missing instance fails.
	if _, _, err := m.CreateDatabase(ctx, spdriver.CreateDatabaseConfig{
		Parent: instName("nope"), Name: instName("nope") + "/databases/x",
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("create db under missing instance: got %v, want NotFound", err)
	}

	db, op, err := m.CreateDatabase(ctx, spdriver.CreateDatabaseConfig{
		Parent:          instName("i"),
		Name:            dbName,
		ExtraStatements: []string{"CREATE TABLE t (id INT64) PRIMARY KEY(id)"},
	})
	if err != nil || !op.Done {
		t.Fatalf("CreateDatabase: %v op=%+v", err, op)
	}

	if db.State != spdriver.StateReady {
		t.Fatalf("db state = %q, want READY", db.State)
	}

	if db.DatabaseDialect != spdriver.DialectGoogleStandardSQL {
		t.Fatalf("dialect = %q, want default GOOGLE_STANDARD_SQL", db.DatabaseDialect)
	}

	if len(db.DDL) != 1 {
		t.Fatalf("initial DDL = %v, want 1 statement", db.DDL)
	}

	// updateDdl appends.
	if _, _, err := m.UpdateDatabaseDdl(ctx, dbName, []string{"CREATE TABLE t2 (id INT64) PRIMARY KEY(id)"}); err != nil {
		t.Fatalf("UpdateDatabaseDdl: %v", err)
	}

	ddl, err := m.GetDatabaseDdl(ctx, dbName)
	if err != nil || len(ddl) != 2 {
		t.Fatalf("GetDatabaseDdl: got %v (%v), want 2 statements", ddl, err)
	}

	dbs, err := m.ListDatabases(ctx, instName("i"))
	if err != nil || len(dbs) != 1 {
		t.Fatalf("ListDatabases: got %d (%v), want 1", len(dbs), err)
	}

	if err := m.DropDatabase(ctx, dbName); err != nil {
		t.Fatalf("DropDatabase: %v", err)
	}

	if err := m.DropDatabase(ctx, dbName); !cerrors.IsNotFound(err) {
		t.Fatalf("drop missing: got %v, want NotFound", err)
	}
}

func TestGetOperationDone(t *testing.T) {
	m := newTestMock()
	inst := mustInstance(t, m, "op", 1, 0)

	// The recorded create operation resolves as done.
	op, err := m.GetOperation(context.Background(), inst.Name+"/operations/spanner-create-instance-1")
	if err != nil || !op.Done {
		t.Fatalf("GetOperation recorded: %v op=%+v", err, op)
	}

	// An unknown operation is reported done (synchronous completion).
	unknown, err := m.GetOperation(context.Background(), inst.Name+"/operations/does-not-exist")
	if err != nil || !unknown.Done {
		t.Fatalf("GetOperation unknown: %v op=%+v", err, unknown)
	}
}
