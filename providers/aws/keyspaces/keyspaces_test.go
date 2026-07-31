package keyspaces

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))

	return New(opts)
}

func mustKeyspace(t *testing.T, m *Mock, name string) {
	t.Helper()

	if _, err := m.CreateKeyspace(context.Background(), ksdriver.CreateKeyspaceConfig{Name: name}); err != nil {
		t.Fatalf("CreateKeyspace %s: %v", name, err)
	}
}

func sampleSchema() ksdriver.SchemaDefinition {
	return ksdriver.SchemaDefinition{
		AllColumns: []ksdriver.ColumnDefinition{
			{Name: "id", Type: "uuid"}, {Name: "ts", Type: "timestamp"}, {Name: "val", Type: "text"},
		},
		PartitionKeys:  []ksdriver.PartitionKey{{Name: "id"}},
		ClusteringKeys: []ksdriver.ClusteringKey{{Name: "ts", OrderBy: "DESC"}},
	}
}

func TestKeyspaceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	ks, err := m.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: "app"})
	if err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	if ks.ReplicationStrategy != ksdriver.SingleRegion || len(ks.ReplicationRegions) != 1 {
		t.Fatalf("default replication wrong: %+v", ks)
	}

	// Duplicate rejected.
	if _, err := m.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: "app"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate keyspace: got %v, want AlreadyExists", err)
	}

	// Update to multi-region.
	upd, err := m.UpdateKeyspace(ctx, "app", []string{"us-west-2"})
	if err != nil {
		t.Fatalf("UpdateKeyspace: %v", err)
	}

	if upd.ReplicationStrategy != ksdriver.MultiRegion || len(upd.ReplicationRegions) != 2 {
		t.Fatalf("update to multi-region failed: %+v", upd)
	}

	// List includes the three system keyspaces + app.
	list, err := m.ListKeyspaces(ctx)
	if err != nil {
		t.Fatalf("ListKeyspaces: %v", err)
	}

	if len(list) != 4 {
		t.Fatalf("expected 4 keyspaces, got %d", len(list))
	}
}

func TestKeyspaceMultiRegionRequiresTwoRegions(t *testing.T) {
	m := newTestMock()

	_, err := m.CreateKeyspace(context.Background(), ksdriver.CreateKeyspaceConfig{
		Name: "mr", ReplicationStrategy: ksdriver.MultiRegion, ReplicationRegions: []string{"us-east-1"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("multi-region with one region: got %v, want InvalidArgument", err)
	}
}

func TestDeleteKeyspaceBlockedWithTables(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t1", SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if err := m.DeleteKeyspace(ctx, "app"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete non-empty keyspace: got %v, want FailedPrecondition", err)
	}

	if err := m.DeleteTable(ctx, "app", "t1"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	if err := m.DeleteKeyspace(ctx, "app"); err != nil {
		t.Fatalf("delete empty keyspace: %v", err)
	}
}

func TestTableLifecycleAndValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	// Missing keyspace rejected.
	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "ghost", Name: "t", SchemaDefinition: sampleSchema(),
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("table in missing keyspace: got %v, want InvalidArgument", err)
	}

	// No partition key rejected.
	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "bad",
		SchemaDefinition: ksdriver.SchemaDefinition{AllColumns: []ksdriver.ColumnDefinition{{Name: "id", Type: "uuid"}}},
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("no partition key: got %v, want InvalidArgument", err)
	}

	// Undeclared partition key rejected.
	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "bad2",
		SchemaDefinition: ksdriver.SchemaDefinition{PartitionKeys: []ksdriver.PartitionKey{{Name: "missing"}}},
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("undeclared partition key: got %v, want InvalidArgument", err)
	}

	tbl, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t1", SchemaDefinition: sampleSchema(),
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// Default capacity mode is PAY_PER_REQUEST; PITR/TTL default DISABLED.
	if tbl.CapacitySpecification.ThroughputMode != ksdriver.ThroughputPayPerRequest {
		t.Fatalf("default throughput mode: %+v", tbl.CapacitySpecification)
	}

	if tbl.EncryptionSpecification.Type != "AWS_OWNED_KMS_KEY" {
		t.Fatalf("default encryption: %+v", tbl.EncryptionSpecification)
	}

	// Update: add a column, enable PITR, switch to provisioned.
	ttl := 3600
	upd, err := m.UpdateTable(ctx, ksdriver.UpdateTableConfig{
		KeyspaceName: "app", Name: "t1",
		AddColumns:            []ksdriver.ColumnDefinition{{Name: "extra", Type: "int"}},
		PointInTimeRecovery:   "ENABLED",
		DefaultTimeToLive:     &ttl,
		CapacitySpecification: &ksdriver.CapacitySpecification{ThroughputMode: ksdriver.ThroughputProvisioned},
	})
	if err != nil {
		t.Fatalf("UpdateTable: %v", err)
	}

	if len(upd.SchemaDefinition.AllColumns) != 4 || upd.PointInTimeRecoveryStatus != "ENABLED" || upd.DefaultTimeToLive != 3600 {
		t.Fatalf("update not applied: %+v", upd)
	}

	if upd.CapacitySpecification.ReadCapacityUnits != 1 || upd.CapacitySpecification.WriteCapacityUnits != 1 {
		t.Fatalf("provisioned defaults not set: %+v", upd.CapacitySpecification)
	}
}

