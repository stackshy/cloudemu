package realengine_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCPCloudSQLPostgresE2E runs the real-user flow against GCP Cloud SQL for
// PostgreSQL: create the instance with the real Cloud SQL Admin SDK (setting the
// root password), read the reported ipAddresses[].ipAddress, connect to it with
// a real Postgres client using the root credentials, run SQL, then delete — all
// against CloudEmu backed by a real embedded Postgres (no Docker, no cloud
// account).
func TestGCPCloudSQLPostgresE2E(t *testing.T) {
	// Default engine port (5432) — the port Cloud SQL clients always use; the
	// SDK never surfaces a port in ipAddresses.
	eng := realengine.NewPostgres(0)
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewGCP(config.WithDatabaseEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudSQL: cloud.CloudSQL}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svc, err := sqladmin.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("sqladmin.NewService: %v", err)
	}

	const (
		project  = "my-project"
		instance = "app-db"
		// The Cloud SQL PostgreSQL root user is fixed to "postgres"; its password
		// is the rootPassword set on insert.
		user = "postgres"
		pass = "R00t-Passw0rd"
	)

	// 1. Create the instance — like `gcloud sql instances create`.
	op, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            instance,
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		RootPassword:    pass,
		Settings: &sqladmin.Settings{
			Tier:           "db-custom-2-8192",
			DataDiskSizeGb: 20,
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Insert: %v", err)
	}

	if op.Status != "DONE" {
		t.Fatalf("insert op status %q, want DONE", op.Status)
	}

	// 2. Read the instance and pull the PRIMARY IP the SDK reports (the real
	//    embedded Postgres address). Connect using ONLY the SDK-reported IP.
	got, err := svc.Instances.Get(project, instance).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	host := primaryIP(t, got)

	// Cloud SQL PostgreSQL clients always connect on 5432; the provisioned
	// database defaults to the instance name when insert carries no dbName.
	dsn := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable",
		host, user, pass, instance)

	// 3. Connect with a real Postgres client and run real SQL.
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

	// 4. Delete the instance — the real database is torn down.
	if _, err := svc.Instances.Delete(project, instance).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted instance's database to fail")
	}
}

// primaryIP returns the PRIMARY ipAddress the SDK reports for inst, failing the
// test if none is present. The e2e connects using only this SDK-reported IP.
func primaryIP(t *testing.T, inst *sqladmin.DatabaseInstance) string {
	t.Helper()

	for _, ip := range inst.IpAddresses {
		if ip.Type == "PRIMARY" {
			return ip.IpAddress
		}
	}

	t.Fatalf("no PRIMARY ipAddress reported: %+v", inst.IpAddresses)

	return ""
}
