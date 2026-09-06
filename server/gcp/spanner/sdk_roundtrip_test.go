package spanner_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/api/option"
	sp "google.golang.org/api/spanner/v1"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/stackshy/cloudemu/v2/config"
	cloudsqlprovider "github.com/stackshy/cloudemu/v2/providers/gcp/cloudsql"
	spannerprovider "github.com/stackshy/cloudemu/v2/providers/gcp/spanner"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	project = "mock-project"
	cfgURL  = "projects/mock-project/instanceConfigs/regional-us-central1"
)

// newServer stands up the full GCP wire server with BOTH Spanner and Cloud SQL
// wired, so the round-trip also proves the two coexist on the shared
// /v1/projects/{p}/instances URL space.
func newServer(t *testing.T) (*sp.Service, *sqladmin.Service) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithProjectID(project))

	srv := gcpserver.New(gcpserver.Drivers{
		Spanner:  spannerprovider.New(opts),
		CloudSQL: cloudsqlprovider.New(opts),
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	spSvc, err := sp.NewService(context.Background(), option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("spanner.NewService: %v", err)
	}

	sqlSvc, err := sqladmin.NewService(context.Background(), option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("sqladmin.NewService: %v", err)
	}

	return spSvc, sqlSvc
}

func TestSDKSpannerInstanceAndDatabaseRoundTrip(t *testing.T) {
	spSvc, _ := newServer(t)
	parent := "projects/" + project

	// Create instance (LRO) — num_nodes=1.
	op, err := spSvc.Projects.Instances.Create(parent, &sp.CreateInstanceRequest{
		InstanceId: "orders",
		Instance: &sp.Instance{
			Config:      cfgURL,
			DisplayName: "Orders",
			NodeCount:   1,
			Labels:      map[string]string{"env": "test"},
		},
	}).Do()
	if err != nil {
		t.Fatalf("Instances.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done: %+v", op)
	}

	// Get instance — full name, config, displayName, derived nodeCount+PU, READY.
	name := parent + "/instances/orders"

	inst, err := spSvc.Projects.Instances.Get(name).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	if inst.Name != name || inst.Config != cfgURL || inst.DisplayName != "Orders" {
		t.Fatalf("instance identity: %+v", inst)
	}

	if inst.NodeCount != 1 || inst.ProcessingUnits != 1000 {
		t.Fatalf("capacity: got %d/%d, want 1/1000", inst.NodeCount, inst.ProcessingUnits)
	}

	if inst.State != "READY" || inst.Labels["env"] != "test" {
		t.Fatalf("state/labels: state=%q labels=%v", inst.State, inst.Labels)
	}

	// Patch instance (LRO, fieldMask) — scale to 2 nodes.
	pop, err := spSvc.Projects.Instances.Patch(name, &sp.UpdateInstanceRequest{
		FieldMask: "nodeCount",
		Instance:  &sp.Instance{Name: name, NodeCount: 2},
	}).Do()
	if err != nil || !pop.Done {
		t.Fatalf("Instances.Patch: %v op=%+v", err, pop)
	}

	inst, _ = spSvc.Projects.Instances.Get(name).Do()
	if inst.NodeCount != 2 || inst.ProcessingUnits != 2000 || inst.DisplayName != "Orders" {
		t.Fatalf("after patch: %+v", inst)
	}

	// Create database (LRO) with initial DDL.
	dop, err := spSvc.Projects.Instances.Databases.Create(name, &sp.CreateDatabaseRequest{
		CreateStatement: "CREATE DATABASE `catalog`",
		ExtraStatements: []string{"CREATE TABLE t (id INT64) PRIMARY KEY(id)"},
	}).Do()
	if err != nil || !dop.Done {
		t.Fatalf("Databases.Create: %v op=%+v", err, dop)
	}

	dbName := name + "/databases/catalog"

	db, err := spSvc.Projects.Instances.Databases.Get(dbName).Do()
	if err != nil {
		t.Fatalf("Databases.Get: %v", err)
	}

	if db.State != "READY" || db.DatabaseDialect != "GOOGLE_STANDARD_SQL" {
		t.Fatalf("database: state=%q dialect=%q", db.State, db.DatabaseDialect)
	}

	// updateDdl (LRO) — add a table.
	uop, err := spSvc.Projects.Instances.Databases.UpdateDdl(dbName, &sp.UpdateDatabaseDdlRequest{
		Statements: []string{"CREATE TABLE t2 (id INT64) PRIMARY KEY(id)"},
	}).Do()
	if err != nil || !uop.Done {
		t.Fatalf("Databases.UpdateDdl: %v op=%+v", err, uop)
	}

	ddl, err := spSvc.Projects.Instances.Databases.GetDdl(dbName).Do()
	if err != nil || len(ddl.Statements) != 2 {
		t.Fatalf("Databases.GetDdl: got %v (%v), want 2 statements", ddl.Statements, err)
	}

	// List databases.
	dbs, err := spSvc.Projects.Instances.Databases.List(name).Do()
	if err != nil || len(dbs.Databases) != 1 {
		t.Fatalf("Databases.List: got %d (%v), want 1", len(dbs.Databases), err)
	}

	// Drop database (sync) then delete instance (sync).
	if _, err := spSvc.Projects.Instances.Databases.DropDatabase(dbName).Do(); err != nil {
		t.Fatalf("Databases.DropDatabase: %v", err)
	}

	if _, err := spSvc.Projects.Instances.Delete(name).Do(); err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	if _, err := spSvc.Projects.Instances.Get(name).Do(); err == nil {
		t.Fatalf("instance still present after delete")
	}
}

// TestCloudSQLCoexistence proves the Spanner handler does not shadow Cloud SQL's
// own /v1/projects/{p}/instances traffic when the two use distinct instance ids:
// a Cloud SQL insert (a body without the Spanner shape) falls through to Cloud
// SQL, and each service's instance resolves to its own backend.
func TestCloudSQLCoexistence(t *testing.T) {
	spSvc, sqlSvc := newServer(t)

	// Cloud SQL insert — body has no instanceId/instance, so Spanner declines the
	// POST and it falls through to Cloud SQL.
	if _, err := sqlSvc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "sql-orders",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Do(); err != nil {
		t.Fatalf("Cloud SQL Instances.Insert: %v", err)
	}

	// A Spanner instance with a distinct id.
	if _, err := spSvc.Projects.Instances.Create("projects/"+project, &sp.CreateInstanceRequest{
		InstanceId: "spanner-orders",
		Instance:   &sp.Instance{Config: cfgURL, NodeCount: 1},
	}).Do(); err != nil {
		t.Fatalf("Spanner Create alongside Cloud SQL: %v", err)
	}

	// Cloud SQL get resolves to the Cloud SQL backend (not shadowed by Spanner).
	sqlInst, err := sqlSvc.Instances.Get(project, "sql-orders").Do()
	if err != nil {
		t.Fatalf("Cloud SQL Instances.Get: %v", err)
	}

	if sqlInst.DatabaseVersion != "POSTGRES_15" {
		t.Fatalf("Cloud SQL instance shadowed: %+v", sqlInst)
	}

	// Spanner get resolves to the Spanner backend: a full resource name and the
	// echoed config prove it did not fall through to Cloud SQL.
	spInst, err := spSvc.Projects.Instances.Get("projects/" + project + "/instances/spanner-orders").Do()
	if err != nil {
		t.Fatalf("Spanner Instances.Get: %v", err)
	}

	if spInst.Name != "projects/"+project+"/instances/spanner-orders" || spInst.Config != cfgURL {
		t.Fatalf("Spanner instance not resolved from its own backend: %+v", spInst)
	}
}
