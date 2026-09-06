package mwaa_test

import (
	"context"
	"testing"
)

// TestSnapshotRoundTripMWAA proves a snapshot/restore round-trip preserves
// environments under their original identities, so the computed arn, createdAt
// and configuration survive a restart transparently.
func TestSnapshotRoundTripMWAA(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	created := create(t, src, "snap-env")

	raw, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newMock()
	requireNoError(t, dst.Restore(ctx, raw))

	restored, err := dst.GetEnvironment(ctx, "snap-env")
	requireNoError(t, err)

	if restored.Arn != created.Arn {
		t.Fatalf("arn drifted after restore: %q != %q", restored.Arn, created.Arn)
	}

	if !restored.CreatedAt.Equal(created.CreatedAt) {
		t.Fatal("createdAt drifted after restore")
	}

	if restored.Tags["a"] != "1" {
		t.Fatalf("tags not preserved: %+v", restored.Tags)
	}

	if string(restored.Config["LoggingConfiguration"]) != string(created.Config["LoggingConfiguration"]) {
		t.Fatal("LoggingConfiguration not preserved after restore")
	}
}
