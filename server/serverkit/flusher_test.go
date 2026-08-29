package serverkit

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it is true or the deadline passes, returning whether
// it became true. It keeps the timing tests robust without fixed long sleeps.
func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}

		time.Sleep(2 * time.Millisecond)
	}

	return cond()
}

// TestFlusherScheduledSavesOnlyWhenDirty asserts the scheduled ticker saves on a
// tick only when state changed, and skips ticks while clean.
func TestFlusherScheduledSavesOnlyWhenDirty(t *testing.T) {
	var saves atomic.Int64

	save := func(_ context.Context, _ bool) error {
		saves.Add(1)

		return nil
	}

	f := newFlusher(StrategyScheduled, 10*time.Millisecond, false, save, io.Discard, true, "state.json")
	f.Start()

	// Clean for several intervals: no save.
	time.Sleep(60 * time.Millisecond)

	if got := saves.Load(); got != 0 {
		t.Fatalf("clean scheduled flusher saved %d times, want 0", got)
	}

	f.markDirty()

	if !waitFor(func() bool { return saves.Load() >= 1 }, time.Second) {
		t.Fatal("dirty scheduled flusher never saved")
	}

	before := saves.Load()
	f.Stop(context.Background())

	// Stop performs one deterministic final save (non-manual).
	if got := saves.Load(); got != before+1 {
		t.Fatalf("Stop saves = %d, want %d (one final save)", got, before+1)
	}
}

// TestFlusherManualNeverSaves locks the manual contract: no periodic loop, and —
// unlike every other strategy — no final save on Stop (graceful shutdown).
func TestFlusherManualNeverSaves(t *testing.T) {
	var saves atomic.Int64

	save := func(_ context.Context, _ bool) error {
		saves.Add(1)

		return nil
	}

	f := newFlusher(StrategyManual, 10*time.Millisecond, false, save, io.Discard, true, "state.json")
	f.Start()
	f.markDirty()
	time.Sleep(50 * time.Millisecond)

	f.Stop(context.Background())

	if got := saves.Load(); got != 0 {
		t.Fatalf("manual strategy saved %d times, want 0 (including on shutdown)", got)
	}
}

// TestFlusherOnShutdownSavesOnlyAtStop asserts on-shutdown runs no periodic loop
// but performs exactly one save at Stop.
func TestFlusherOnShutdownSavesOnlyAtStop(t *testing.T) {
	var saves atomic.Int64

	save := func(_ context.Context, _ bool) error {
		saves.Add(1)

		return nil
	}

	f := newFlusher(StrategyOnShutdown, 10*time.Millisecond, false, save, io.Discard, true, "state.json")
	f.Start()
	f.markDirty()
	time.Sleep(50 * time.Millisecond)

	if got := saves.Load(); got != 0 {
		t.Fatalf("on-shutdown saved %d times while running, want 0", got)
	}

	f.Stop(context.Background())

	if got := saves.Load(); got != 1 {
		t.Fatalf("on-shutdown Stop saves = %d, want 1", got)
	}
}

// TestFlusherOnRequestCapFiresUnderContinuousWrites is the reviewer catch: a pure
// trailing-edge debounce would never fire under a sustained write loop, so the
// hard max-wait cap must force a save. Debounce is set huge so only the cap can
// fire.
func TestFlusherOnRequestCapFiresUnderContinuousWrites(t *testing.T) {
	var saves atomic.Int64

	save := func(_ context.Context, _ bool) error {
		saves.Add(1)

		return nil
	}

	f := newFlusher(StrategyOnRequest, 0, false, save, io.Discard, true, "state.json")
	f.debounce = time.Hour            // never quiet under continuous writes
	f.maxWait = 40 * time.Millisecond // cap is the only thing that can fire
	f.Start()

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				f.markDirty()
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	got := waitFor(func() bool { return saves.Load() >= 1 }, time.Second)
	close(stop)
	f.Stop(context.Background())

	if !got {
		t.Fatal("on-request cap never fired under a sustained write loop")
	}
}

// TestFlusherSingleFlightAndFinalIsNewest proves two invariants at once:
//   - single-flight: a save slower than the tick interval never overlaps itself.
//   - final-write-wins: after the loop drains, the deterministic final save is
//     the last one to run and observes the newest state version, so no stale tick
//     can rename over it.
//
// Run under -race, it also exercises the markDirty/save concurrency.
func TestFlusherSingleFlightAndFinalIsNewest(t *testing.T) {
	var (
		version  atomic.Int64
		inFlight atomic.Int32
		mu       sync.Mutex
		saved    []int64
	)

	save := func(_ context.Context, _ bool) error {
		if n := inFlight.Add(1); n > 1 {
			t.Errorf("single-flight violated: %d concurrent saves", n)
		}

		v := version.Load()
		time.Sleep(12 * time.Millisecond) // slower than the 4ms interval
		mu.Lock()
		saved = append(saved, v)
		mu.Unlock()
		inFlight.Add(-1)

		return nil
	}

	f := newFlusher(StrategyScheduled, 4*time.Millisecond, false, save, io.Discard, true, "state.json")
	f.Start()

	for range 20 {
		version.Add(1)
		f.markDirty()
		time.Sleep(3 * time.Millisecond)
	}

	final := version.Load()
	f.Stop(context.Background())

	mu.Lock()
	defer mu.Unlock()

	if len(saved) == 0 {
		t.Fatal("no saves recorded")
	}

	if last := saved[len(saved)-1]; last != final {
		t.Fatalf("final save observed version %d, want newest %d", last, final)
	}
}

// TestFlusherReDirtiesOnSaveError asserts a failed save re-marks state dirty so
// the next tick retries rather than silently dropping the change.
func TestFlusherReDirtiesOnSaveError(t *testing.T) {
	var (
		attempts atomic.Int64
		fail     atomic.Bool
	)

	fail.Store(true)

	save := func(_ context.Context, _ bool) error {
		attempts.Add(1)
		if fail.Load() {
			return context.DeadlineExceeded
		}

		return nil
	}

	f := newFlusher(StrategyScheduled, 8*time.Millisecond, false, save, io.Discard, true, "state.json")
	f.Start()
	f.markDirty()

	// First attempt fails and re-dirties; a later tick retries. Then let it
	// succeed and confirm the flag clears (no unbounded retries).
	if !waitFor(func() bool { return attempts.Load() >= 2 }, time.Second) {
		t.Fatal("failed save did not retry (dirty not re-set)")
	}

	fail.Store(false)

	if !waitFor(func() bool { return !f.dirty.Load() }, time.Second) {
		t.Fatal("dirty flag never cleared after a successful save")
	}

	f.Stop(context.Background())
}
