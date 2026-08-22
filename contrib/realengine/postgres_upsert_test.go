package realengine_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/lib/pq"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
)

// TestPostgresRolePasswordUpsert proves the ensureRole password upsert: two
// instances that pin the same master username (as Cloud SQL does with
// "postgres") share one role on the shared server, and the most recently
// provisioned password must actually authenticate — the earlier "create IF NOT
// EXISTS" only ever honoured the first instance's password.
func TestPostgresRolePasswordUpsert(t *testing.T) {
	eng := realengine.NewPostgres(55441)
	t.Cleanup(func() { _ = eng.Close() })

	ctx := context.Background()

	if _, err := eng.Provision(ctx, config.ProvisionRequest{
		InstanceID: "inst-a", Engine: "postgres", DBName: "inst-a",
		Username: "postgres", Password: "first-password",
	}); err != nil {
		t.Fatalf("provision inst-a: %v", err)
	}

	res, err := eng.Provision(ctx, config.ProvisionRequest{
		InstanceID: "inst-b", Engine: "postgres", DBName: "inst-b",
		Username: "postgres", Password: "second-password",
	})
	if err != nil {
		t.Fatalf("provision inst-b: %v", err)
	}

	// The role now carries inst-b's password (last writer wins) — connect with it.
	dsn := fmt.Sprintf("host=%s port=%d user=postgres password=%s dbname=inst-b sslmode=disable",
		res.Host, res.Port, "second-password")

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("connect with the most-recent password must succeed: %v", err)
	}

	// The superseded first password must no longer authenticate.
	stale := fmt.Sprintf("host=%s port=%d user=postgres password=%s dbname=inst-b sslmode=disable",
		res.Host, res.Port, "first-password")

	staleDB, _ := sql.Open("postgres", stale)
	defer staleDB.Close()

	if err := staleDB.Ping(); err == nil {
		t.Fatal("stale first password should no longer authenticate after upsert")
	}
}
