package mysqlflex

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
		config.WithAccountID("sub-1"),
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
			name: "default tier and storage",
			cfg:  rdsdriver.InstanceConfig{ID: "orders"},
		},
		{
			name: "explicit sku and storage",
			cfg: rdsdriver.InstanceConfig{
				ID:               "analytics",
				InstanceClass:    "Standard_D2ds_v4",
				AllocatedStorage: 200,
				StorageType:      "Premium_LRS",
				EngineVersion:    "8.0.21",
			},
		},
		{
			name:      "missing identifier",
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
			assertEqual(t, defaultEngine, inst.Engine)
			assertEqual(t, defaultPort, inst.Port)
			assertNotEmpty(t, inst.ARN)

			if !strings.HasSuffix(inst.Endpoint, endpointSuffix) {
				t.Errorf("expected endpoint suffix %q, got %q", endpointSuffix, inst.Endpoint)
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

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1"})
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

func TestRebootRequiresAvailable(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1"})
	requireNoError(t, err)

	requireNoError(t, m.StopInstance(ctx, "db1"))

	if err := m.RebootInstance(ctx, "db1"); err == nil {
		t.Fatal("expected reboot to fail on stopped instance")
	}
}

func TestModifyInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1"})
	requireNoError(t, err)

	updated, err := m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "Standard_D4ds_v4",
		AllocatedStorage: 500,
		EngineVersion:    "8.0.21",
		Tags:             map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	assertEqual(t, "Standard_D4ds_v4", updated.InstanceClass)
	assertEqual(t, 500, updated.AllocatedStorage)
	assertEqual(t, "8.0.21", updated.EngineVersion)
	assertEqual(t, "prod", updated.Tags["env"])
}

func TestSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "src",
		AllocatedStorage: 100,
		EngineVersion:    "8.0.21",
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
	assertEqual(t, defaultPort, restored.Port)

	requireNoError(t, m.DeleteSnapshot(ctx, "snap1"))
}

func TestClusterOpsUnsupported(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c"}); err == nil {
		t.Fatal("CreateCluster should be unsupported on MySQL Flex")
	}

	clusters, err := m.DescribeClusters(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 0, len(clusters))

	if err := m.StartCluster(ctx, "c"); err == nil {
		t.Fatal("StartCluster should be unsupported on MySQL Flex")
	}

	if err := m.StopCluster(ctx, "c"); err == nil {
		t.Fatal("StopCluster should be unsupported on MySQL Flex")
	}

	if err := m.DeleteCluster(ctx, "c"); err == nil {
		t.Fatal("DeleteCluster should be unsupported on MySQL Flex")
	}

	if _, err := m.ModifyCluster(ctx, "c", rdsdriver.ModifyInstanceInput{}); err == nil {
		t.Fatal("ModifyCluster should be unsupported on MySQL Flex")
	}

	if _, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "s", ClusterID: "c"}); err == nil {
		t.Fatal("CreateClusterSnapshot should be unsupported on MySQL Flex")
	}

	csnaps, err := m.DescribeClusterSnapshots(ctx, nil, "")
	requireNoError(t, err)
	assertEqual(t, 0, len(csnaps))

	if err := m.DeleteClusterSnapshot(ctx, "s"); err == nil {
		t.Fatal("DeleteClusterSnapshot should be unsupported on MySQL Flex")
	}

	if _, err := m.RestoreClusterFromSnapshot(ctx, rdsdriver.RestoreClusterInput{}); err == nil {
		t.Fatal("RestoreClusterFromSnapshot should be unsupported on MySQL Flex")
	}
}

func TestDescribeInstancesUnknown(t *testing.T) {
	m := newTestMock()

	if _, err := m.DescribeInstances(context.Background(), []string{"nope"}); err == nil {
		t.Fatal("expected NotFound on unknown id")
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
