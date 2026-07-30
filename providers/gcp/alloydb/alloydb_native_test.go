package alloydb

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestAlloyDBNativeClusterAndInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateAlloyDBCluster(ctx, rdsdriver.AlloyDBClusterConfig{
		ID: "c1", DatabaseVersion: "POSTGRES_15", Network: "default",
		InitialUser: "postgres", ContinuousBackup: true, AutomatedBackupEnabled: true,
		MaintenanceDay: "SUNDAY",
	})
	requireNoError(t, err)
	assertEqual(t, "c1", c.ID)

	// The initial user was created.
	if _, err := m.GetUser(ctx, "c1", "postgres"); err != nil {
		t.Errorf("initial user not created: %v", err)
	}

	info, err := m.AlloyDBClusterInfo(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, clusterTypePrimary, info.ClusterType)
	assertEqual(t, true, info.ContinuousBackup)
	assertEqual(t, "SUNDAY", info.MaintenanceDay)

	// PRIMARY instance + a READ_POOL instance.
	if _, err := m.CreateAlloyDBInstance(ctx, rdsdriver.AlloyDBInstanceConfig{
		ClusterID: "c1", ID: "primary", InstanceType: instanceTypePrimary, CPUCount: 4,
	}); err != nil {
		t.Fatalf("CreateAlloyDBInstance primary: %v", err)
	}

	if _, err := m.CreateAlloyDBInstance(ctx, rdsdriver.AlloyDBInstanceConfig{
		ClusterID: "c1", ID: "pool", InstanceType: instanceTypeReadPool, NodeCount: 3, CPUCount: 2,
	}); err != nil {
		t.Fatalf("CreateAlloyDBInstance read pool: %v", err)
	}

	iInfo, err := m.AlloyDBInstanceInfo(ctx, "c1", "pool")
	requireNoError(t, err)
	assertEqual(t, instanceTypeReadPool, iInfo.InstanceType)
	assertEqual(t, 3, iInfo.NodeCount)

	// Invalid instance type rejected.
	if _, err := m.CreateAlloyDBInstance(ctx, rdsdriver.AlloyDBInstanceConfig{
		ClusterID: "c1", ID: "bad", InstanceType: "WEIRD",
	}); err == nil {
		t.Error("invalid instance type: expected InvalidArgument")
	}

	// Failover + restart actions work on an existing instance.
	if _, err := m.FailoverInstance(ctx, "c1", "primary"); err != nil {
		t.Errorf("FailoverInstance: %v", err)
	}

	if _, err := m.RestartInstance(ctx, "c1", "pool"); err != nil {
		t.Errorf("RestartInstance: %v", err)
	}

	if _, err := m.FailoverInstance(ctx, "c1", "ghost"); err == nil {
		t.Error("FailoverInstance on missing instance: expected NotFound")
	}
}

func TestAlloyDBSecondaryAndPromote(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateAlloyDBCluster(ctx, rdsdriver.AlloyDBClusterConfig{ID: "primary"}); err != nil {
		t.Fatalf("CreateAlloyDBCluster: %v", err)
	}

	sec, err := m.CreateSecondaryCluster(ctx, rdsdriver.SecondaryClusterConfig{ID: "secondary", PrimaryCluster: "primary"})
	requireNoError(t, err)
	assertEqual(t, "secondary", sec.ID)

	info, err := m.AlloyDBClusterInfo(ctx, "secondary")
	requireNoError(t, err)
	assertEqual(t, clusterTypeSecondary, info.ClusterType)
	assertEqual(t, "primary", info.PrimaryCluster)

	// Secondary from a non-existent primary is rejected.
	if _, err := m.CreateSecondaryCluster(ctx, rdsdriver.SecondaryClusterConfig{ID: "x", PrimaryCluster: "ghost"}); err == nil {
		t.Error("secondary from missing primary: expected NotFound")
	}

	// Secondary from a secondary is rejected.
	if _, err := m.CreateSecondaryCluster(ctx, rdsdriver.SecondaryClusterConfig{ID: "y", PrimaryCluster: "secondary"}); err == nil {
		t.Error("secondary from a secondary: expected FailedPrecondition")
	}

	// Promote the secondary → PRIMARY.
	if _, err := m.PromoteCluster(ctx, "secondary"); err != nil {
		t.Fatalf("PromoteCluster: %v", err)
	}

	after, err := m.AlloyDBClusterInfo(ctx, "secondary")
	requireNoError(t, err)
	assertEqual(t, clusterTypePrimary, after.ClusterType)
	assertEqual(t, "", after.PrimaryCluster)

	// Promoting a PRIMARY is rejected.
	if _, err := m.PromoteCluster(ctx, "primary"); err == nil {
		t.Error("promote a PRIMARY: expected FailedPrecondition")
	}
}
