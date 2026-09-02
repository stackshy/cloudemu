package savingsplans

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

const (
	storeTestAccount  = "123456789012"
	storeTestRegion   = "us-east-1"
	storeTestOffering = "sp-offering-compute-1yr-no"
)

func newTestStore(t *testing.T) (*store, *config.FakeClock) {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC))

	return newStore(storeTestAccount, storeTestRegion, clock), clock
}

// stateOf returns the single plan's clock-derived state as DescribeSavingsPlans
// would report it, failing if the store does not hold exactly one plan.
func stateOf(t *testing.T, s *store) string {
	t.Helper()

	plans := s.describe(planFilter{})
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}

	return plans[0].State
}

// TestLazyStateTransitions is the regression test for the frozen-state bug: a
// plan bought for the future must transition queued -> active -> retired as the
// clock advances, and must feed cost.Commitments exactly while active.
func TestLazyStateTransitions(t *testing.T) {
	s, clock := newTestStore(t)
	future := clock.Now().Add(time.Hour)

	id, err := s.create(&createInput{
		savingsPlanOfferingID: storeTestOffering,
		commitment:            "1.500",
		purchaseTime:          future,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Before Start: queued, and no commitment feeds the engine.
	if got := stateOf(t, s); got != stateQueued {
		t.Fatalf("at creation: expected %q, got %q", stateQueued, got)
	}

	if commitments := listActive(t, s, clock.Now()); len(commitments) != 0 {
		t.Fatalf("queued plan must not feed commitments, got %d", len(commitments))
	}

	// Advance past Start: active, and it now feeds a commitment.
	clock.Advance(2 * time.Hour)

	if got := stateOf(t, s); got != stateActive {
		t.Fatalf("after purchaseTime: expected %q, got %q", stateActive, got)
	}

	commitments := listActive(t, s, clock.Now())
	if len(commitments) != 1 {
		t.Fatalf("active plan must feed exactly 1 commitment, got %d", len(commitments))
	}

	if commitments[0].ID != id {
		t.Fatalf("commitment id mismatch: %q != %q", commitments[0].ID, id)
	}

	if commitments[0].HourlyCommitmentUSD != 1.5 {
		t.Fatalf("commitment hourly mismatch: %v", commitments[0].HourlyCommitmentUSD)
	}

	// Advance past End (the plan's one-year term): retired, and it drops out.
	clock.Advance(2 * 365 * 24 * time.Hour)

	if got := stateOf(t, s); got != stateRetired {
		t.Fatalf("after term end: expected %q, got %q", stateRetired, got)
	}

	if commitments := listActive(t, s, clock.Now()); len(commitments) != 0 {
		t.Fatalf("retired plan must not feed commitments, got %d", len(commitments))
	}
}

// TestQueuedDeletedStaysTerminal verifies queued-deleted is sticky: the clock
// passing Start must NOT resurrect a deleted-queued plan to active.
func TestQueuedDeletedStaysTerminal(t *testing.T) {
	s, clock := newTestStore(t)
	id, err := s.create(&createInput{
		savingsPlanOfferingID: storeTestOffering,
		commitment:            "0.500",
		purchaseTime:          clock.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.deleteQueued(id); err != nil {
		t.Fatalf("deleteQueued: %v", err)
	}

	if got := stateOf(t, s); got != stateQueuedDeleted {
		t.Fatalf("after delete: expected %q, got %q", stateQueuedDeleted, got)
	}

	// Advancing past what would have been Start must not revive it.
	clock.Advance(2 * time.Hour)

	if got := stateOf(t, s); got != stateQueuedDeleted {
		t.Fatalf("after advance: expected sticky %q, got %q", stateQueuedDeleted, got)
	}

	if commitments := listActive(t, s, clock.Now()); len(commitments) != 0 {
		t.Fatalf("queued-deleted plan must never feed commitments, got %d", len(commitments))
	}

	// Deleting an already-deleted (non-queued) plan must fail.
	if err := s.deleteQueued(id); err == nil {
		t.Fatal("expected error deleting a queued-deleted plan")
	}
}

// TestDeleteRejectsActive verifies a plan whose Start has passed (now active)
// cannot be deleted through the queued path, using the effective state.
func TestDeleteRejectsActive(t *testing.T) {
	s, clock := newTestStore(t)

	id, err := s.create(&createInput{
		savingsPlanOfferingID: storeTestOffering,
		commitment:            "1.000",
		purchaseTime:          clock.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	clock.Advance(2 * time.Hour) // plan is now active

	if err := s.deleteQueued(id); err == nil {
		t.Fatal("expected error deleting an active plan via the queued path")
	}
}

func listActive(t *testing.T, s *store, at time.Time) []cost.Commitment {
	t.Helper()

	out, err := s.ListActive(context.Background(), at)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}

	return out
}
