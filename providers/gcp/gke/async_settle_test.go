package gke

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
)

// newAsyncMock builds a GKE mock with AsyncSettle enabled and a FakeClock so
// create/mutate calls report their real transient GKE states deterministically.
func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-central1"),
		config.WithProjectID("mock-project"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

// TestAsyncSettleClusterCreate pins the AsyncSettle transitions for
// CreateCluster: the cluster and its bootstrap default node pool report
// PROVISIONING (and the create operation reports RUNNING) until the cluster
// settle window elapses, then RUNNING/DONE.
func TestAsyncSettleClusterCreate(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	c, op, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "prod", Location: "us-central1"})
	requireNoError(t, err)

	if c.Status != statusProvisioning {
		t.Fatalf("create response Status = %q, want %q", c.Status, statusProvisioning)
	}

	if op.Status != opStatusRunning {
		t.Fatalf("create response op.Status = %q, want %q", op.Status, opStatusRunning)
	}

	got, err := m.GetCluster(ctx, "us-central1", "prod")
	requireNoError(t, err)

	if got.Status != statusProvisioning {
		t.Fatalf("GetCluster before settle = %q, want %q", got.Status, statusProvisioning)
	}

	pool, err := m.GetNodePool(ctx, "us-central1", "prod", "default-pool")
	requireNoError(t, err)

	if pool.Status != statusProvisioning {
		t.Fatalf("GetNodePool before settle = %q, want %q", pool.Status, statusProvisioning)
	}

	opGot, err := m.GetOperation(ctx, "us-central1", op.Name)
	requireNoError(t, err)

	if opGot.Status != opStatusRunning {
		t.Fatalf("GetOperation before settle = %q, want %q", opGot.Status, opStatusRunning)
	}

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultGKEClusterSettle - time.Millisecond)

	got, _ = m.GetCluster(ctx, "us-central1", "prod")
	if got.Status != statusProvisioning {
		t.Fatalf("GetCluster just before settle = %q, want %q", got.Status, statusProvisioning)
	}

	// Past the window: cluster, node pool, and operation all settle together.
	fc.Advance(time.Millisecond)

	got, _ = m.GetCluster(ctx, "us-central1", "prod")
	if got.Status != statusRunning {
		t.Fatalf("GetCluster after settle = %q, want %q", got.Status, statusRunning)
	}

	pool, _ = m.GetNodePool(ctx, "us-central1", "prod", "default-pool")
	if pool.Status != statusRunning {
		t.Fatalf("GetNodePool after settle = %q, want %q", pool.Status, statusRunning)
	}

	opGot, _ = m.GetOperation(ctx, "us-central1", op.Name)
	if opGot.Status != opStatusDone {
		t.Fatalf("GetOperation after settle = %q, want %q", opGot.Status, opStatusDone)
	}
}

// TestAsyncSettleNodePoolCreate pins the standalone CreateNodePool transition:
// PROVISIONING -> RUNNING over the node-pool settle window, independent of the
// (already-settled) parent cluster.
func TestAsyncSettleNodePoolCreate(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "host", Location: "us-central1"})
	requireNoError(t, err)

	fc.Advance(settle.DefaultGKEClusterSettle)

	np, op, err := m.CreateNodePool(ctx, "us-central1", "host", &NodePoolSpec{Name: "extra", InitialNodeCount: int64Ptr(1)})
	requireNoError(t, err)

	if np.Status != statusProvisioning {
		t.Fatalf("CreateNodePool response Status = %q, want %q", np.Status, statusProvisioning)
	}

	if op.Status != opStatusRunning {
		t.Fatalf("CreateNodePool response op.Status = %q, want %q", op.Status, opStatusRunning)
	}

	fc.Advance(settle.DefaultGKENodePoolSettle)

	got, err := m.GetNodePool(ctx, "us-central1", "host", "extra")
	requireNoError(t, err)

	if got.Status != statusRunning {
		t.Fatalf("GetNodePool after settle = %q, want %q", got.Status, statusRunning)
	}
}

