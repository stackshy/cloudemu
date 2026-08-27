package keyspaces

import (
	"context"
	"testing"

	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// TestSnapshotRoundTripKeyspaces proves a snapshot/restore round-trip preserves
// keyspaces, tables (keyed by "keyspace/table"), and the mu-guarded tag map
// under their original identities.
func TestSnapshotRoundTripKeyspaces(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: "ks1"}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	if _, err := src.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "ks1",
		Name:         "tbl1",
		SchemaDefinition: ksdriver.SchemaDefinition{
			AllColumns:    []ksdriver.ColumnDefinition{{Name: "id", Type: "text"}},
			PartitionKeys: []ksdriver.PartitionKey{{Name: "id"}},
		},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if err := src.TagResource(ctx, src.keyspaceARN("ks1"), map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	ks, err := dst.GetKeyspace(ctx, "ks1")
	if err != nil || ks.Name != "ks1" {
		t.Fatalf("restored keyspace = %+v, err %v", ks, err)
	}

	tbl, err := dst.GetTable(ctx, "ks1", "tbl1")
	if err != nil || tbl.Name != "tbl1" {
		t.Fatalf("restored table = %+v, err %v", tbl, err)
	}

	tags, err := dst.ListTagsForResource(ctx, src.keyspaceARN("ks1"))
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags) != 1 || tags[0].Key != "env" || tags[0].Value != "prod" {
		t.Fatalf("restored tags = %+v", tags)
	}
}
