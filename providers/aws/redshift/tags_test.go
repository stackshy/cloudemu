package redshift

import (
	"context"
	"maps"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func assertTags(t *testing.T, what string, got, want map[string]string) {
	t.Helper()

	if len(got) == 0 && len(want) == 0 {
		return
	}

	if !maps.Equal(got, want) {
		t.Fatalf("%s tags = %v, want %v", what, got, want)
	}
}

func TestModifyClusterTagsReplaceStore(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	c, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "c1", Tags: map[string]string{"a": "1"}})
	requireNoError(t, err)

	out, err := m.ModifyCluster(ctx, "c1", rdbdriver.ModifyInstanceInput{Tags: map[string]string{"b": "2"}})
	requireNoError(t, err)
	assertTags(t, "ModifyCluster", out.Tags, map[string]string{"b": "2"})

	got, err := m.DescribeTags(ctx, c.ARN)
	requireNoError(t, err)
	assertTags(t, "DescribeTags", got, map[string]string{"b": "2"})

	paused, err := m.PauseCluster(ctx, "c1")
	requireNoError(t, err)
	assertTags(t, "PauseCluster", paused.Tags, map[string]string{"b": "2"})
}

func TestRestoreClusterTagsGoToStore(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "src"})
	requireNoError(t, err)

	snap, err := m.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{
		ID: "s1", ClusterID: "src", Tags: map[string]string{"k": "snap"},
	})
	requireNoError(t, err)
	assertTags(t, "CreateClusterSnapshot", snap.Tags, map[string]string{"k": "snap"})

	restored, err := m.RestoreClusterFromSnapshot(ctx, rdbdriver.RestoreClusterInput{
		NewClusterID: "dst", SnapshotID: "s1", Tags: map[string]string{"k": "new"},
	})
	requireNoError(t, err)

	requireNoError(t, m.CreateTags(ctx, restored.ARN, map[string]string{"x": "y"}))

	clusters, err := m.DescribeClusters(ctx, []string{"dst"})
	requireNoError(t, err)
	assertTags(t, "DescribeClusters", clusters[0].Tags, map[string]string{"k": "new", "x": "y"})

	requireNoError(t, m.DeleteClusterSnapshot(ctx, "s1"))

	if _, ok := m.tagsByARN[snap.ARN]; ok {
		t.Fatal("tags of deleted snapshot are still stored")
	}

	if _, err := m.DescribeTags(ctx, snap.ARN); !cerrors.IsNotFound(err) {
		t.Fatalf("DescribeTags after snapshot delete: err = %v, want NotFound", err)
	}
}

// TestRestoreMovesRowTagsToStore covers state saved before tags moved to the
// ARN-keyed store: row tags must still read back and be editable.
func TestRestoreMovesRowTagsToStore(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, err := src.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "c1"})
	requireNoError(t, err)

	legacy, _ := src.clusters.Get("c1")
	legacy.Tags = map[string]string{"old": "row"}
	src.clusters.Set("c1", legacy)

	raw, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, raw))

	clusters, err := dst.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)
	assertTags(t, "DescribeClusters after restore", clusters[0].Tags, map[string]string{"old": "row"})

	requireNoError(t, dst.DeleteTags(ctx, legacy.ARN, []string{"old"}))

	clusters, err = dst.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)
	assertTags(t, "DescribeClusters after DeleteTags", clusters[0].Tags, nil)
}
