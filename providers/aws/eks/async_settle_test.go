package eks

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// newAsyncMock builds an EKS mock with AsyncSettle enabled and a FakeClock so
// create/update report their real transient states deterministically.
func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

func clusterStatus(t *testing.T, m *Mock, name string) string {
	t.Helper()

	got, err := m.DescribeCluster(context.Background(), name)
	if err != nil {
		t.Fatalf("describe cluster: %v", err)
	}

	return got.Status
}

func nodegroupStatus(t *testing.T, m *Mock, clusterName, nodegroupName string) string {
	t.Helper()

	got, err := m.DescribeNodegroup(context.Background(), clusterName, nodegroupName)
	if err != nil {
		t.Fatalf("describe nodegroup: %v", err)
	}

	return got.Status
}

// TestAsyncSettleClusterCreateUpdate pins the AsyncSettle transitions for a
// cluster: CREATING then ACTIVE on create, UPDATING then ACTIVE on update, and
// a rejected overlapping update while the window is still open.
func TestAsyncSettleClusterCreateUpdate(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	created, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if created.Status != eksdriver.ClusterStatusCreating {
		t.Fatalf("create status = %q, want %q", created.Status, eksdriver.ClusterStatusCreating)
	}

	if got := clusterStatus(t, m, "c1"); got != eksdriver.ClusterStatusCreating {
		t.Fatalf("describe status = %q, want %q", got, eksdriver.ClusterStatusCreating)
	}

	// A cluster-level update is rejected while creation is still settling.
	if _, err := m.UpdateClusterVersion(ctx, "c1", "1.31"); err == nil {
		t.Fatal("expected UpdateClusterVersion to reject a cluster that is still CREATING")
	}

	// Nodegroup creation is rejected too: the cluster isn't ACTIVE yet.
	if _, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"}); err == nil {
		t.Fatal("expected CreateNodegroup to reject a cluster that is not ACTIVE")
	}

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultClusterSettle - time.Millisecond)

	if got := clusterStatus(t, m, "c1"); got != eksdriver.ClusterStatusCreating {
		t.Fatalf("pre-settle status = %q, want %q", got, eksdriver.ClusterStatusCreating)
	}

	// Past the window: terminal ACTIVE.
	fc.Advance(time.Millisecond)

	if got := clusterStatus(t, m, "c1"); got != eksdriver.ClusterStatusActive {
		t.Fatalf("settled status = %q, want %q", got, eksdriver.ClusterStatusActive)
	}

	// Update -> UPDATING -> ACTIVE.
	updated, err := m.UpdateClusterVersion(ctx, "c1", "1.31")
	if err != nil {
		t.Fatalf("update cluster version: %v", err)
	}

	if updated.Status != "Successful" {
		t.Fatalf("update record status = %q, want Successful", updated.Status)
	}

	if got := clusterStatus(t, m, "c1"); got != eksdriver.ClusterStatusUpdating {
		t.Fatalf("post-update describe status = %q, want %q", got, eksdriver.ClusterStatusUpdating)
	}

	// A second update is rejected while the first is still settling.
	if _, err := m.UpdateClusterConfig(ctx, "c1", eksdriver.VPCConfig{}, nil); err == nil {
		t.Fatal("expected UpdateClusterConfig to reject an overlapping update")
	}

	fc.Advance(settle.DefaultClusterSettle)

	if got := clusterStatus(t, m, "c1"); got != eksdriver.ClusterStatusActive {
		t.Fatalf("post-update settled status = %q, want %q", got, eksdriver.ClusterStatusActive)
	}
}

// TestAsyncSettleNodegroupCreateUpdate pins the AsyncSettle transitions for a
// nodegroup: CREATING then ACTIVE on create, UPDATING then ACTIVE on config
// update, and a rejected overlapping update while the window is still open.
func TestAsyncSettleNodegroupCreateUpdate(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	fc.Advance(settle.DefaultClusterSettle)

	created, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName:   "c1",
		NodegroupName: "ng1",
		ScalingConfig: eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: 2},
	})
	if err != nil {
		t.Fatalf("create nodegroup: %v", err)
	}

	if created.Status != eksdriver.NodegroupStatusCreating {
		t.Fatalf("create status = %q, want %q", created.Status, eksdriver.NodegroupStatusCreating)
	}

	if got := nodegroupStatus(t, m, "c1", "ng1"); got != eksdriver.NodegroupStatusCreating {
		t.Fatalf("describe status = %q, want %q", got, eksdriver.NodegroupStatusCreating)
	}

	// An update is rejected while creation is still settling.
	if _, err := m.UpdateNodegroupConfig(ctx, "c1", "ng1", eksdriver.NodegroupConfigUpdate{}); err == nil {
		t.Fatal("expected UpdateNodegroupConfig to reject a nodegroup that is still CREATING")
	}

	fc.Advance(settle.DefaultClusterSettle)

	if got := nodegroupStatus(t, m, "c1", "ng1"); got != eksdriver.NodegroupStatusActive {
		t.Fatalf("settled status = %q, want %q", got, eksdriver.NodegroupStatusActive)
	}

	scaling := eksdriver.NodegroupScalingConfig{MinSize: 2, MaxSize: 5, DesiredSize: 4}

	upd, err := m.UpdateNodegroupConfig(ctx, "c1", "ng1", eksdriver.NodegroupConfigUpdate{Scaling: &scaling})
	if err != nil {
		t.Fatalf("update nodegroup config: %v", err)
	}

	if upd.Status != "Successful" {
		t.Fatalf("update record status = %q, want Successful", upd.Status)
	}

	if got := nodegroupStatus(t, m, "c1", "ng1"); got != eksdriver.NodegroupStatusUpdating {
		t.Fatalf("post-update describe status = %q, want %q", got, eksdriver.NodegroupStatusUpdating)
	}

	fc.Advance(settle.DefaultClusterSettle)

	got, err := m.DescribeNodegroup(ctx, "c1", "ng1")
	if err != nil {
		t.Fatalf("describe nodegroup: %v", err)
	}

	if got.Status != eksdriver.NodegroupStatusActive {
		t.Fatalf("settled status = %q, want %q", got.Status, eksdriver.NodegroupStatusActive)
	}

	if got.ScalingConfig.DesiredSize != 4 {
		t.Fatalf("desiredSize = %d, want 4", got.ScalingConfig.DesiredSize)
	}
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and update are observed as ACTIVE
// immediately, with no transient state and no overlapping-update rejection.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	created, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if created.Status != eksdriver.ClusterStatusActive {
		t.Fatalf("create status = %q, want %q", created.Status, eksdriver.ClusterStatusActive)
	}

	ng, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	if err != nil {
		t.Fatalf("create nodegroup: %v", err)
	}

	if ng.Status != eksdriver.NodegroupStatusActive {
		t.Fatalf("nodegroup create status = %q, want %q", ng.Status, eksdriver.NodegroupStatusActive)
	}

	if _, err := m.UpdateClusterVersion(ctx, "c1", "1.31"); err != nil {
		t.Fatalf("update cluster version: %v", err)
	}

	if got := clusterStatus(t, m, "c1"); got != eksdriver.ClusterStatusActive {
		t.Fatalf("post-update status = %q, want %q", got, eksdriver.ClusterStatusActive)
	}
}
