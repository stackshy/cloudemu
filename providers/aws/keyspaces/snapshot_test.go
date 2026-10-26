package keyspaces

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
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

// Snapshot must not hand json.Marshal the live tag maps: TagResource mutates
// them in place, so a concurrent tag write raced the marshal (go test -race).
func TestSnapshotConcurrentWithTagResource(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	mustKeyspace(t, m, "app")

	arn := m.keyspaceARN("app")
	if err := m.TagResource(ctx, arn, map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for i := 0; i < 500; i++ {
			_ = m.TagResource(ctx, arn, map[string]string{"k" + strconv.Itoa(i%50): "v"})
		}
	}()

	for i := 0; i < 200; i++ {
		if _, err := m.Snapshot(ctx, false); err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
	}

	<-done
}

// A snapshot written by an older build can hold tags under a non-canonical ARN
// (the slash-less table form) or under an ARN that names no resource. Restore
// moves the first onto the canonical ARN, so the tags stay reachable and are
// dropped with the table, and discards the second, so a resource created later
// under that ARN doesn't inherit them.
func TestRestoreCanonicalizesLegacyTagARNs(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	mustKeyspace(t, src, "app")

	if _, err := src.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t1", SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	tableARN := src.tableARN("app", "t1")
	ghostARN := src.keyspaceARN("ghost")

	raw, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	var snap keyspacesSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	snap.Tags = map[string]map[string]string{
		tableARN:                             {"env": "prod"},
		strings.TrimSuffix(tableARN, "/"):    {"env": "old", "team": "data"},
		ghostARN:                             {"stale": "yes"},
		src.keyspaceARN("app") + "extra/x/y": {"junk": "1"},
	}

	legacy, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, legacy); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := dst.ListTagsForResource(ctx, tableARN)
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	want := []ksdriver.Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "data"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored table tags = %+v, want %+v", got, want)
	}

	if len(dst.tags) != 1 {
		t.Fatalf("restored tag store = %+v, want only the canonical table entry", dst.tags)
	}

	if _, err := dst.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: "ghost"}); err != nil {
		t.Fatalf("CreateKeyspace ghost: %v", err)
	}

	if tags, err := dst.ListTagsForResource(ctx, ghostARN); err != nil || len(tags) != 0 {
		t.Fatalf("new keyspace inherited stale tags: %+v, %v", tags, err)
	}

	if err := dst.DeleteTable(ctx, "app", "t1"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	if len(dst.tags) != 0 {
		t.Fatalf("tags left after DeleteTable: %+v", dst.tags)
	}
}
