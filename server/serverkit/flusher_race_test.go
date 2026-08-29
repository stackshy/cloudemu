package serverkit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// recordingCacheEngine is a no-op CacheEngine whose Close() flags a violation if
// it fires while an export is in progress. It lets TestFlusherSaveGuardsClose
// prove closeProviders never tears down a (real, contrib) engine mid-export.
type recordingCacheEngine struct {
	exporting *atomic.Bool
	violation *atomic.Bool
}

func (recordingCacheEngine) Provision(context.Context, config.CacheProvisionRequest) (config.ProvisionResult, error) {
	return config.ProvisionResult{}, nil
}
func (recordingCacheEngine) Deprovision(context.Context, string) error { return nil }
func (e recordingCacheEngine) Close() error {
	if e.exporting.Load() {
		e.violation.Store(true)
	}

	return nil
}

// TestFlusherSaveGuardsClose runs the flusher save (which reads the providers)
// concurrently with Rebuild()/reset (which Close()es the outgoing providers) and
// asserts a provider is never Close()d while an export is still reading it. The
// override save mirrors the production save's exportMu.RLock guard, and the
// assertion proves the production closeProviders honors that write-lock: with the
// guard removed, Close would overlap the in-flight export and set violation.
// Run under -race it also guards the shared-state accesses.
func TestFlusherSaveGuardsClose(t *testing.T) {
	var exporting, violation atomic.Bool

	eng := recordingCacheEngine{exporting: &exporting, violation: &violation}
	app := newTestApp(t, Config{
		Providers:       []string{"aws"},
		Host:            "127.0.0.1",
		Ports:           map[string]string{"aws": "0"},
		Admin:           true,
		Persist:         true,
		StateFile:       filepath.Join(t.TempDir(), "state.json"),
		PersistStrategy: StrategyScheduled,
		PersistInterval: time.Hour, // we drive runSave by hand
		BaseOptions:     []config.Option{config.WithCacheEngine(eng)},
		Out:             io.Discard,
	})

	// Replace the save body with one that holds the SAME production export
	// read-guard (exportMu) and marks an export in-flight for its duration, so the
	// engine's Close can observe an overlap the guard must prevent.
	app.flusher.save = func(_ context.Context, _ bool) error {
		app.exportMu.RLock()
		defer app.exportMu.RUnlock()

		exporting.Store(true)
		time.Sleep(time.Millisecond)
		exporting.Store(false)

		return nil
	}

	done := make(chan struct{})

	var wg sync.WaitGroup

	// Concurrent resets: each swaps in a fresh provider and Close()es the outgoing
	// one (under exportMu.Lock).
	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				app.Rebuild()
			}
		}
	}()

	// Concurrent saves holding the export read-guard.
	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				app.flusher.runSave()
			}
		}
	}()

	time.Sleep(150 * time.Millisecond)
	close(done)
	wg.Wait()

	if violation.Load() {
		t.Fatal("a provider engine Close() ran while an export was in flight; closeProviders did not wait on exportMu")
	}
}

// TestFlusherResetRace runs the scheduled flusher (which reads a.snapTargets
// under rebuildMu) concurrently with /_cloudemu/reset (which reassigns
// a.snapTargets under the same lock) and with provider requests marking dirty.
// Under `go test -race` it is the guard against an unsynchronized snapTargets
// read during a save racing a swap.
func TestFlusherResetRace(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")
	app := newTestApp(t, Config{
		Providers:       []string{"aws"},
		Host:            "127.0.0.1",
		Ports:           map[string]string{"aws": "0"},
		Admin:           true,
		Persist:         true,
		StateFile:       stateFile,
		PersistStrategy: StrategyScheduled,
		PersistInterval: time.Millisecond, // hammer the save path
		Out:             io.Discard,
	})

	// Drive the flusher directly (Serve would bind listeners we don't need here).
	app.flusher.interval = time.Millisecond
	app.flusher.Start()

	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))

	done := make(chan struct{})

	var wg sync.WaitGroup

	// Concurrent resets.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_cloudemu/reset", nil))
			}
		}
	}()

	// Concurrent provider requests marking dirty.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				rec := httptest.NewRecorder()
				app.backends["aws"].ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			}
		}
	}()

	time.Sleep(120 * time.Millisecond)
	close(done)
	wg.Wait()

	app.flusher.Stop(context.Background())
}
