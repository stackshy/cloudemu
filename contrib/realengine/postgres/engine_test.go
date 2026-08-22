package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/lib/pq"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/postgres"
)

// TestPostgresProvisionRoundTrip provisions a database through the engine,
// connects to it with the instance's master credentials, runs real SQL, then
// deprovisions and confirms the database is gone — the engine's own contract.
func TestPostgresProvisionRoundTrip(t *testing.T) {
	eng := postgres.New(55440)
	t.Cleanup(func() { _ = eng.Close() })

	ctx := context.Background()

	res, err := eng.Provision(ctx, config.ProvisionRequest{
		InstanceID: "db1", Engine: "postgres", DBName: "app", Username: "admin", Password: "secret",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	dsn := fmt.Sprintf("host=%s port=%d user=admin password=secret dbname=app sslmode=disable", res.Host, res.Port)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := db.Exec("CREATE TABLE widgets (id int primary key, name text)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec("INSERT INTO widgets VALUES (1, 'cloudemu')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM widgets WHERE id = 1").Scan(&name); err != nil {
		t.Fatalf("select: %v", err)
	}

	if name != "cloudemu" {
		t.Fatalf("round-trip mismatch: got %q", name)
	}

	_ = db.Close()

	if err := eng.Deprovision(ctx, "db1"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}

	// The database is gone: a fresh connection to it must fail.
	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deprovisioned database to fail")
	}
}
