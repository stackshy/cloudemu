package eks

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/settle"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

const (
	updateInProgress = "InProgress"
	updateSuccessful = "Successful"
)

func updateStatus(t *testing.T, m *Mock, cluster, id string) string {
	t.Helper()

	u, err := m.DescribeUpdate(context.Background(), cluster, id)
	requireNoError(t, err)

	return u.Status
}

// TestAsyncSettleClusterVersionAppliesOnCompletion checks that under
// AsyncSettle an UpdateClusterVersion keeps reporting the old version, in
// DescribeCluster and on the data plane's /version, until the update settles.
func TestAsyncSettleClusterVersionAppliesOnCompletion(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()
	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.31"})
	requireNoError(t, err)
	fc.Advance(settle.DefaultClusterSettle)

	uid := m.k8sUIDs["c1"]

	upd, err := m.UpdateClusterVersion(ctx, "c1", toVersion("1.32"))
	requireNoError(t, err)

	if upd.Status != updateInProgress {
		t.Fatalf("update response status = %q, want %q", upd.Status, updateInProgress)
	}

	fc.Advance(settle.DefaultClusterSettle - time.Millisecond)

	got, err := m.DescribeCluster(ctx, "c1")
	requireNoError(t, err)

	if got.Version != "1.31" || got.Status != eksdriver.ClusterStatusUpdating {
		t.Fatalf("mid-update describe = %s/%s, want 1.31/UPDATING", got.Version, got.Status)
	}

	if _, minor := dataPlaneVersion(t, api, uid); minor != "31+" {
		t.Fatalf("mid-update /version minor = %q, want 31+", minor)
	}

	if s := updateStatus(t, m, "c1", upd.ID); s != updateInProgress {
		t.Fatalf("mid-update DescribeUpdate status = %q, want %q", s, updateInProgress)
	}

	fc.Advance(time.Millisecond)

	got, err = m.DescribeCluster(ctx, "c1")
	requireNoError(t, err)

	if got.Version != "1.32" || got.Status != eksdriver.ClusterStatusActive {
		t.Fatalf("settled describe = %s/%s, want 1.32/ACTIVE", got.Version, got.Status)
	}

	if _, minor := dataPlaneVersion(t, api, uid); minor != "32+" {
		t.Fatalf("settled /version minor = %q, want 32+", minor)
	}

	if s := updateStatus(t, m, "c1", upd.ID); s != updateSuccessful {
		t.Fatalf("settled DescribeUpdate status = %q, want %q", s, updateSuccessful)
	}
}

// TestAsyncSettleNodegroupVersionAppliesOnCompletion is the nodegroup analog:
// the nodegroup keeps its old version until UpdateNodegroupVersion settles, and
// while the control plane upgrade is still running a nodegroup can't move to
// the version the cluster hasn't reached yet.
func TestAsyncSettleNodegroupVersionAppliesOnCompletion(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.31"})
	requireNoError(t, err)
	fc.Advance(settle.DefaultClusterSettle)

	_, err = m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	requireNoError(t, err)
	fc.Advance(settle.DefaultClusterSettle)

	_, err = m.UpdateClusterVersion(ctx, "c1", toVersion("1.32"))
	requireNoError(t, err)

	_, err = m.UpdateNodegroupVersion(ctx, "c1", "ng1", eksdriver.NodegroupVersionUpdate{Version: "1.32"})
	if err == nil {
		t.Fatal("expected UpdateNodegroupVersion to 1.32 to fail while the control plane is still on 1.31")
	}

	fc.Advance(settle.DefaultClusterSettle)

	upd, err := m.UpdateNodegroupVersion(ctx, "c1", "ng1", eksdriver.NodegroupVersionUpdate{Version: "1.32"})
	requireNoError(t, err)

	if upd.Status != updateInProgress {
		t.Fatalf("update response status = %q, want %q", upd.Status, updateInProgress)
	}

	ng, err := m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	if ng.Version != "1.31" || ng.Status != eksdriver.NodegroupStatusUpdating {
		t.Fatalf("mid-update nodegroup = %s/%s, want 1.31/UPDATING", ng.Version, ng.Status)
	}

	if s := updateStatus(t, m, "c1", upd.ID); s != updateInProgress {
		t.Fatalf("mid-update DescribeUpdate status = %q, want %q", s, updateInProgress)
	}

	fc.Advance(settle.DefaultClusterSettle)

	ng, err = m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	if ng.Version != "1.32" || ng.Status != eksdriver.NodegroupStatusActive {
		t.Fatalf("settled nodegroup = %s/%s, want 1.32/ACTIVE", ng.Version, ng.Status)
	}

	if s := updateStatus(t, m, "c1", upd.ID); s != updateSuccessful {
		t.Fatalf("settled DescribeUpdate status = %q, want %q", s, updateSuccessful)
	}
}

// TestSyncClusterVersionImmediate checks the default path: with AsyncSettle
// off the new version and a Successful update are visible straight away.
func TestSyncClusterVersionImmediate(t *testing.T) {
	m, _ := newVersionMock(t, "1.31")
	ctx := context.Background()

	upd, err := m.UpdateClusterVersion(ctx, "c1", toVersion("1.32"))
	requireNoError(t, err)

	if upd.Status != updateSuccessful {
		t.Fatalf("update status = %q, want %q", upd.Status, updateSuccessful)
	}

	got, err := m.DescribeCluster(ctx, "c1")
	requireNoError(t, err)

	if got.Version != "1.32" {
		t.Fatalf("version = %s, want 1.32", got.Version)
	}
}