func TestRestoreTable(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "src", SchemaDefinition: sampleSchema(), Comment: "original",
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	restored, err := m.RestoreTable(ctx, ksdriver.RestoreTableConfig{
		SourceKeyspace: "app", SourceTable: "src", TargetKeyspace: "app", TargetTable: "dst",
		RestoreTimestamp: m.opts.Clock.Now(),
	})
	if err != nil {
		t.Fatalf("RestoreTable: %v", err)
	}

	if restored.Name != "dst" || restored.Comment != "original" || len(restored.SchemaDefinition.PartitionKeys) != 1 {
		t.Fatalf("restore lost shape: %+v", restored)
	}
}

func TestUserDefinedTypes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateType(ctx, "app", "empty", nil); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("type with no fields: got %v, want InvalidArgument", err)
	}

	u, err := m.CreateType(ctx, "app", "address", []ksdriver.FieldDefinition{
		{Name: "street", Type: "text"}, {Name: "zip", Type: "int"},
	})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}

	if u.Status != ksdriver.StatusActive || len(u.FieldDefinitions) != 2 {
		t.Fatalf("type wrong: %+v", u)
	}

	got, err := m.GetType(ctx, "app", "address")
	if err != nil || got.Name != "address" {
		t.Fatalf("GetType: %v %+v", err, got)
	}

	types, err := m.ListTypes(ctx, "app")
	if err != nil || len(types) != 1 {
		t.Fatalf("ListTypes: %v %+v", err, types)
	}

	if _, err := m.DeleteType(ctx, "app", "address"); err != nil {
		t.Fatalf("DeleteType: %v", err)
	}
}