// TestAsyncSettleNodePoolResize pins SetNodePoolSize: the node count applies
// immediately (currentNodeCount is never gated by the settle window) while the
// pool's status briefly reports RECONCILING before settling back to RUNNING.
func TestAsyncSettleNodePoolResize(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "host", Location: "us-central1", InitialNodeCount: int64Ptr(1)})
	requireNoError(t, err)

	fc.Advance(settle.DefaultGKEClusterSettle)

	op, err := m.SetNodePoolSize(ctx, "us-central1", "host", "default-pool", 3)
	requireNoError(t, err)

	if op.Status != opStatusRunning {
		t.Fatalf("SetNodePoolSize response op.Status = %q, want %q", op.Status, opStatusRunning)
	}

	got, err := m.GetNodePool(ctx, "us-central1", "host", "default-pool")
	requireNoError(t, err)

	if got.NodeCount != 3 {
		t.Fatalf("NodeCount = %d, want 3 (applied immediately, not gated by settle)", got.NodeCount)
	}

	if got.Status != statusReconciling {
		t.Fatalf("Status during resize = %q, want %q", got.Status, statusReconciling)
	}

	fc.Advance(settle.DefaultGKEReconcileSettle)

	got, _ = m.GetNodePool(ctx, "us-central1", "host", "default-pool")
	if got.Status != statusRunning {
		t.Fatalf("Status after resize settle = %q, want %q", got.Status, statusRunning)
	}

	if got.NodeCount != 3 {
		t.Fatalf("NodeCount after settle = %d, want 3", got.NodeCount)
	}
}

// TestAsyncSettleCancelSupersedesWindow proves a cancel is observed
// immediately even while a create's settle window is still open.
func TestAsyncSettleCancelSupersedesWindow(t *testing.T) {
	m, _ := newAsyncMock()
	ctx := context.Background()

	_, op, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "prod", Location: "us-central1"})
	requireNoError(t, err)

	if err := m.CancelOperation(ctx, "us-central1", op.Name); err != nil {
		t.Fatalf("CancelOperation: %v", err)
	}

	got, err := m.GetOperation(ctx, "us-central1", op.Name)
	requireNoError(t, err)

	if got.Status != opStatusAborting {
		t.Fatalf("GetOperation after cancel = %q, want %q (must not be masked by the pending RUNNING window)", got.Status, opStatusAborting)
	}
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: cluster, node pool, and operation report their
// terminal state immediately, with no transient state.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	c, op, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "prod", Location: "us-central1"})
	requireNoError(t, err)

	if c.Status != statusRunning {
		t.Fatalf("create response Status = %q, want %q (settle off)", c.Status, statusRunning)
	}

	if op.Status != opStatusDone {
		t.Fatalf("create response op.Status = %q, want %q (settle off)", op.Status, opStatusDone)
	}

	np, npOp, err := m.CreateNodePool(ctx, "us-central1", "prod", &NodePoolSpec{Name: "extra"})
	requireNoError(t, err)

	if np.Status != statusRunning || npOp.Status != opStatusDone {
		t.Fatalf("CreateNodePool response = (%q, %q), want (%q, %q) (settle off)",
			np.Status, npOp.Status, statusRunning, opStatusDone)
	}

	resizeOp, err := m.SetNodePoolSize(ctx, "us-central1", "prod", "extra", 5)
	requireNoError(t, err)

	if resizeOp.Status != opStatusDone {
		t.Fatalf("SetNodePoolSize response op.Status = %q, want %q (settle off)", resizeOp.Status, opStatusDone)
	}

	got, _ := m.GetNodePool(ctx, "us-central1", "prod", "extra")
	if got.Status != statusRunning || got.NodeCount != 5 {
		t.Fatalf("GetNodePool after resize = (%q, %d), want (%q, 5) (settle off)", got.Status, got.NodeCount, statusRunning)
	}
}
