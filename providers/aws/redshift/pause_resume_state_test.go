package redshift

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestPauseResumePreconditions asserts Pause/Resume are strict about their
// source state, unlike the idempotent portable Start/Stop: resuming an
// available cluster and pausing a paused one are FailedPrecondition
// (InvalidClusterState on the wire).
func TestPauseResumePreconditions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "w"}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := m.ResumeCluster(ctx, "w"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("resume available cluster: err = %v, want FailedPrecondition", err)
	}

	if _, err := m.PauseCluster(ctx, "w"); err != nil {
		t.Fatalf("pause: %v", err)
	}

	if _, err := m.PauseCluster(ctx, "w"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("pause paused cluster: err = %v, want FailedPrecondition", err)
	}

	if _, err := m.ResumeCluster(ctx, "w"); err != nil {
		t.Fatalf("resume: %v", err)
	}
}

// TestPauseWhileCreatingRejected asserts that with AsyncSettle a cluster still
// creating cannot be paused until it becomes available.
func TestPauseWhileCreatingRejected(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "c"}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := m.PauseCluster(ctx, "c"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("pause creating cluster: err = %v, want FailedPrecondition", err)
	}

	fc.Advance(settle.DefaultClusterSettle)

	paused, err := m.PauseCluster(ctx, "c")
	if err != nil || paused.State != clusterStatePaused {
		t.Fatalf("pause after settle: state=%v err=%v", paused, err)
	}
}