func TestTagsDeterministic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	arn := m.keyspaceARN("app")
	if err := m.TagResource(ctx, arn, map[string]string{"team": "data", "env": "prod"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := m.ListTagsForResource(ctx, arn)
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	// Sorted by key: env, team.
	if len(tags) != 2 || tags[0].Key != "env" || tags[1].Key != "team" {
		t.Fatalf("tags not sorted deterministically: %+v", tags)
	}

	if err := m.UntagResource(ctx, arn, []string{"env"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, _ = m.ListTagsForResource(ctx, arn)
	if len(tags) != 1 || tags[0].Key != "team" {
		t.Fatalf("untag failed: %+v", tags)
	}
}

func TestAutoScalingRequiresProvisioned(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	// PAY_PER_REQUEST table has no auto scaling.
	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "ppr", SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if _, err := m.GetTableAutoScalingSettings(ctx, "app", "ppr"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("autoscaling on PPR table: got %v, want InvalidArgument", err)
	}

	// PROVISIONED table with auto scaling returns settings.
	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "prov", SchemaDefinition: sampleSchema(),
		CapacitySpecification: ksdriver.CapacitySpecification{ThroughputMode: ksdriver.ThroughputProvisioned},
		AutoScaling: &ksdriver.AutoScalingSpecification{
			Read: &ksdriver.AutoScalingSettings{MinimumUnits: 1, MaximumUnits: 10, TargetValue: 70},
		},
	}); err != nil {
		t.Fatalf("CreateTable provisioned: %v", err)
	}

	as, err := m.GetTableAutoScalingSettings(ctx, "app", "prov")
	if err != nil {
		t.Fatalf("GetTableAutoScalingSettings: %v", err)
	}

	if as.AutoScaling == nil || as.AutoScaling.Read == nil || as.AutoScaling.Read.MaximumUnits != 10 {
		t.Fatalf("autoscaling settings not returned: %+v", as.AutoScaling)
	}
}

func TestUDTInUseGuard(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateType(ctx, "app", "address", []ksdriver.FieldDefinition{{Name: "street", Type: "text"}}); err != nil {
		t.Fatalf("CreateType: %v", err)
	}

	// A table with a frozen<address> column registers a reference.
	schema := sampleSchema()
	schema.AllColumns = append(schema.AllColumns, ksdriver.ColumnDefinition{Name: "addr", Type: "frozen<address>"})

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{KeyspaceName: "app", Name: "people", SchemaDefinition: schema}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// DeleteType is now blocked (guard is live).
	if _, err := m.DeleteType(ctx, "app", "address"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete in-use type: got %v, want FailedPrecondition", err)
	}

	// Dropping the referring table unblocks the delete.
	if err := m.DeleteTable(ctx, "app", "people"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	if _, err := m.DeleteType(ctx, "app", "address"); err != nil {
		t.Fatalf("delete type after table gone: %v", err)
	}
}

func TestSchemaRejectsUndeclaredStaticColumn(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	schema := sampleSchema()
	schema.StaticColumns = []ksdriver.StaticColumn{{Name: "undeclared"}}

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{KeyspaceName: "app", Name: "t", SchemaDefinition: schema}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("undeclared static column: got %v, want InvalidArgument", err)
	}
}

func TestDeleteKeyspaceGuardsTypesAndSystem(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateType(ctx, "app", "address", []ksdriver.FieldDefinition{{Name: "s", Type: "text"}}); err != nil {
		t.Fatalf("CreateType: %v", err)
	}

	if err := m.DeleteKeyspace(ctx, "app"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete keyspace with types: got %v, want FailedPrecondition", err)
	}

	// System keyspaces cannot be deleted.
	if err := m.DeleteKeyspace(ctx, "system"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("delete system keyspace: got %v, want InvalidArgument", err)
	}

	// GetType on a missing keyspace is NotFound (not an orphan lookup).
	if _, err := m.GetType(ctx, "ghost", "address"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetType missing keyspace: got %v, want NotFound", err)
	}
}

func TestCreateKeyspaceClonesRegions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	regions := []string{"us-east-1", "us-west-2"}
	if _, err := m.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{
		Name: "mr", ReplicationStrategy: ksdriver.MultiRegion, ReplicationRegions: regions,
	}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	regions[0] = "MUTATED" // must not reach the store

	got, _ := m.GetKeyspace(ctx, "mr")
	if got.ReplicationRegions[0] != "us-east-1" {
		t.Fatalf("stored keyspace aliases caller slice: %v", got.ReplicationRegions)
	}
}

func TestCreateKeyspaceRejectsBadStrategy(t *testing.T) {
	m := newTestMock()

	if _, err := m.CreateKeyspace(context.Background(), ksdriver.CreateKeyspaceConfig{
		Name: "bad", ReplicationStrategy: "GARBAGE",
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad strategy: got %v, want InvalidArgument", err)
	}
}

func TestUpdateTableRejectsDuplicateColumn(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{KeyspaceName: "app", Name: "t", SchemaDefinition: sampleSchema()}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// "val" already exists in sampleSchema.
	if _, err := m.UpdateTable(ctx, ksdriver.UpdateTableConfig{
		KeyspaceName: "app", Name: "t", AddColumns: []ksdriver.ColumnDefinition{{Name: "val", Type: "text"}},
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("duplicate column: got %v, want InvalidArgument", err)
	}
}

func TestRestoreTableRejectsFutureTimestamp(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{KeyspaceName: "app", Name: "src", SchemaDefinition: sampleSchema()}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	future := m.opts.Clock.Now().Add(24 * time.Hour)
	if _, err := m.RestoreTable(ctx, ksdriver.RestoreTableConfig{
		SourceKeyspace: "app", SourceTable: "src", TargetKeyspace: "app", TargetTable: "dst", RestoreTimestamp: future,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("future restore timestamp: got %v, want InvalidArgument", err)
	}
}

func TestTableResultDoesNotAliasStore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t1", SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	got, _ := m.GetTable(ctx, "app", "t1")
	got.SchemaDefinition.AllColumns[0].Name = "MUTATED"

	again, _ := m.GetTable(ctx, "app", "t1")
	if again.SchemaDefinition.AllColumns[0].Name == "MUTATED" {
		t.Fatal("returned table aliases the store (clone-on-read broken)")
	}
}
