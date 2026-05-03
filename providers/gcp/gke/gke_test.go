package gke

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
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

func TestCreateCluster(t *testing.T) {
	tests := []struct {
		name      string
		input     CreateClusterInput
		expectErr bool
	}{
		{
			name: "minimal",
			input: CreateClusterInput{
				Name:     "prod",
				Location: "us-central1",
			},
		},
		{
			name: "with explicit node pool",
			input: CreateClusterInput{
				Name:     "with-np",
				Location: "us-central1",
				NodePools: []NodePoolSpec{
					{Name: "primary", InitialNodeCount: 3, MachineType: "e2-standard-4"},
				},
			},
		},
		{
			name:      "missing name",
			input:     CreateClusterInput{Location: "us-central1"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			c, op, err := m.CreateCluster(context.Background(), &tc.input)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.input.Name, c.Name)
			assertEqual(t, "RUNNING", c.Status)
			assertEqual(t, "DONE", op.Status)

			if len(c.NodePoolNames) == 0 {
				t.Fatal("expected at least one node pool")
			}
		})
	}
}

func TestClusterDuplicateRejected(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "p1", Location: "us-central1"})
	requireNoError(t, err)

	if _, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "p1", Location: "us-central1"}); err == nil {
		t.Fatal("expected AlreadyExists error on duplicate cluster")
	}
}

func TestGetListUpdateDeleteCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "alpha", Location: "us-central1"})
	requireNoError(t, err)

	got, err := m.GetCluster(ctx, "us-central1", "alpha")
	requireNoError(t, err)
	assertEqual(t, "alpha", got.Name)

	if _, err := m.GetCluster(ctx, "us-central1", "missing"); err == nil {
		t.Fatal("expected NotFound for missing cluster")
	}

	clusters, err := m.ListClusters(ctx, "-")
	requireNoError(t, err)
	assertEqual(t, 1, len(clusters))

	_, err = m.UpdateCluster(ctx, "us-central1", "alpha", UpdateClusterInput{
		LoggingService: "none",
		ResourceLabels: map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	got, _ = m.GetCluster(ctx, "us-central1", "alpha")
	assertEqual(t, "none", got.LoggingService)
	assertEqual(t, "prod", got.ResourceLabels["env"])

	_, err = m.DeleteCluster(ctx, "us-central1", "alpha")
	requireNoError(t, err)

	if _, err := m.GetCluster(ctx, "us-central1", "alpha"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestClusterSetters(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	loc, name := "us-central1", "tweaks"

	_, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: name, Location: loc})
	requireNoError(t, err)

	if _, err := m.SetClusterLogging(ctx, loc, name, "logging.googleapis.com/kubernetes"); err != nil {
		t.Fatalf("SetClusterLogging: %v", err)
	}

	if _, err := m.SetClusterMonitoring(ctx, loc, name, "monitoring.googleapis.com/kubernetes"); err != nil {
		t.Fatalf("SetClusterMonitoring: %v", err)
	}

	if _, err := m.SetMasterAuth(ctx, loc, name, "admin"); err != nil {
		t.Fatalf("SetMasterAuth: %v", err)
	}

	if _, err := m.SetLegacyAbac(ctx, loc, name, true); err != nil {
		t.Fatalf("SetLegacyAbac: %v", err)
	}

	if _, err := m.SetNetworkPolicy(ctx, loc, name, true); err != nil {
		t.Fatalf("SetNetworkPolicy: %v", err)
	}

	if _, err := m.SetMaintenancePolicy(ctx, loc, name, "03:00"); err != nil {
		t.Fatalf("SetMaintenancePolicy: %v", err)
	}

	if _, err := m.SetResourceLabels(ctx, loc, name, map[string]string{"team": "core"}); err != nil {
		t.Fatalf("SetResourceLabels: %v", err)
	}

	if _, err := m.StartIPRotation(ctx, loc, name); err != nil {
		t.Fatalf("StartIPRotation: %v", err)
	}

	got, _ := m.GetCluster(ctx, loc, name)
	assertEqual(t, true, got.LegacyAbacEnabled)
	assertEqual(t, true, got.NetworkPolicy)
	assertEqual(t, "03:00", got.MaintenanceWindow)
	assertEqual(t, "admin", got.MasterUsername)
	assertEqual(t, "core", got.ResourceLabels["team"])
	assertEqual(t, true, got.IPRotationActive)

	if _, err := m.CompleteIPRotation(ctx, loc, name); err != nil {
		t.Fatalf("CompleteIPRotation: %v", err)
	}

	got, _ = m.GetCluster(ctx, loc, name)
	assertEqual(t, false, got.IPRotationActive)
}

func TestNodePoolLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	loc, cName := "us-central1", "host"

	_, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: cName, Location: loc})
	requireNoError(t, err)

	_, _, err = m.CreateNodePool(ctx, loc, cName, &NodePoolSpec{
		Name:             "extra",
		InitialNodeCount: 2,
		MachineType:      "e2-standard-4",
	})
	requireNoError(t, err)

	if _, _, err := m.CreateNodePool(ctx, loc, cName, &NodePoolSpec{Name: "extra"}); err == nil {
		t.Fatal("expected AlreadyExists on duplicate pool")
	}

	pool, err := m.GetNodePool(ctx, loc, cName, "extra")
	requireNoError(t, err)
	assertEqual(t, int64(2), pool.NodeCount)
	assertEqual(t, "e2-standard-4", pool.MachineType)

	pools, err := m.ListNodePools(ctx, loc, cName)
	requireNoError(t, err)

	if len(pools) != 2 {
		t.Fatalf("expected 2 pools (default + extra), got %d", len(pools))
	}

	if _, err := m.SetNodePoolSize(ctx, loc, cName, "extra", 5); err != nil {
		t.Fatalf("SetNodePoolSize: %v", err)
	}

	pool, _ = m.GetNodePool(ctx, loc, cName, "extra")
	assertEqual(t, int64(5), pool.NodeCount)

	if _, err := m.SetNodePoolAutoscaling(ctx, loc, cName, "extra", true, 1, 10); err != nil {
		t.Fatalf("SetNodePoolAutoscaling: %v", err)
	}

	pool, _ = m.GetNodePool(ctx, loc, cName, "extra")
	assertEqual(t, true, pool.AutoscalingOn)
	assertEqual(t, int64(1), pool.AutoscalingMin)
	assertEqual(t, int64(10), pool.AutoscalingMax)

	if _, err := m.SetNodePoolManagement(ctx, loc, cName, "extra", false, false); err != nil {
		t.Fatalf("SetNodePoolManagement: %v", err)
	}

	pool, _ = m.GetNodePool(ctx, loc, cName, "extra")
	assertEqual(t, false, pool.AutoUpgrade)
	assertEqual(t, false, pool.AutoRepair)

	if _, err := m.UpdateNodePool(ctx, loc, cName, "extra", UpdateNodePoolInput{
		NodeVersion: "1.31.0-gke.0",
		MachineType: "e2-standard-8",
	}); err != nil {
		t.Fatalf("UpdateNodePool: %v", err)
	}

	pool, _ = m.GetNodePool(ctx, loc, cName, "extra")
	assertEqual(t, "1.31.0-gke.0", pool.Version)
	assertEqual(t, "e2-standard-8", pool.MachineType)

	if _, err := m.RollbackNodePool(ctx, loc, cName, "extra"); err != nil {
		t.Fatalf("RollbackNodePool: %v", err)
	}

	pool, _ = m.GetNodePool(ctx, loc, cName, "extra")
	assertEqual(t, true, pool.UpgradeRolledBack)

	if _, err := m.DeleteNodePool(ctx, loc, cName, "extra"); err != nil {
		t.Fatalf("DeleteNodePool: %v", err)
	}

	if _, err := m.GetNodePool(ctx, loc, cName, "extra"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestNodePoolErrorPaths(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, _, err := m.CreateNodePool(ctx, "us-central1", "missing", &NodePoolSpec{Name: "x"}); err == nil {
		t.Fatal("expected NotFound when cluster missing")
	}

	_, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "host", Location: "us-central1"})
	requireNoError(t, err)

	if _, _, err := m.CreateNodePool(ctx, "us-central1", "host", &NodePoolSpec{}); err == nil {
		t.Fatal("expected InvalidArgument when name empty")
	}
}

