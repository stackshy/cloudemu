package redshift

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
	)

	return New(opts)
}

func TestCreateCluster(t *testing.T) {
	tests := []struct {
		name      string
		cfg       rdbdriver.ClusterConfig
		expectErr bool
	}{
		{
			name: "success_default_engine",
			cfg: rdbdriver.ClusterConfig{
				ID:             "warehouse",
				MasterUsername: "admin",
				DatabaseName:   "dev",
			},
		},
		{
			name: "success_explicit_engine",
			cfg: rdbdriver.ClusterConfig{
				ID:     "warehouse2",
				Engine: "redshift",
			},
		},
		{
			name:      "missing_identifier",
			cfg:       rdbdriver.ClusterConfig{},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			cluster, err := m.CreateCluster(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.cfg.ID, cluster.ID)
			assertEqual(t, "available", cluster.State)
			assertEqual(t, 5439, cluster.Port)
			assertEqual(t, "redshift", cluster.Engine)
			assertNotEmpty(t, cluster.ARN)
			assertNotEmpty(t, cluster.Endpoint)
		})
	}
}

func TestCreateCluster_DuplicateRejected(t *testing.T) {
	m := newTestMock()
	cfg := rdbdriver.ClusterConfig{ID: "warehouse"}

	_, err := m.CreateCluster(context.Background(), cfg)
	requireNoError(t, err)

	if _, err := m.CreateCluster(context.Background(), cfg); err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "warehouse"})
	requireNoError(t, err)

	requireNoError(t, m.StopCluster(ctx, "warehouse"))

	clusters, err := m.DescribeClusters(ctx, []string{"warehouse"})
	requireNoError(t, err)
	assertEqual(t, "stopped", clusters[0].State)

	// Idempotent stop on already-stopped.
	requireNoError(t, m.StopCluster(ctx, "warehouse"))

	requireNoError(t, m.StartCluster(ctx, "warehouse"))

	clusters, err = m.DescribeClusters(ctx, []string{"warehouse"})
	requireNoError(t, err)
	assertEqual(t, "available", clusters[0].State)

	requireNoError(t, m.RebootCluster(ctx, "warehouse"))

	requireNoError(t, m.DeleteCluster(ctx, "warehouse"))

	if _, err := m.DescribeClusters(ctx, []string{"warehouse"}); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestRebootInstance_DelegatesToCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "warehouse"})
	requireNoError(t, err)

	requireNoError(t, m.RebootInstance(ctx, "warehouse"))
}

func TestModifyCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "warehouse"})
	requireNoError(t, err)

	updated, err := m.ModifyCluster(ctx, "warehouse", rdbdriver.ModifyInstanceInput{
		EngineVersion: "1.0.32",
		Tags:          map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	assertEqual(t, "1.0.32", updated.EngineVersion)
	assertEqual(t, "prod", updated.Tags["env"])
}

// TestModifyCluster_FieldLevelMerge proves ModifyCluster is a field-level
// merge: a first call sets NodeType/security groups/parameter group/booleans/
// maintenance track/elastic IP, and a second call that touches only
// NumberOfNodes must not revert any of them.
func TestModifyCluster_FieldLevelMerge(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "warehouse"})
	requireNoError(t, err)

	allowUpgrade := false
	publiclyAccessible := true
	encrypted := true

	first, err := m.ModifyCluster(ctx, "warehouse", rdbdriver.ModifyInstanceInput{
		NodeType:                    "ra3.xlplus",
		VPCSecurityGroups:           []string{"sg-1", "sg-2"},
		ClusterSecurityGroups:       []string{"classic-sg"},
		DBClusterParameterGroupName: "custom-pg",
		AllowVersionUpgrade:         &allowUpgrade,
		PubliclyAccessible:          &publiclyAccessible,
		Encrypted:                   &encrypted,
		MaintenanceTrackName:        "trailing",
		ElasticIP:                   "192.0.2.10",
	})
	requireNoError(t, err)

	assertEqual(t, "ra3.xlplus", first.NodeType)
	assertEqual(t, "custom-pg", first.DBClusterParameterGroupName)
	assertEqual(t, false, first.AllowVersionUpgrade)
	assertEqual(t, true, first.PubliclyAccessible)
	assertEqual(t, true, first.Encrypted)
	assertEqual(t, "trailing", first.MaintenanceTrackName)
	assertEqual(t, "192.0.2.10", first.ElasticIP)

	if len(first.VPCSecurityGroups) != 2 || len(first.ClusterSecurityGroups) != 1 {
		t.Fatalf("security groups not applied: vpc=%v classic=%v", first.VPCSecurityGroups, first.ClusterSecurityGroups)
	}

	// Second call touches only NumberOfNodes; every field set above must survive.
	second, err := m.ModifyCluster(ctx, "warehouse", rdbdriver.ModifyInstanceInput{
		ClusterType:   "multi-node",
		NumberOfNodes: 4,
	})
	requireNoError(t, err)

	assertEqual(t, 4, second.NumberOfNodes)
	assertEqual(t, "ra3.xlplus", second.NodeType)
	assertEqual(t, "custom-pg", second.DBClusterParameterGroupName)
	assertEqual(t, false, second.AllowVersionUpgrade)
	assertEqual(t, true, second.PubliclyAccessible)
	assertEqual(t, true, second.Encrypted)
	assertEqual(t, "trailing", second.MaintenanceTrackName)
	assertEqual(t, "192.0.2.10", second.ElasticIP)

	if len(second.VPCSecurityGroups) != 2 || len(second.ClusterSecurityGroups) != 1 {
		t.Fatalf("second modify dropped security groups: vpc=%v classic=%v",
			second.VPCSecurityGroups, second.ClusterSecurityGroups)
	}

	// DescribeClusters must reflect the same preserved state.
	desc, err := m.DescribeClusters(ctx, []string{"warehouse"})
	requireNoError(t, err)
	assertEqual(t, "ra3.xlplus", desc[0].NodeType)
	assertEqual(t, 4, desc[0].NumberOfNodes)
	assertEqual(t, "192.0.2.10", desc[0].ElasticIP)
}

// TestModifyCluster_MasterUserPasswordDoesNotError proves a MasterUserPassword
// change on ModifyCluster is accepted (and, with no real database engine wired,
// is safely a no-op) rather than causing an error or being silently mishandled.
func TestModifyCluster_MasterUserPasswordDoesNotError(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "warehouse", MasterUsername: "admin"})
	requireNoError(t, err)

	updated, err := m.ModifyCluster(ctx, "warehouse", rdbdriver.ModifyInstanceInput{
		MasterUserPassword: "NewPassw0rd!",
	})
	requireNoError(t, err)
	assertEqual(t, "warehouse", updated.ID)
}

func TestClusterSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "src-warehouse"})
	requireNoError(t, err)

	snap, err := m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{
		ID:        "snap-1",
		ClusterID: "src-warehouse",
	})
	requireNoError(t, err)

	assertEqual(t, "available", snap.State)
	assertEqual(t, "redshift", snap.Engine)
	assertNotEmpty(t, snap.ARN)

	// Describe filtered by cluster id.
	snaps, err := m.DescribeClusterSnapshots(ctx, nil, "src-warehouse")
	requireNoError(t, err)
	assertEqual(t, 1, len(snaps))

	// Restore into a new cluster.
	restored, err := m.RestoreClusterFromSnapshot(ctx, rdbdriver.RestoreClusterInput{
		NewClusterID: "restored-warehouse",
		SnapshotID:   "snap-1",
	})
	requireNoError(t, err)

	assertEqual(t, "restored-warehouse", restored.ID)
	assertEqual(t, "redshift", restored.Engine)
	assertEqual(t, "available", restored.State)

	requireNoError(t, m.DeleteClusterSnapshot(ctx, "snap-1"))
}

