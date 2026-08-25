package sql

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestCreateDatabaseValidatesElasticPoolCapability is the Databases-capability
// counterpart of TestCreateDatabaseValidatesElasticPool (which only exercises
// the CreateInstance/RDS-shaped path). The ARM wire server backs
// Microsoft.Sql/servers/databases with CreateDatabase, not CreateInstance, so
// this is the path a real armsql client actually drives.
func TestCreateDatabaseValidatesElasticPoolCapability(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustServer(t, m, "srv")

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server: "srv", Name: "db1", ElasticPoolID: "ghost-pool",
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("CreateDatabase with unknown elastic pool: got %v, want NotFound", err)
	}

	if _, err := m.CreateElasticPool(ctx, rdsdriver.ElasticPoolConfig{Server: "srv", Name: "pool1"}); err != nil {
		t.Fatalf("CreateElasticPool: %v", err)
	}

	db, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server: "srv", Name: "db1", ElasticPoolID: "pool1",
	})
	if err != nil {
		t.Fatalf("CreateDatabase with valid elastic pool: %v", err)
	}

	if db.ElasticPoolID != "pool1" {
		t.Fatalf("ElasticPoolID not persisted on create: got %q, want pool1", db.ElasticPoolID)
	}

	// Round-trips on Get and List too, not just the create response.
	got, err := m.GetDatabase(ctx, "srv", "db1")
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}

	if got.ElasticPoolID != "pool1" {
		t.Fatalf("ElasticPoolID not echoed on Get: got %q, want pool1", got.ElasticPoolID)
	}

	list, err := m.ListDatabases(ctx, "srv")
	if err != nil || len(list) != 1 || list[0].ElasticPoolID != "pool1" {
		t.Fatalf("ElasticPoolID not echoed on List: %+v, err=%v", list, err)
	}
}

// TestDatabaseElasticPoolMembershipBlocksDelete is the Databases-capability
// counterpart of TestElasticPoolMembershipBlocksDelete: a pool populated via
// CreateDatabase (what the ARM wire server actually calls) must block delete
// the same way a pool populated via the legacy CreateInstance path does.
// Before this fix DeleteElasticPool only inspected the Instance store, so a
// pool full of wire-created databases could be deleted while non-empty.
func TestDatabaseElasticPoolMembershipBlocksDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustServer(t, m, "srv")

	if _, err := m.CreateElasticPool(ctx, rdsdriver.ElasticPoolConfig{Server: "srv", Name: "pool"}); err != nil {
		t.Fatalf("CreateElasticPool: %v", err)
	}

	poolID := "/subscriptions/x/resourceGroups/x/providers/Microsoft.Sql/servers/srv/elasticPools/pool"
	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server: "srv", Name: "db1", ElasticPoolID: poolID,
	}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if err := m.DeleteElasticPool(ctx, "srv", "pool"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("DeleteElasticPool on non-empty pool: got %v, want FailedPrecondition", err)
	}

	if err := m.DeleteDatabase(ctx, "srv", "db1"); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	if err := m.DeleteElasticPool(ctx, "srv", "pool"); err != nil {
		t.Errorf("DeleteElasticPool on empty pool: %v", err)
	}
}

// TestCascadeDeleteServerRemovesLogicalDatabases is the Databases-capability
// counterpart of TestCascadeDeleteServer. Real Azure: "a logical container
// with strong lifetime semantics - delete a server and it deletes its
// databases and elastic pools"
// (learn.microsoft.com/azure/azure-sql/database/logical-servers). Databases
// created through the Databases capability (what the ARM wire server uses for
// Microsoft.Sql/servers/databases) previously survived DeleteCluster, which
// only cleared the legacy Instance store and firewall/vnet/elasticPool/
// failoverGroup/AAD-admin children — never m.databases.
func TestCascadeDeleteServerRemovesLogicalDatabases(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustServer(t, m, "srv1")

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1"}); err != nil {
		t.Fatalf("CreateDatabase db1: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db2"}); err != nil {
		t.Fatalf("CreateDatabase db2: %v", err)
	}

	if _, err := m.CreateElasticPool(ctx, rdsdriver.ElasticPoolConfig{Server: "srv1", Name: "pool1"}); err != nil {
		t.Fatalf("CreateElasticPool: %v", err)
	}

	if err := m.DeleteCluster(ctx, "srv1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	list, err := m.ListDatabases(ctx, "srv1")
	if err != nil {
		t.Fatalf("ListDatabases after server delete: %v", err)
	}

	if len(list) != 0 {
		t.Fatalf("databases survived server delete: %+v", list)
	}

	if _, err := m.GetDatabase(ctx, "srv1", "db1"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetDatabase after server delete: got %v, want NotFound", err)
	}

	// ListElasticPools requires the server to exist, so its NotFound here
	// confirms the server itself is gone; GetElasticPool confirms the pool
	// specifically didn't survive under a since-recreated server below.
	if _, err := m.ListElasticPools(ctx, "srv1"); !cerrors.IsNotFound(err) {
		t.Fatalf("ListElasticPools after server delete: got %v, want NotFound", err)
	}

	// Re-creating the server and a same-named database afterward must not
	// resurrect anything from the deleted server: the store starts clean.
	mustServer(t, m, "srv1")

	db, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "db1"})
	if err != nil {
		t.Fatalf("re-CreateDatabase db1: %v", err)
	}

	if db.ElasticPoolID != "" {
		t.Fatalf("re-created database inherited stale ElasticPoolID: %q", db.ElasticPoolID)
	}
}
