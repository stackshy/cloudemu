package cloudsql

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
		config.WithRegion("us-central1"),
		config.WithProjectID("mock-project"),
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
			name: "MySQL default",
			cfg: rdsdriver.InstanceConfig{
				ID:     "orders",
				Engine: "MYSQL_8_0",
			},
		},
		{
			name: "Postgres explicit tier and storage",
			cfg: rdsdriver.InstanceConfig{
				ID:               "analytics",
				Engine:           "POSTGRES_15",
				InstanceClass:    "db-custom-2-8192",
				AllocatedStorage: 200,
				StorageType:      "PD_HDD",
			},
		},
		{
			name:      "missing identifier",
			cfg:       rdsdriver.InstanceConfig{Engine: "MYSQL_8_0"},
			expectErr: true,
		},
		{
			name:      "missing engine",
			cfg:       rdsdriver.InstanceConfig{ID: "x"},
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

func TestPortDefaults(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	mysql, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "m", Engine: "MYSQL_8_0"})
	requireNoError(t, err)
	assertEqual(t, 3306, mysql.Port)

	pg, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "p", Engine: "POSTGRES_15"})
	requireNoError(t, err)
	assertEqual(t, 5432, pg.Port)

	ms, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "s", Engine: "SQLSERVER_2019_STANDARD"})
	requireNoError(t, err)
	assertEqual(t, 1433, ms.Port)
}

func TestInstanceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "MYSQL_8_0"})
	requireNoError(t, err)

	requireNoError(t, m.StopInstance(ctx, "db1"))
	insts, err := m.DescribeInstances(ctx, []string{"db1"})
	requireNoError(t, err)
	assertEqual(t, "stopped", insts[0].State)

	// Idempotent stop.
	requireNoError(t, m.StopInstance(ctx, "db1"))

	requireNoError(t, m.StartInstance(ctx, "db1"))
	requireNoError(t, m.RebootInstance(ctx, "db1"))

	requireNoError(t, m.DeleteInstance(ctx, "db1"))

	if _, err := m.DescribeInstances(ctx, []string{"db1"}); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestModifyInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "MYSQL_8_0"})
	requireNoError(t, err)

	updated, err := m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "db-custom-4-16384",
		AllocatedStorage: 500,
		Tags:             map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	assertEqual(t, "db-custom-4-16384", updated.InstanceClass)
	assertEqual(t, 500, updated.AllocatedStorage)
	assertEqual(t, "prod", updated.Tags["env"])
}

func TestSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "src",
		Engine:           "POSTGRES_15",
		AllocatedStorage: 100,
	})
	requireNoError(t, err)

	snap, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap1", InstanceID: "src"})
	requireNoError(t, err)

	assertEqual(t, "available", snap.State)
	assertEqual(t, 100, snap.AllocatedStorage)

	restored, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "restored",
		SnapshotID:    "snap1",
	})
	requireNoError(t, err)
	assertEqual(t, "restored", restored.ID)
	assertEqual(t, 100, restored.AllocatedStorage)
	assertEqual(t, "POSTGRES_15", restored.Engine)
	assertEqual(t, 5432, restored.Port)

	requireNoError(t, m.DeleteSnapshot(ctx, "snap1"))
}

func TestClusterOpsUnsupported(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c", Engine: "x"}); err == nil {
		t.Fatal("CreateCluster should be unsupported on Cloud SQL")
	}

	clusters, err := m.DescribeClusters(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 0, len(clusters))

	if err := m.StartCluster(ctx, "c"); err == nil {
		t.Fatal("StartCluster should be unsupported on Cloud SQL")
	}

	if _, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "s", ClusterID: "c"}); err == nil {
		t.Fatal("CreateClusterSnapshot should be unsupported on Cloud SQL")
	}

	csnaps, err := m.DescribeClusterSnapshots(ctx, nil, "")
	requireNoError(t, err)
	assertEqual(t, 0, len(csnaps))
}

// Hand-rolled helpers per CLAUDE.md (provider tests don't use testify).

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error, got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}