func TestClusterSnapshotPreservesKmsKeyID(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{
		ID:        "enc-warehouse",
		Encrypted: true,
		KmsKeyID:  "arn:aws:kms:us-east-1:123456789012:key/abc-123",
	})
	requireNoError(t, err)

	snap, err := m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{
		ID:        "enc-snap",
		ClusterID: "enc-warehouse",
	})
	requireNoError(t, err)

	// CreateClusterSnapshot captures the source cluster's encryption key.
	assertEqual(t, true, snap.Encrypted)
	assertEqual(t, "arn:aws:kms:us-east-1:123456789012:key/abc-123", snap.KmsKeyID)

	// DescribeClusterSnapshots reflects it.
	snaps, err := m.DescribeClusterSnapshots(ctx, []string{"enc-snap"}, "")
	requireNoError(t, err)
	assertEqual(t, 1, len(snaps))
	assertEqual(t, true, snaps[0].Encrypted)
	assertEqual(t, "arn:aws:kms:us-east-1:123456789012:key/abc-123", snaps[0].KmsKeyID)

	// Restore inherits the snapshot's key.
	restored, err := m.RestoreClusterFromSnapshot(ctx, rdbdriver.RestoreClusterInput{
		NewClusterID: "restored-enc",
		SnapshotID:   "enc-snap",
	})
	requireNoError(t, err)
	assertEqual(t, true, restored.Encrypted)
	assertEqual(t, "arn:aws:kms:us-east-1:123456789012:key/abc-123", restored.KmsKeyID)

	// DescribeClusters reflects the preserved key on the restored cluster.
	clusters, err := m.DescribeClusters(ctx, []string{"restored-enc"})
	requireNoError(t, err)
	assertEqual(t, 1, len(clusters))
	assertEqual(t, "arn:aws:kms:us-east-1:123456789012:key/abc-123", clusters[0].KmsKeyID)
}

func TestRestoreClusterKmsKeyIDOverride(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{
		ID:        "enc-warehouse",
		Encrypted: true,
		KmsKeyID:  "key/original",
	})
	requireNoError(t, err)

	_, err = m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{
		ID:        "enc-snap",
		ClusterID: "enc-warehouse",
	})
	requireNoError(t, err)

	restored, err := m.RestoreClusterFromSnapshot(ctx, rdbdriver.RestoreClusterInput{
		NewClusterID: "restored-override",
		SnapshotID:   "enc-snap",
		KmsKeyID:     "key/override",
	})
	requireNoError(t, err)
	assertEqual(t, true, restored.Encrypted)
	assertEqual(t, "key/override", restored.KmsKeyID)
}

func TestEncryptedClusterWithoutKeyGetsDefault(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cluster, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{
		ID:        "enc-default",
		Encrypted: true,
	})
	requireNoError(t, err)
	assertEqual(t, "alias/aws/redshift", cluster.KmsKeyID)

	snap, err := m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{
		ID:        "enc-default-snap",
		ClusterID: "enc-default",
	})
	requireNoError(t, err)
	assertEqual(t, "alias/aws/redshift", snap.KmsKeyID)
}

func TestUnencryptedClusterSnapshotHasNoKmsKey(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cluster, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "plain-warehouse"})
	requireNoError(t, err)
	assertEqual(t, false, cluster.Encrypted)
	assertEqual(t, "", cluster.KmsKeyID)

	snap, err := m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{
		ID:        "plain-snap",
		ClusterID: "plain-warehouse",
	})
	requireNoError(t, err)
	assertEqual(t, false, snap.Encrypted)
	assertEqual(t, "", snap.KmsKeyID)

	restored, err := m.RestoreClusterFromSnapshot(ctx, rdbdriver.RestoreClusterInput{
		NewClusterID: "restored-plain",
		SnapshotID:   "plain-snap",
	})
	requireNoError(t, err)
	assertEqual(t, false, restored.Encrypted)
	assertEqual(t, "", restored.KmsKeyID)
}

func TestInstanceOpsRejected(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdbdriver.InstanceConfig{ID: "x"}); err == nil {
		t.Fatal("expected CreateInstance to be rejected")
	}

	if _, err := m.DescribeInstances(ctx, []string{"x"}); err == nil {
		t.Fatal("expected DescribeInstances to be rejected")
	}

	if err := m.DeleteInstance(ctx, "x"); err == nil {
		t.Fatal("expected DeleteInstance to be rejected")
	}

	if err := m.StartInstance(ctx, "x"); err == nil {
		t.Fatal("expected StartInstance to be rejected")
	}

	if err := m.StopInstance(ctx, "x"); err == nil {
		t.Fatal("expected StopInstance to be rejected")
	}

	if _, err := m.ModifyInstance(ctx, "x", rdbdriver.ModifyInstanceInput{}); err == nil {
		t.Fatal("expected ModifyInstance to be rejected")
	}

	if _, err := m.CreateSnapshot(ctx, rdbdriver.SnapshotConfig{ID: "s"}); err == nil {
		t.Fatal("expected CreateSnapshot to be rejected")
	}

	if _, err := m.DescribeSnapshots(ctx, nil, ""); err == nil {
		t.Fatal("expected DescribeSnapshots to be rejected")
	}

	if err := m.DeleteSnapshot(ctx, "s"); err == nil {
		t.Fatal("expected DeleteSnapshot to be rejected")
	}

	if _, err := m.RestoreInstanceFromSnapshot(ctx, rdbdriver.RestoreInstanceInput{}); err == nil {
		t.Fatal("expected RestoreInstanceFromSnapshot to be rejected")
	}
}

