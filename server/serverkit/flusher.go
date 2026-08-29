package serverkit

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Persistence strategies. They mirror the LocalStack SNAPSHOT_SAVE_STRATEGY
// matrix so users moving over find the same knobs.
const (
	// StrategyScheduled saves on a background ticker whenever state changed
	// since the last save, plus a final save on shutdown. The default.
	StrategyScheduled = "scheduled"
	// StrategyOnRequest saves shortly after mutations settle (trailing-edge
	// coalesce) but at least once per max-wait cap under sustained writes, plus
	// a final save on shutdown.
	StrategyOnRequest = "on-request"
	// StrategyOnShutdown keeps the historical behavior: save only on graceful
	// shutdown.
	StrategyOnShutdown = "on-shutdown"
	// StrategyManual never saves automatically — only the admin snapshot
	// endpoint does. It does NOT save on shutdown either.
	StrategyManual = "manual"

	// DefaultPersistStrategy is the strategy used when --persist is on but no
	// strategy is chosen. Both entrypoints default to it, so they never drift.
	DefaultPersistStrategy = StrategyScheduled
	// DefaultPersistInterval is the scheduled ticker cadence when none is given.
	DefaultPersistInterval = 15 * time.Second

	// defaultOnRequestMaxWait bounds how long on-request will coalesce a
	// continuous write stream before forcing a save, so a sustained mutation
	// loop still flushes (a pure trailing-edge debounce never would).
	defaultOnRequestMaxWait = 1 * time.Second
	// defaultOnRequestDebounce is the quiet window on-request waits for after
	// the last mutation before saving, capped by defaultOnRequestMaxWait.
	defaultOnRequestDebounce = 50 * time.Millisecond
)

// saveFunc performs one whole-emulator snapshot to the state file. includeAssets
// controls whether object bodies are written. It is injected so serverkit owns
// the snapTargets locking and tests can substitute a deterministic save.
type saveFunc func(ctx context.Context, includeAssets bool) error

// flusher is the single owner of every automatic persistence save. It runs a
// background loop (scheduled ticker or on-request coalescer), guarantees at most
// one save is in flight at a time (single-flight), and performs the one
// deterministic final save on Stop so no earlier save can rename over it. A
// flusher exists only when --persist is set; when nil, every method call site
// short-circuits.
type flusher struct {
	strategy string
	interval time.Duration
	debounce time.Duration
	maxWait  time.Duration
	// includeAssets governs whether object bodies are written on EVERY save —
	// periodic, on-request, and the final shutdown save alike. It is
	// !PersistMetadataOnly, so the default persists bodies on every save (matching
	// LocalStack and the "crash-safe" expectation) and --persist-metadata-only is
	// the metadata-only opt-out for cost.
	includeAssets bool

	save      saveFunc
	out       io.Writer
	quiet     bool
	stateFile string

	// dirty is set by markDirty on each mutating request/admin op and cleared
	// atomically at the start of a save, so a mutation landing mid-save
	// re-dirties and is caught by the next tick/cycle.
	dirty atomic.Bool
	// signalCh wakes the on-request loop; buffered(1) with non-blocking sends so
	// markDirty never blocks a request.
	signalCh chan struct{}

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// newFlusher builds a flusher for the given config. All timing knobs get their
// defaults here; tests construct a flusher directly to shrink them.
func newFlusher(
	strategy string, interval time.Duration, includeAssets bool,
	save saveFunc, out io.Writer, quiet bool, stateFile string,
) *flusher {
	return &flusher{
		strategy:      strategy,
		interval:      interval,
		debounce:      defaultOnRequestDebounce,
		maxWait:       defaultOnRequestMaxWait,
		includeAssets: includeAssets,
		save:          save,
		out:           out,
		quiet:         quiet,
		stateFile:     stateFile,
		signalCh:      make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
	}
}

// markDirty records that state changed. It is safe to call from any request
// goroutine and never blocks.
func (f *flusher) markDirty() {
	if f == nil {
		return
	}

	f.dirty.Store(true)

	select {
	case f.signalCh <- struct{}{}:
	default:
	}
}

// Start launches the background loop for strategies that save while running.
// on-shutdown and manual have no loop — they act (or don't) only at Stop.
func (f *flusher) Start() {
	if f == nil {
		return
	}

	switch f.strategy {
	case StrategyScheduled:
		f.wg.Add(1)
		go f.scheduledLoop()
	case StrategyOnRequest:
		f.wg.Add(1)
		go f.onRequestLoop()
	}
}

// scheduledLoop saves on each tick when state is dirty. The dirty flag is
// cleared before the save so a concurrent mutation re-dirties for the next tick.
func (f *flusher) scheduledLoop() {
	defer f.wg.Done()

	t := time.NewTicker(f.interval)
	defer t.Stop()

	for {
		select {
		case <-f.stopCh:
			return
		case <-t.C:
			if f.dirty.Swap(false) {
				f.runSave()
			}
		}
	}
}

// onRequestLoop waits for a mutation signal, then coalesces the burst before
// saving. The single-flight save runs inside this goroutine, so a save slower
// than the arrival rate can never overlap itself.
func (f *flusher) onRequestLoop() {
	defer f.wg.Done()

	for {
		select {
		case <-f.stopCh:
			return
		case <-f.signalCh:
			if !f.coalesceAndSave() {
				return
			}
		}
	}
}

// coalesceAndSave, entered after a first mutation signal, waits for the write
// stream to go quiet (debounce) but no longer than maxWait, then performs one
// save. It returns false if Stop was requested mid-coalesce (loop should exit).
func (f *flusher) coalesceAndSave() bool {
	capT := time.NewTimer(f.maxWait)
	defer capT.Stop()

	deb := time.NewTimer(f.debounce)
	defer deb.Stop()

	for {
		select {
		case <-f.stopCh:
			return false
		case <-f.signalCh:
			// More mutations arrived: extend the quiet window, but never past the
			// hard cap.
			if !deb.Stop() {
				select {
				case <-deb.C:
				default:
				}
			}

			deb.Reset(f.debounce)
		case <-deb.C:
			f.dirty.Swap(false)
			f.runSave()

			return true
		case <-capT.C:
			f.dirty.Swap(false)
			f.runSave()

			return true
		}
	}
}

// runSave performs one periodic/on-request save. It honors the same
// includeAssets as the final shutdown save, so object bodies are crash-safe on
// every save by default (and dropped only under --persist-metadata-only). On
// failure it re-marks dirty so the next tick/cycle retries, and logs a warning.
func (f *flusher) runSave() {
	if err := f.save(context.Background(), f.includeAssets); err != nil {
		f.dirty.Store(true)
		fmt.Fprintf(f.out, "warning: failed to save state to %s: %v\n", f.stateFile, err)
	}
}

// Stop halts the background loop, waits for any in-flight save to finish, then
// performs the single deterministic final save (unless the strategy is manual).
// Because the loop has fully drained before this final save, no earlier save can
// rename over it and regress the on-disk snapshot to an older version.
func (f *flusher) Stop(ctx context.Context) {
	if f == nil {
		return
	}

	close(f.stopCh)
	f.wg.Wait()

	if f.strategy == StrategyManual {
		return
	}

	if err := f.save(ctx, f.includeAssets); err != nil {
		fmt.Fprintf(f.out, "warning: failed to save state to %s: %v\n", f.stateFile, err)

		return
	}

	if !f.quiet {
		fmt.Fprintf(f.out, "state saved to %s\n", f.stateFile)
	}
}
