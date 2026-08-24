package settle_test

import (
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/settle"
)

var base = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func TestPendingObservesIntermediateThenFinal(t *testing.T) {
	t.Parallel()

	w := settle.Pending("pending", base, 2*time.Second)

	if got := w.Observe(base, "running"); got != "pending" {
		t.Fatalf("at create: Observe = %q, want pending", got)
	}
	if got := w.Observe(base.Add(time.Second), "running"); got != "pending" {
		t.Fatalf("before readyAt: Observe = %q, want pending", got)
	}
	if got := w.Observe(base.Add(2*time.Second), "running"); got != "running" {
		t.Fatalf("at readyAt: Observe = %q, want running", got)
	}
	if got := w.Observe(base.Add(time.Hour), "running"); got != "running" {
		t.Fatalf("after readyAt: Observe = %q, want running", got)
	}
}

func TestSettled(t *testing.T) {
	t.Parallel()

	w := settle.Pending("creating", base, time.Second)
	if w.Settled(base) {
		t.Fatal("Settled at create = true, want false")
	}
	if !w.Settled(base.Add(time.Second)) {
		t.Fatal("Settled at readyAt = false, want true")
	}
}

func TestZeroWindowIsInactive(t *testing.T) {
	t.Parallel()

	// The zero value (and a non-positive duration) must report the final state
	// immediately — this is what preserves the synchronous default behavior.
	var zero settle.Window
	if got := zero.Observe(base, "available"); got != "available" {
		t.Fatalf("zero Observe = %q, want available", got)
	}
	if !zero.Settled(base) {
		t.Fatal("zero Settled = false, want true")
	}

	disabled := settle.Pending("creating", base, 0)
	if got := disabled.Observe(base, "available"); got != "available" {
		t.Fatalf("d<=0 Observe = %q, want available", got)
	}
}
