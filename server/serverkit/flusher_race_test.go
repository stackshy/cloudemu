package serverkit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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
