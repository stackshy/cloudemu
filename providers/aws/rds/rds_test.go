package rds

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
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

func TestCreateInstance(t *testing.T) {
	tests := []struct {
		name      string
		cfg       rdsdriver.InstanceConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg: rdsdriver.InstanceConfig{
				ID:             "db1",
				Engine:         "mysql",
				MasterUsername: "admin",
			},
		},
		{
			name:      "missing identifier",
			cfg:       rdsdriver.InstanceConfig{Engine: "mysql"},
			expectErr: true,
		},
		{
			name:      "missing engine",
			cfg:       rdsdriver.InstanceConfig{ID: "db1"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			inst, err := m.CreateInstance(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.cfg.ID, inst.ID)
			assertEqual(t, "available", inst.State)
			assertNotEmpty(t, inst.ARN)
			assertNotEmpty(t, inst.Endpoint)

			if inst.Port == 0 {
				t.Errorf("expected default port to be set")
			}
		})
	}
}

func TestCreateInstance_DuplicateRejected(t *testing.T) {
	m := newTestMock()
	cfg := rdsdriver.InstanceConfig{ID: "db1", Engine: "mysql"}

	_, err := m.CreateInstance(context.Background(), cfg)
	requireNoError(t, err)

	_, err = m.CreateInstance(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
}

func TestInstanceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := rdsdriver.InstanceConfig{ID: "db1", Engine: "postgres"}
	_, err := m.CreateInstance(ctx, cfg)
	requireNoError(t, err)

	requireNoError(t, m.StopInstance(ctx, "db1"))

	insts, err := m.DescribeInstances(ctx, []string{"db1"})
	requireNoError(t, err)
	assertEqual(t, "stopped", insts[0].State)

	// Idempotent stop on already-stopped.
	requireNoError(t, m.StopInstance(ctx, "db1"))

	requireNoError(t, m.StartInstance(ctx, "db1"))

	insts, err = m.DescribeInstances(ctx, []string{"db1"})
	requireNoError(t, err)
	assertEqual(t, "available", insts[0].State)

	requireNoError(t, m.RebootInstance(ctx, "db1"))

	requireNoError(t, m.DeleteInstance(ctx, "db1"))

	if _, err := m.DescribeInstances(ctx, []string{"db1"}); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestModifyInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:     "db1",
		Engine: "mysql",
	})
	requireNoError(t, err)

	multi := true
	updated, err := m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "db.t3.large",
		AllocatedStorage: 100,
		EngineVersion:    "8.0.32",
		MultiAZ:          &multi,
		Tags:             map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	assertEqual(t, "db.t3.large", updated.InstanceClass)
	assertEqual(t, 100, updated.AllocatedStorage)
	assertEqual(t, "8.0.32", updated.EngineVersion)
	assertTrue(t, updated.MultiAZ, "MultiAZ flag should be set")
	assertEqual(t, "prod", updated.Tags["env"])
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cluster, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID:             "cluster1",
		Engine:         "aurora-postgresql",
		MasterUsername: "admin",
		DatabaseName:   "appdb",
	})
	requireNoError(t, err)

	assertEqual(t, "available", cluster.State)
	assertNotEmpty(t, cluster.Endpoint)
	assertNotEmpty(t, cluster.ReaderEndpoint)
	assertEqual(t, 5432, cluster.Port)

	// Add a member instance.
	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:        "cluster1-instance-1",
		Engine:    "aurora-postgresql",
		ClusterID: "cluster1",
	})
	requireNoError(t, err)

	clusters, err := m.DescribeClusters(ctx, []string{"cluster1"})
	requireNoError(t, err)
	assertEqual(t, 1, len(clusters[0].Members))

	// Cannot delete cluster with members.
	if err := m.DeleteCluster(ctx, "cluster1"); err == nil {
		t.Fatal("expected delete-with-members error")
	}

	// Stop / start.
	requireNoError(t, m.StopCluster(ctx, "cluster1"))
	requireNoError(t, m.StartCluster(ctx, "cluster1"))

	// Delete instance, then cluster.
	requireNoError(t, m.DeleteInstance(ctx, "cluster1-instance-1"))
	requireNoError(t, m.DeleteCluster(ctx, "cluster1"))
}

func TestSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "src",
		Engine:           "mysql",
		AllocatedStorage: 50,
	})
	requireNoError(t, err)

	snap, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{
		ID:         "snap-1",
		InstanceID: "src",
	})
	requireNoError(t, err)

	assertEqual(t, "available", snap.State)
	assertEqual(t, 50, snap.AllocatedStorage)

	// Restore into a new instance.
	restored, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "restored",
		SnapshotID:    "snap-1",
	})
	requireNoError(t, err)

	assertEqual(t, "restored", restored.ID)
	assertEqual(t, 50, restored.AllocatedStorage)
	assertEqual(t, "mysql", restored.Engine)

	// Delete snapshot.
	requireNoError(t, m.DeleteSnapshot(ctx, "snap-1"))
}

func TestClusterSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID:     "src-cluster",
		Engine: "aurora-mysql",
	})
	requireNoError(t, err)

	snap, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{
		ID:        "csnap-1",
		ClusterID: "src-cluster",
	})
	requireNoError(t, err)

	assertEqual(t, "available", snap.State)

	restored, err := m.RestoreClusterFromSnapshot(ctx, rdsdriver.RestoreClusterInput{
		NewClusterID: "restored-cluster",
		SnapshotID:   "csnap-1",
	})
	requireNoError(t, err)

	assertEqual(t, "restored-cluster", restored.ID)
	assertEqual(t, "aurora-mysql", restored.Engine)

	requireNoError(t, m.DeleteClusterSnapshot(ctx, "csnap-1"))
}

func TestDefaultPortFor(t *testing.T) {
	tests := []struct {
		engine string
		want   int
	}{
		{"mysql", 3306},
		{"mariadb", 3306},
		{"aurora-mysql", 3306},
		{"postgres", 5432},
		{"aurora-postgresql", 5432},
		{"neptune", 8182},
		{"docdb", 27017},
	}

	for _, tc := range tests {
		t.Run(tc.engine, func(t *testing.T) {
			if got := defaultPortFor(tc.engine); got != tc.want {
				t.Errorf("defaultPortFor(%q)=%d, want %d", tc.engine, got, tc.want)
			}
		})
	}
}

func TestNamespaceFor(t *testing.T) {
	tests := []struct {
		engine string
		want   string
	}{
		{"mysql", "AWS/RDS"},
		{"postgres", "AWS/RDS"},
		{"aurora-mysql", "AWS/RDS"},
		{"neptune", "AWS/Neptune"},
		{"docdb", "AWS/DocDB"},
	}

	for _, tc := range tests {
		t.Run(tc.engine, func(t *testing.T) {
			if got := namespaceFor(tc.engine); got != tc.want {
				t.Errorf("namespaceFor(%q)=%q, want %q", tc.engine, got, tc.want)
			}
		})
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

// assertTrue asserts val is true.
func assertTrue(t *testing.T, val bool, msg string) {
	t.Helper()

	if !val {
		t.Errorf("expected true: %s", msg)
	}
}
