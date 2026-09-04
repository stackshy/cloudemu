package settle_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
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

func TestSetBeginStateAdvanceClear(t *testing.T) {
	t.Parallel()

	fc := config.NewFakeClock(base)
	s := settle.NewSet()

	// Absent id: State returns final, Settled reports done.
	if got := s.State("db1", fc.Now(), "available"); got != "available" {
		t.Fatalf("absent State = %q, want available", got)
	}
	if !s.Settled("db1", fc.Now()) {
		t.Fatal("absent Settled = false, want true")
	}

	s.Begin("db1", "creating", fc.Now(), 2*time.Second)

	// Immediately after Begin: intermediate observed, not settled.
	if got := s.State("db1", fc.Now(), "available"); got != "creating" {
		t.Fatalf("at begin State = %q, want creating", got)
	}
	if s.Settled("db1", fc.Now()) {
		t.Fatal("at begin Settled = true, want false")
	}

	// Before ReadyAt: still intermediate.
	fc.Advance(2*time.Second - time.Millisecond)
	if got := s.State("db1", fc.Now(), "available"); got != "creating" {
		t.Fatalf("before readyAt State = %q, want creating", got)
	}

	// At/after ReadyAt: final, settled.
	fc.Advance(time.Millisecond)
	if got := s.State("db1", fc.Now(), "available"); got != "available" {
		t.Fatalf("after readyAt State = %q, want available", got)
	}
	if !s.Settled("db1", fc.Now()) {
		t.Fatal("after readyAt Settled = false, want true")
	}

	// Clear drops the window: State returns final regardless of clock.
	s.Begin("db2", "creating", fc.Now(), time.Hour)
	if got := s.State("db2", fc.Now(), "available"); got != "creating" {
		t.Fatalf("db2 before clear State = %q, want creating", got)
	}
	s.Clear("db2")
	if got := s.State("db2", fc.Now(), "available"); got != "available" {
		t.Fatalf("db2 after clear State = %q, want available", got)
	}
	// Clear is idempotent.
	s.Clear("db2")
}

func TestSetBeginNonPositiveClears(t *testing.T) {
	t.Parallel()

	s := settle.NewSet()

	// A live window then a d<=0 Begin must clear it (opt-out / re-create).
	s.Begin("db1", "creating", base, time.Hour)
	if got := s.State("db1", base, "available"); got != "creating" {
		t.Fatalf("live window State = %q, want creating", got)
	}

	s.Begin("db1", "creating", base, 0)
	if got := s.State("db1", base, "available"); got != "available" {
		t.Fatalf("after d=0 Begin State = %q, want available", got)
	}

	// Negative duration on an absent id is a harmless no-op.
	s.Begin("db2", "creating", base, -time.Second)
	if got := s.State("db2", base, "available"); got != "available" {
		t.Fatalf("after d<0 Begin State = %q, want available", got)
	}
}

func TestSetConcurrentAccess(t *testing.T) {
	t.Parallel()

	s := settle.NewSet()

	const workers = 8

	var wg sync.WaitGroup

	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(n int) {
			defer wg.Done()

			id := "db" + string(rune('a'+n))

			for j := 0; j < 1000; j++ {
				s.Begin(id, "creating", base, time.Duration(j)*time.Second)
				_ = s.State(id, base.Add(time.Duration(j)*time.Second), "available")
				_ = s.Settled(id, base)
				s.Clear(id)
			}
		}(i)
	}

	wg.Wait()
}
