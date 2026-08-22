package realengine_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/lib/pq"
	alloydbapi "google.golang.org/api/alloydb/v1"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestAlloyDBPostgresE2E runs the real-user flow against GCP AlloyDB: create a
// cluster with an initial user + password using the AlloyDB Admin SDK, create a
// PRIMARY instance, read the instance IP the SDK reports, connect a real
// Postgres client to that IP on 5432 with the initial user, run SQL, then
// delete — all against CloudEmu backed by a real embedded Postgres (no Docker,
// no cloud account). The client connects using ONLY the SDK-reported IP.
//
// AlloyDB clients always connect on 5432 (the SDK never surfaces a port), so the
// engine listens there. The per-instance database is named by the instance ID.
func TestAlloyDBPostgresE2E(t *testing.T) {
	// Default engine port (5432) — the port AlloyDB clients always use.
	eng := realengine.NewPostgres(0)
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewGCP(config.WithDatabaseEngine(eng),
		config.WithRegion("us-central1"), config.WithProjectID("my-project"))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{AlloyDB: cloud.AlloyDB}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svc, err := alloydbapi.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("alloydb.NewService: %v", err)
	}

	const (
		parent    = "projects/my-project/locations/us-central1"
		clusterID = "app-cluster"
		instID    = "app-primary"
		user      = "postgres"
		pass      = "AlloyDB-Secret-Pw"
	)

	// 1. Create the cluster with an initial user + password.
	if _, err := svc.Projects.Locations.Clusters.Create(parent, &alloydbapi.Cluster{
		DatabaseVersion: "POSTGRES_15",
		Network:         "default",
		InitialUser:     &alloydbapi.UserPassword{User: user, Password: pass},
	}).ClusterId(clusterID).Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	// 2. Create the PRIMARY instance.
	if _, err := svc.Projects.Locations.Clusters.Instances.Create(parent+"/clusters/"+clusterID,
		&alloydbapi.Instance{
			InstanceType:  "PRIMARY",
			MachineConfig: &alloydbapi.MachineConfig{CpuCount: 2},
		}).InstanceId(instID).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Create: %v", err)
	}

	// 3. Read the instance IP the SDK reports — the real embedded Postgres
	//    address. Connect using ONLY the SDK-reported IP.
	inst, err := svc.Projects.Locations.Clusters.Instances.Get(
		parent + "/clusters/" + clusterID + "/instances/" + instID).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	if inst.IpAddress == "" {
		t.Fatalf("no instance IP reported: %+v", inst)
	}

	// AlloyDB clients always connect on 5432; the per-instance database is named
	// by the instance ID.
	dsn := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable",
		inst.IpAddress, user, pass, instID)

	// 4. Connect with a real Postgres client and run real SQL.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	db.SetConnMaxLifetime(time.Minute)

	if _, err := db.Exec("CREATE TABLE orders (id serial primary key, item text, qty int)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec("INSERT INTO orders (item, qty) VALUES ($1, $2)", "widget", 7); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var (
		item string
		qty  int
	)

	if err := db.QueryRow("SELECT item, qty FROM orders WHERE id = 1").Scan(&item, &qty); err != nil {
		t.Fatalf("select: %v", err)
	}

	if item != "widget" || qty != 7 {
		t.Fatalf("round-trip mismatch: got %q, %d", item, qty)
	}

	_ = db.Close()

	// 5. Delete the instance — the real database is torn down.
	if _, err := svc.Projects.Locations.Clusters.Instances.Delete(
		parent + "/clusters/" + clusterID + "/instances/" + instID).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted instance's database to fail")
	}
}
