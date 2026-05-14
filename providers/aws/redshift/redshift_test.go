package redshift

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	rdbdriver "github.com/stackshy/cloudemu/relationaldb/driver"
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
