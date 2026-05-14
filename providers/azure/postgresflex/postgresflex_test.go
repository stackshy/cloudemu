package postgresflex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("eastus"),
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
			name: "default Postgres",
			cfg: rdsdriver.InstanceConfig{
				ID: "srv1",
			},
		},
		{
			name: "explicit SKU and storage",
			cfg: rdsdriver.InstanceConfig{
				ID:               "srv2",
				Engine:           "Postgres",
				EngineVersion:    "15",
				InstanceClass:    "Standard_D2s_v3",
				AllocatedStorage: 128,
			},
		},
		{
			name:      "missing name",
			cfg:       rdsdriver.InstanceConfig{},
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
			assertEqual(t, 5432, inst.Port)
			assertEqual(t, "Postgres", inst.Engine)
			assertNotEmpty(t, inst.ARN)

			if !strings.HasSuffix(inst.Endpoint, ".postgres.database.azure.com") {
				t.Errorf("expected endpoint to end with .postgres.database.azure.com, got %q", inst.Endpoint)
			}
		})
	}
}

func TestDuplicateCreate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "dup"})
	requireNoError(t, err)

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "dup"}); err == nil {
		t.Fatal("expected AlreadyExists on duplicate create")
	}
}

func TestInstanceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv"})
	requireNoError(t, err)

	requireNoError(t, m.StopInstance(ctx, "srv"))
	insts, err := m.DescribeInstances(ctx, []string{"srv"})
	requireNoError(t, err)
	assertEqual(t, "stopped", insts[0].State)

	// Idempotent stop.
	requireNoError(t, m.StopInstance(ctx, "srv"))

	// Cannot reboot when stopped.
	if err := m.RebootInstance(ctx, "srv"); err == nil {
		t.Fatal("expected restart on stopped server to fail")
	}

	requireNoError(t, m.StartInstance(ctx, "srv"))
	requireNoError(t, m.RebootInstance(ctx, "srv"))

	requireNoError(t, m.DeleteInstance(ctx, "srv"))

	if _, err := m.DescribeInstances(ctx, []string{"srv"}); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestModifyInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv"})
	requireNoError(t, err)

	updated, err := m.ModifyInstance(ctx, "srv", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "Standard_D4s_v3",
		AllocatedStorage: 256,
		EngineVersion:    "15",
		Tags:             map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	assertEqual(t, "Standard_D4s_v3", updated.InstanceClass)
	assertEqual(t, 256, updated.AllocatedStorage)
	assertEqual(t, "15", updated.EngineVersion)
	assertEqual(t, "prod", updated.Tags["env"])
}

func TestDescribeAll(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "a"})
	requireNoError(t, err)

	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "b"})
	requireNoError(t, err)

	all, err := m.DescribeInstances(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 2, len(all))
}

func TestSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "src",
		EngineVersion:    "15",
		AllocatedStorage: 100,
	})
	requireNoError(t, err)

	snap, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap1", InstanceID: "src"})
	requireNoError(t, err)

	assertEqual(t, "available", snap.State)
	assertEqual(t, 100, snap.AllocatedStorage)
	assertEqual(t, "Postgres", snap.Engine)

	snaps, err := m.DescribeSnapshots(ctx, nil, "src")
	requireNoError(t, err)
	assertEqual(t, 1, len(snaps))

	restored, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "restored",
		SnapshotID:    "snap1",
	})
	requireNoError(t, err)
	assertEqual(t, "restored", restored.ID)
	assertEqual(t, 100, restored.AllocatedStorage)
	assertEqual(t, 5432, restored.Port)

	// Snapshot of unknown server fails.
	if _, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "x", InstanceID: "missing"}); err == nil {
		t.Fatal("expected snapshot of missing server to fail")
	}

	requireNoError(t, m.DeleteSnapshot(ctx, "snap1"))
}

func TestClusterOpsUnsupported(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c", Engine: "x"}); err == nil {
		t.Fatal("CreateCluster should be unsupported on Postgres Flex")
	}

	clusters, err := m.DescribeClusters(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 0, len(clusters))

	if _, err := m.ModifyCluster(ctx, "c", rdsdriver.ModifyInstanceInput{}); err == nil {
		t.Fatal("ModifyCluster should be unsupported on Postgres Flex")
	}

	if err := m.DeleteCluster(ctx, "c"); err == nil {
		t.Fatal("DeleteCluster should be unsupported on Postgres Flex")
	}

	if err := m.StartCluster(ctx, "c"); err == nil {
		t.Fatal("StartCluster should be unsupported on Postgres Flex")
	}

	if err := m.StopCluster(ctx, "c"); err == nil {
		t.Fatal("StopCluster should be unsupported on Postgres Flex")
	}

	if _, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "s", ClusterID: "c"}); err == nil {
		t.Fatal("CreateClusterSnapshot should be unsupported on Postgres Flex")
	}

	csnaps, err := m.DescribeClusterSnapshots(ctx, nil, "")
	requireNoError(t, err)
	assertEqual(t, 0, len(csnaps))

	if err := m.DeleteClusterSnapshot(ctx, "s"); err == nil {
		t.Fatal("DeleteClusterSnapshot should be unsupported on Postgres Flex")
	}

	if _, err := m.RestoreClusterFromSnapshot(ctx, rdsdriver.RestoreClusterInput{}); err == nil {
		t.Fatal("RestoreClusterFromSnapshot should be unsupported on Postgres Flex")
	}
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
