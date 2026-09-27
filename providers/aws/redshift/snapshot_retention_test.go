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

	snap, err := m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{
		ID: "s1", ClusterID: "c1", Tags: map[string]string{"k": "v"},
	})
	requireNoError(t, err)

	m.opts.Clock.(*config.FakeClock).Advance(3 * 24 * time.Hour)

	// A period that has not passed yet is applied, with the days left.
	ten := 10
	out, err := m.ModifyClusterSnapshot(ctx, "s1", &ten, false)
	requireNoError(t, err)
	assertEqual(t, 10, out.ManualSnapshotRetentionPeriod)
	assertEqual(t, 7, *out.ManualSnapshotRemainingDays)

	one := 1
	if _, err := m.ModifyClusterSnapshot(ctx, "s1", &one, false); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("passed period without force: err = %v, want InvalidArgument", err)
	}

	// With Force the snapshot is deleted right away, tags too.
	out, err = m.ModifyClusterSnapshot(ctx, "s1", &one, true)
	requireNoError(t, err)
	assertEqual(t, 1, out.ManualSnapshotRetentionPeriod)

	if m.clusterSnapshots.Has("s1") {
		t.Fatal("snapshot still stored after forced expiry")
	}

	if _, ok := m.tagsByARN[snap.ARN]; ok {
		t.Fatalf("tags of deleted snapshot are still stored")
	}

	if _, err := m.DescribeTags(ctx, snap.ARN); !cerrors.IsNotFound(err) {
		t.Fatalf("DescribeTags on deleted snapshot: err = %v, want NotFound", err)
	}
}

func TestSnapshotRemainingDaysOmittedWhenIndefinite(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "c1"})
	requireNoError(t, err)

	snap, err := m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{ID: "s1", ClusterID: "c1"})
	requireNoError(t, err)

	if snap.ManualSnapshotRemainingDays != nil {
		t.Fatalf("remaining days = %d, want none for -1", *snap.ManualSnapshotRemainingDays)
	}
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