// TestDeleteClusterSubnetGroupInUseGuard proves a subnet group referenced by a
// live cluster cannot be deleted, and that deleting the cluster releases it.
func TestDeleteClusterSubnetGroupInUseGuard(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateClusterSubnetGroup(ctx, "sng", "desc", []string{"subnet-1"}); err != nil {
		t.Fatalf("create subnet group: %v", err)
	}

	if _, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{
		ID: "cl1", MasterUsername: "admin", SubnetGroupName: "sng",
	}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	// In use → rejected; the group survives.
	if err := m.DeleteClusterSubnetGroup(ctx, "sng"); err == nil {
		t.Fatal("expected in-use subnet group delete to be rejected")
	}

	if sngs, err := m.DescribeClusterSubnetGroups(ctx, []string{"sng"}); err != nil || len(sngs) != 1 {
		t.Fatalf("subnet group should still exist: %+v, err %v", sngs, err)
	}

	if err := m.DeleteCluster(ctx, "cl1"); err != nil {
		t.Fatalf("delete cluster: %v", err)
	}

	// Released → delete succeeds.
	if err := m.DeleteClusterSubnetGroup(ctx, "sng"); err != nil {
		t.Fatalf("delete released subnet group: %v", err)
	}
}

// TestDeleteClusterParameterGroupInUseGuard proves a parameter group referenced
// by a live cluster cannot be deleted, and that deleting the cluster frees it.
func TestDeleteClusterParameterGroupInUseGuard(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateClusterParameterGroup(ctx, "pg", "redshift-1.0", "desc"); err != nil {
		t.Fatalf("create parameter group: %v", err)
	}

	if _, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{
		ID: "cl1", MasterUsername: "admin", DBClusterParameterGroupName: "pg",
	}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if err := m.DeleteClusterParameterGroup(ctx, "pg"); err == nil {
		t.Fatal("expected in-use parameter group delete to be rejected")
	}

	if pgs, err := m.DescribeClusterParameterGroups(ctx, []string{"pg"}); err != nil || len(pgs) != 1 {
		t.Fatalf("parameter group should still exist: %+v, err %v", pgs, err)
	}

	if err := m.DeleteCluster(ctx, "cl1"); err != nil {
		t.Fatalf("delete cluster: %v", err)
	}

	if err := m.DeleteClusterParameterGroup(ctx, "pg"); err != nil {
		t.Fatalf("delete released parameter group: %v", err)
	}
}

// TestDeleteUnreferencedGroupsSucceed proves the in-use guard never blocks a
// group nothing references.
func TestDeleteUnreferencedGroupsSucceed(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateClusterSubnetGroup(ctx, "sng", "desc", []string{"subnet-1"}); err != nil {
		t.Fatalf("create subnet group: %v", err)
	}

	if _, err := m.CreateClusterParameterGroup(ctx, "pg", "redshift-1.0", "desc"); err != nil {
		t.Fatalf("create parameter group: %v", err)
	}

	if err := m.DeleteClusterSubnetGroup(ctx, "sng"); err != nil {
		t.Fatalf("delete unreferenced subnet group: %v", err)
	}

	if err := m.DeleteClusterParameterGroup(ctx, "pg"); err != nil {
		t.Fatalf("delete unreferenced parameter group: %v", err)
	}
}

// requireNoError fails the test immediately if err is non-nil.
func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertError asserts that err matches the expectErr expectation.
func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error, got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertEqual asserts that expected and actual are equal.
func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

// assertNotEmpty asserts that s is non-empty.
func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}
