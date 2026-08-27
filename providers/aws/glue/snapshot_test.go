package glue_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// TestSnapshotRoundTripGlue proves a snapshot/restore round-trip preserves the
// Glue mock's state under the original (composite) identities: a Data Catalog
// database, a table promoted with its version machinery, and an ETL job all
// survive restore into a fresh mock.
func TestSnapshotRoundTripGlue(t *testing.T) {
	ctx := context.Background()
	const cat = "123456789012"

	src := newMock()

	if err := src.CreateDatabase(ctx, cat, driver.Database{Name: "db1", Description: "d"}); err != nil {
		t.Fatalf("create database: %v", err)
	}

	if err := src.CreateTable(ctx, cat, "db1", driver.Table{Name: "tbl1", TableType: "EXTERNAL_TABLE"}); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := src.CreateJob(ctx, driver.Job{Name: "job1", Role: "arn:aws:iam::123456789012:role/r"}); err != nil {
		t.Fatalf("create job: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	db, err := dst.GetDatabase(ctx, cat, "db1")
	if err != nil {
		t.Fatalf("get restored database: %v", err)
	}

	if db.Name != "db1" || db.Description != "d" {
		t.Fatalf("restored database = %+v, want name db1 / desc d", db)
	}

	tbl, err := dst.GetTable(ctx, cat, "db1", "tbl1")
	if err != nil {
		t.Fatalf("get restored table: %v", err)
	}

	if tbl.Name != "tbl1" || tbl.TableType != "EXTERNAL_TABLE" {
		t.Fatalf("restored table = %+v, want name tbl1 / type EXTERNAL_TABLE", tbl)
	}

	job, err := dst.GetJob(ctx, "job1")
	if err != nil {
		t.Fatalf("get restored job: %v", err)
	}

	if job.Name != "job1" {
		t.Fatalf("restored job name = %q, want job1", job.Name)
	}
}
