package dockerengine_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine"
)

// TestMySQLRejectsRootUser proves a "root" master username fails loudly at
// Provision rather than reporting success with credentials that never
// authenticate.
func TestMySQLRejectsRootUser(t *testing.T) {
	if !dockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := dockerengine.NewMySQL(3309)
	t.Cleanup(func() { _ = eng.Close() })

	_, err := eng.Provision(context.Background(), config.ProvisionRequest{
		InstanceID: "r", Engine: "mysql", DBName: "r", Username: "root", Password: "x",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "root") {
		t.Fatalf("want a root-reserved error, got %v", err)
	}
}

// TestMySQLNoCrossTenantGrant proves two instances that pin the same master
// username do not leak database access to each other: the most-recent instance's
// account is scoped to its own database, and the superseded password stops
// working (documented last-writer-wins for a shared username).
func TestMySQLNoCrossTenantGrant(t *testing.T) {
	if !dockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := dockerengine.NewMySQL(3309)
	t.Cleanup(func() { _ = eng.Close() })

	ctx := context.Background()

	if _, err := eng.Provision(ctx, config.ProvisionRequest{
		InstanceID: "a", Engine: "mysql", DBName: "dba", Username: "shared", Password: "pwA-secret",
	}); err != nil {
		t.Fatalf("provision a: %v", err)
	}

	res, err := eng.Provision(ctx, config.ProvisionRequest{
		InstanceID: "b", Engine: "mysql", DBName: "dbb", Username: "shared", Password: "pwB-secret",
	})
	if err != nil {
		t.Fatalf("provision b: %v", err)
	}

	// Instance B's current credentials work against its own database...
	dbB, err := sql.Open("mysql", fmt.Sprintf("shared:pwB-secret@tcp(%s:%d)/dbb", res.Host, res.Port))
	if err != nil {
		t.Fatalf("open dbb: %v", err)
	}
	defer dbB.Close()

	if err := dbB.Ping(); err != nil {
		t.Fatalf("connect to dbb with current password must work: %v", err)
	}

	// ...but must NOT be able to touch instance A's database.
	if _, err := dbB.Exec("CREATE TABLE dba.leak (id int)"); err == nil {
		t.Fatal("cross-tenant leak: shared account wrote into another instance's database")
	}

	// The superseded first password no longer authenticates.
	stale, _ := sql.Open("mysql", fmt.Sprintf("shared:pwA-secret@tcp(%s:%d)/dbb", res.Host, res.Port))
	defer stale.Close()

	if err := stale.Ping(); err == nil {
		t.Fatal("stale password should no longer authenticate after the account was recreated")
	}
}
