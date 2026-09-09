package bigquery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

const testProject = "proj-1"

func newMock(t *testing.T) *Mock {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2024, 9, 5, 12, 0, 0, 0, time.UTC))

	return New(&config.Options{Clock: clock})
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertGetDataset(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	created, err := m.InsertDataset(ctx, testProject, &driver.Dataset{
		DatasetID:    "ds1",
		FriendlyName: "friendly",
		Labels:       map[string]string{"env": "test"},
	})
	requireNoError(t, err)

	if created.Location != defaultLocation {
		t.Fatalf("location = %q, want default %q", created.Location, defaultLocation)
	}

	if created.Etag == "" {
		t.Fatal("etag not set on create")
	}

	if created.CreationTime.IsZero() {
		t.Fatal("creationTime not set")
	}

	got, err := m.GetDataset(ctx, testProject, "ds1")
	requireNoError(t, err)

	if got.FriendlyName != "friendly" || got.Labels["env"] != "test" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestInsertDatasetDuplicate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{DatasetID: "ds1"})
	requireNoError(t, err)

	_, err = m.InsertDataset(ctx, testProject, &driver.Dataset{DatasetID: "ds1"})
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want AlreadyExists, got %v", err)
	}
}

func TestGetDatasetNotFound(t *testing.T) {
	m := newMock(t)

	_, err := m.GetDataset(context.Background(), testProject, "missing")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestPatchDatasetMergesLabels(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{
		DatasetID: "ds1",
		Labels:    map[string]string{"a": "1", "b": "2"},
	})
	requireNoError(t, err)

	desc := "patched"
	patched, err := m.PatchDataset(ctx, testProject, "ds1", &driver.DatasetPatch{
		Description: &desc,
		Labels:      map[string]string{"b": "22", "c": "3"},
		LabelsSet:   true,
	})
	requireNoError(t, err)

	if patched.Description != "patched" {
		t.Fatalf("description = %q", patched.Description)
	}

	// Merge: a preserved, b overwritten, c added.
	want := map[string]string{"a": "1", "b": "22", "c": "3"}
	for k, v := range want {
		if patched.Labels[k] != v {
			t.Fatalf("label %s = %q, want %q (labels=%v)", k, patched.Labels[k], v, patched.Labels)
		}
	}
}

func TestUpdateDatasetReplacesLabels(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{
		DatasetID:    "ds1",
		FriendlyName: "orig",
		Labels:       map[string]string{"a": "1"},
	})
	requireNoError(t, err)

	// Replace: friendlyName omitted -> cleared; labels swapped.
	updated, err := m.UpdateDataset(ctx, testProject, "ds1", &driver.DatasetPatch{
		Labels:    map[string]string{"x": "9"},
		LabelsSet: true,
	})
	requireNoError(t, err)

	if updated.FriendlyName != "" {
		t.Fatalf("friendlyName not cleared on update: %q", updated.FriendlyName)
	}

	if _, ok := updated.Labels["a"]; ok {
		t.Fatalf("old label survived replace: %v", updated.Labels)
	}

	if updated.Labels["x"] != "9" {
		t.Fatalf("new label missing: %v", updated.Labels)
	}
}

func TestPatchDatasetEtagPrecondition(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{DatasetID: "ds1"})
	requireNoError(t, err)

	_, err = m.PatchDataset(ctx, testProject, "ds1", &driver.DatasetPatch{Etag: "wrong"})
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("want FailedPrecondition on etag mismatch, got %v", err)
	}
}

func TestDeleteDatasetContentsGuard(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{DatasetID: "ds1"})
	requireNoError(t, err)

	_, err = m.InsertTable(ctx, testProject, "ds1", &driver.Table{TableID: "t1"})
	requireNoError(t, err)

	// Non-empty without deleteContents -> FailedPrecondition.
	if err := m.DeleteDataset(ctx, testProject, "ds1", false); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}

	// deleteContents=true succeeds.
	requireNoError(t, m.DeleteDataset(ctx, testProject, "ds1", true))

	if _, err := m.GetDataset(ctx, testProject, "ds1"); !cerrors.IsNotFound(err) {
		t.Fatalf("dataset still present after delete: %v", err)
	}
}

func TestListDatasetsOrdered(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"c", "a", "b"} {
		_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{DatasetID: id})
		requireNoError(t, err)
	}

	got, err := m.ListDatasets(ctx, testProject)
	requireNoError(t, err)

	if len(got) != 3 || got[0].DatasetID != "a" || got[1].DatasetID != "b" || got[2].DatasetID != "c" {
		t.Fatalf("list not deterministically ordered: %+v", got)
	}
}

func TestInsertTableNestedRecordRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{DatasetID: "ds1"})
	requireNoError(t, err)

	schema := []driver.Field{
		{Name: "id", Type: "INTEGER", Mode: "REQUIRED"},
		{Name: "name", Type: "STRING"}, // no mode -> default NULLABLE downstream
		{Name: "addr", Type: "RECORD", Mode: "NULLABLE", Fields: []driver.Field{
			{Name: "city", Type: "STRING"},
			{Name: "zip", Type: "STRING", Mode: "REQUIRED"},
		}},
	}

	created, err := m.InsertTable(ctx, testProject, "ds1", &driver.Table{TableID: "t1", Schema: schema})
	requireNoError(t, err)

	if created.Type != tableTypeTable {
		t.Fatalf("type = %q, want TABLE", created.Type)
	}

	got, err := m.GetTable(ctx, testProject, "ds1", "t1")
	requireNoError(t, err)

	if len(got.Schema) != 3 {
		t.Fatalf("schema len = %d", len(got.Schema))
	}

	rec := got.Schema[2]
	if rec.Type != "RECORD" || len(rec.Fields) != 2 || rec.Fields[1].Name != "zip" || rec.Fields[1].Mode != "REQUIRED" {
		t.Fatalf("nested RECORD did not round-trip: %+v", rec)
	}
}

func TestInsertTableViewImpliesType(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{DatasetID: "ds1"})
	requireNoError(t, err)

	created, err := m.InsertTable(ctx, testProject, "ds1", &driver.Table{
		TableID: "v1",
		View:    &driver.ViewDefinition{Query: "SELECT 1", UseLegacySQL: false},
	})
	requireNoError(t, err)

	if created.Type != tableTypeView {
		t.Fatalf("view table type = %q, want VIEW", created.Type)
	}

	if created.View == nil || created.View.Query != "SELECT 1" {
		t.Fatalf("view query did not round-trip: %+v", created.View)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.InsertDataset(ctx, testProject, &driver.Dataset{DatasetID: "ds1", Labels: map[string]string{"k": "v"}})
	requireNoError(t, err)
	_, err = m.InsertTable(ctx, testProject, "ds1", &driver.Table{
		TableID: "t1",
		Schema:  []driver.Field{{Name: "c", Type: "STRING", Mode: "NULLABLE"}},
	})
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, true)
	requireNoError(t, err)

	restored := New(&config.Options{})
	requireNoError(t, restored.Restore(ctx, data))

	ds, err := restored.GetDataset(ctx, testProject, "ds1")
	requireNoError(t, err)

	if ds.Labels["k"] != "v" {
		t.Fatalf("dataset label lost after restore: %+v", ds)
	}

	tbl, err := restored.GetTable(ctx, testProject, "ds1", "t1")
	requireNoError(t, err)

	if len(tbl.Schema) != 1 || tbl.Schema[0].Name != "c" {
		t.Fatalf("table schema lost after restore: %+v", tbl)
	}
}