func TestOperations(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, op, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "p", Location: "us-central1"})
	requireNoError(t, err)

	got, err := m.GetOperation(ctx, "us-central1", op.Name)
	requireNoError(t, err)
	assertEqual(t, op.Name, got.Name)
	assertEqual(t, "DONE", got.Status)

	ops, err := m.ListOperations(ctx, "us-central1")
	requireNoError(t, err)

	if len(ops) == 0 {
		t.Fatal("expected at least one operation")
	}

	if err := m.CancelOperation(ctx, "us-central1", op.Name); err != nil {
		t.Fatalf("CancelOperation: %v", err)
	}

	got, _ = m.GetOperation(ctx, "us-central1", op.Name)
	assertEqual(t, "ABORTING", got.Status)

	if err := m.CancelOperation(ctx, "us-central1", "missing"); err == nil {
		t.Fatal("expected NotFound on cancel of missing op")
	}
}

func TestCascadingDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	loc, cName := "us-central1", "host"

	_, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: cName, Location: loc})
	requireNoError(t, err)

	_, _, err = m.CreateNodePool(ctx, loc, cName, &NodePoolSpec{Name: "extra"})
	requireNoError(t, err)

	_, err = m.DeleteCluster(ctx, loc, cName)
	requireNoError(t, err)

	if _, err := m.GetNodePool(ctx, loc, cName, "extra"); err == nil {
		t.Fatal("expected node pools to be deleted along with cluster")
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
