package bigtable

import (
	"context"
	"testing"

	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

// TestSnapshotRoundTripBigtable proves a snapshot/restore round-trip preserves an
// instance, its initial cluster, a table, and a resource IAM policy under their
// original resource names.
func TestSnapshotRoundTripBigtable(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	inst := mustInstance(t, src, "app")

	if _, err := src.CreateTable(ctx, btdriver.CreateTableConfig{
		Parent: inst, TableID: "t1",
		ColumnFamilies: map[string]btdriver.ColumnFamily{"cf1": {}},
	}); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := src.SetIamPolicy(ctx, inst, btdriver.Policy{
		Bindings: []btdriver.Binding{{Role: "roles/bigtable.user", Members: []string{"user:a@b.com"}}},
	}); err != nil {
		t.Fatalf("set iam policy: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got, err := dst.GetInstance(ctx, inst); err != nil || got.DisplayName != "app" {
		t.Fatalf("restored instance = %+v, err %v", got, err)
	}

	clusters, err := dst.ListClusters(ctx, inst)
	if err != nil || len(clusters) != 1 || clusters[0].ServeNodes != 3 {
		t.Fatalf("restored clusters = %+v, err %v", clusters, err)
	}

	tbl, err := dst.GetTable(ctx, inst+"/tables/t1")
	if err != nil {
		t.Fatalf("get restored table: %v", err)
	}

	if _, ok := tbl.ColumnFamilies["cf1"]; !ok {
		t.Fatalf("restored table missing column family cf1: %+v", tbl.ColumnFamilies)
	}

	pol, err := dst.GetIamPolicy(ctx, inst)
	if err != nil || len(pol.Bindings) != 1 || pol.Bindings[0].Role != "roles/bigtable.user" {
		t.Fatalf("restored policy = %+v, err %v", pol, err)
	}
}
