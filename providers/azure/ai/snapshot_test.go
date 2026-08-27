package ai

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

func newSnapshotTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"), config.WithAccountID("sub-1"))

	return New(opts)
}

// TestSnapshotRoundTripAI proves a snapshot/restore cycle reinstates the mock's
// state under the original identities: a Cognitive Services account created
// before the snapshot is readable from a fresh mock after restore, and
// re-snapshotting yields byte-identical JSON, so every store round-trips.
func TestSnapshotRoundTripAI(t *testing.T) {
	ctx := context.Background()
	src := newSnapshotTestMock()

	acct, err := src.CreateAccount(ctx, driver.AccountConfig{
		Name: "acct-1", ResourceGroup: "rg-1", Location: "eastus", Kind: "OpenAI",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newSnapshotTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.GetAccount(ctx, "rg-1", "acct-1")
	if err != nil {
		t.Fatalf("get restored account: %v", err)
	}

	if got.ID != acct.ID {
		t.Fatalf("restored account ID = %q, want %q", got.ID, acct.ID)
	}

	reSnap, err := dst.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("re-snapshot: %v", err)
	}

	if !bytes.Equal(raw, reSnap) {
		t.Fatalf("re-snapshot differs from original snapshot; a store did not round-trip")
	}
}
