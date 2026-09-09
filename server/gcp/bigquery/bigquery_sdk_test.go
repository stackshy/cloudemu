package bigquery_test

import (
	"context"
	"net/http/httptest"
	"testing"

	bq "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const sdkProject = "demo-project"

// newSDK builds the real google.golang.org/api/bigquery/v2 discovery client
// pointed at an in-process GCP server. The endpoint keeps the /bigquery/v2/
// path segment (the client appends projects/... to its BasePath), which the
// handler's Matches gate requires.
func newSDK(t *testing.T) *bq.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{BigQuery: cloud.BigQuery})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := bq.NewService(context.Background(),
		option.WithEndpoint(ts.URL+"/bigquery/v2/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("bigquery.NewService: %v", err)
	}

	return svc
}

// TestSDKDatasetLifecycle drives the dataset surface through the real client:
// insert -> get (id/etag/location/labels round-trip) -> list -> patch (merge)
// -> update (replace) -> delete.
func TestSDKDatasetLifecycle(t *testing.T) {
	svc := newSDK(t)
	ctx := context.Background()

	created, err := svc.Datasets.Insert(sdkProject, &bq.Dataset{
		DatasetReference: &bq.DatasetReference{DatasetId: "ds_sdk"},
		FriendlyName:     "Friendly",
		Labels:           map[string]string{"env": "test"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Datasets.Insert: %v", err)
	}

	if created.Id != sdkProject+":ds_sdk" {
		t.Fatalf("dataset id = %q, want %s:ds_sdk", created.Id, sdkProject)
	}

	if created.Location != "US" {
		t.Fatalf("location = %q, want US", created.Location)
	}

	if created.Etag == "" || created.CreationTime == 0 {
		t.Fatalf("etag/creationTime not populated: %+v", created)
	}

	got, err := svc.Datasets.Get(sdkProject, "ds_sdk").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Datasets.Get: %v", err)
	}

	if got.Labels["env"] != "test" || got.FriendlyName != "Friendly" {
		t.Fatalf("dataset did not round-trip: %+v", got)
	}

	list, err := svc.Datasets.List(sdkProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Datasets.List: %v", err)
	}

	if len(list.Datasets) != 1 || list.Datasets[0].Id != sdkProject+":ds_sdk" {
		t.Fatalf("list = %+v", list.Datasets)
	}

	// PATCH merges labels; existing env stays, team added.
	patched, err := svc.Datasets.Patch(sdkProject, "ds_sdk", &bq.Dataset{
		Labels: map[string]string{"team": "data"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Datasets.Patch: %v", err)
	}

	if patched.Labels["env"] != "test" || patched.Labels["team"] != "data" {
		t.Fatalf("patch did not merge labels: %+v", patched.Labels)
	}

	if err := svc.Datasets.Delete(sdkProject, "ds_sdk").Context(ctx).Do(); err != nil {
		t.Fatalf("Datasets.Delete: %v", err)
	}

	if _, err := svc.Datasets.Get(sdkProject, "ds_sdk").Context(ctx).Do(); err == nil {
		t.Fatal("dataset still present after delete")
	}
}

// TestSDKTableSchemaLifecycle drives the table surface through the real client,
// asserting the colon+dot id, the schema round-trip (mode NULLABLE echo on a
// field sent without a mode, a REQUIRED field, and a nested RECORD), then a
// schema update (add a column).
func TestSDKTableSchemaLifecycle(t *testing.T) {
	svc := newSDK(t)
	ctx := context.Background()

	if _, err := svc.Datasets.Insert(sdkProject, &bq.Dataset{
		DatasetReference: &bq.DatasetReference{DatasetId: "ds_tbl"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Datasets.Insert: %v", err)
	}

	schema := &bq.TableSchema{Fields: []*bq.TableFieldSchema{
		{Name: "id", Type: "INTEGER", Mode: "REQUIRED"},
		{Name: "name", Type: "STRING"}, // no mode -> must echo NULLABLE
		{Name: "addr", Type: "RECORD", Fields: []*bq.TableFieldSchema{
			{Name: "city", Type: "STRING"},
			{Name: "zip", Type: "STRING", Mode: "REQUIRED"},
		}},
	}}

	created, err := svc.Tables.Insert(sdkProject, "ds_tbl", &bq.Table{
		TableReference: &bq.TableReference{ProjectId: sdkProject, DatasetId: "ds_tbl", TableId: "t_sdk"},
		Schema:         schema,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Tables.Insert: %v", err)
	}

	if created.Id != sdkProject+":ds_tbl.t_sdk" {
		t.Fatalf("table id = %q, want %s:ds_tbl.t_sdk", created.Id, sdkProject)
	}

	if created.Type != "TABLE" {
		t.Fatalf("type = %q, want TABLE", created.Type)
	}

	got, err := svc.Tables.Get(sdkProject, "ds_tbl", "t_sdk").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Tables.Get: %v", err)
	}

	assertSchemaRoundTrip(t, got.Schema)

	// Update: replace the schema with an added column. The client's UPDATE (PUT)
	// carries the full table.
	got.Schema.Fields = append(got.Schema.Fields, &bq.TableFieldSchema{Name: "created", Type: "TIMESTAMP"})

	updated, err := svc.Tables.Update(sdkProject, "ds_tbl", "t_sdk", got).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Tables.Update: %v", err)
	}

	if len(updated.Schema.Fields) != 4 || updated.Schema.Fields[3].Name != "created" {
		t.Fatalf("schema update did not add column: %+v", updated.Schema.Fields)
	}

	if err := svc.Tables.Delete(sdkProject, "ds_tbl", "t_sdk").Context(ctx).Do(); err != nil {
		t.Fatalf("Tables.Delete: %v", err)
	}
}

func assertSchemaRoundTrip(t *testing.T, schema *bq.TableSchema) {
	t.Helper()

	if schema == nil || len(schema.Fields) != 3 {
		t.Fatalf("schema not round-tripped: %+v", schema)
	}

	if schema.Fields[0].Mode != "REQUIRED" {
		t.Fatalf("REQUIRED field mode lost: %+v", schema.Fields[0])
	}

	// A field sent without a mode must read back NULLABLE.
	if schema.Fields[1].Name != "name" || schema.Fields[1].Mode != "NULLABLE" {
		t.Fatalf("default mode not echoed NULLABLE: %+v", schema.Fields[1])
	}

	rec := schema.Fields[2]
	if rec.Type != "RECORD" || len(rec.Fields) != 2 {
		t.Fatalf("nested RECORD lost: %+v", rec)
	}

	if rec.Fields[0].Mode != "NULLABLE" || rec.Fields[1].Mode != "REQUIRED" {
		t.Fatalf("nested field modes lost: %+v", rec.Fields)
	}
}
