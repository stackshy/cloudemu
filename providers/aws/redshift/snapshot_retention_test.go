package redshift

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestModifyClusterSnapshotForce(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "c1"})
	requireNoError(t, err)

	_, err = m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{ID: "s1", ClusterID: "c1"})
	requireNoError(t, err)

	m.opts.Clock.(*config.FakeClock).Advance(3 * 24 * time.Hour)

	one := 1
	if _, err := m.ModifyClusterSnapshot(ctx, "s1", &one, false); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("passed period without force: err = %v, want InvalidArgument", err)
	}

	out, err := m.ModifyClusterSnapshot(ctx, "s1", &one, true)
	requireNoError(t, err)
	assertEqual(t, 1, out.ManualSnapshotRetentionPeriod)

	ten := 10
	out, err = m.ModifyClusterSnapshot(ctx, "s1", &ten, false)
	requireNoError(t, err)
	assertEqual(t, 10, out.ManualSnapshotRetentionPeriod)
}

// TestLegacySnapshotRetentionReadsIndefinite covers rows saved before the
// retention field existed (zero value).
func TestLegacySnapshotRetentionReadsIndefinite(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "c1"})
	requireNoError(t, err)

	_, err = m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{ID: "s1", ClusterID: "c1"})
	requireNoError(t, err)

	row, _ := m.clusterSnapshots.Get("s1")
	row.ManualSnapshotRetentionPeriod = 0
	m.clusterSnapshots.Set("s1", row)

	snaps, err := m.DescribeClusterSnapshots(ctx, []string{"s1"}, "")
	requireNoError(t, err)
	assertEqual(t, -1, snaps[0].ManualSnapshotRetentionPeriod)
}
