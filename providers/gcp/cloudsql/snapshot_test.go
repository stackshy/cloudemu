package cloudsql

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestSnapshotRoundTripCloudSQL proves the mock serializes its entire state and
// restores it into a fresh mock identity-preservingly: re-snapshotting the
// restored mock yields byte-identical JSON (so every store round-tripped), and
// the seeded instance, database, and user come back under their original ids.
func TestSnapshotRoundTripCloudSQL(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "pg1", Engine: "POSTGRES_15", MasterUsername: "postgres", MasterUserPassword: "secret", DBName: "app",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := src.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "pg1", Name: "orders"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if _, err := src.CreateUser(ctx, rdsdriver.UserConfig{Instance: "pg1", Name: "app-user", Password: "pw"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	data2, err := dst.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("re-Snapshot: %v", err)
	}

	if string(data) != string(data2) {
		t.Fatalf("snapshot not stable across restore:\n%s\nvs\n%s", data, data2)
	}

	insts, err := dst.DescribeInstances(ctx, []string{"pg1"})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	if len(insts) != 1 || insts[0].ID != "pg1" {
		t.Fatalf("restored instance = %+v, want id pg1", insts)
	}

	dbs, err := dst.ListDatabases(ctx, "pg1")
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}

	if len(dbs) != 1 || dbs[0].Name != "orders" {
		t.Fatalf("restored databases = %+v, want one named orders", dbs)
	}

	users, err := dst.ListUsers(ctx, "pg1")
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	if len(users) != 1 || users[0].Name != "app-user" {
		t.Fatalf("restored users = %+v, want one named app-user", users)
	}
}

// TestSnapshotEmptyCloudSQL confirms a fresh mock snapshots and restores cleanly.
func TestSnapshotEmptyCloudSQL(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}
