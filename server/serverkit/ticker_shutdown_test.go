package serverkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// TestTickerStopsBeforeFinalSave checks shutdown order. A tick that changes
// state must never run after the final persistence save, or that change is
// lost. Each tick here creates a bucket, so every bucket in memory must be in
// the saved state file, and no tick may run once Serve returns.
func TestTickerStopsBeforeFinalSave(t *testing.T) {
	base := runtime.NumGoroutine()
	stateFile := filepath.Join(t.TempDir(), "state.json")

	app := newTestApp(t, Config{
		Providers:       []string{"aws"},
		Host:            "127.0.0.1",
		Ports:           map[string]string{"aws": "0"},
		Persist:         true,
		StateFile:       stateFile,
		PersistInterval: time.Hour, // only the final save runs
		Out:             io.Discard,
	})

	storage := app.targets["aws"].Storage

	var ticks atomic.Int64

	app.ticker.add(time.Millisecond, func() []config.Tickable {
		return []config.Tickable{tickFunc(func(time.Time) bool {
			n := ticks.Add(1)

			return storage.CreateBucket(context.Background(), fmt.Sprintf("tick-%d", n)) == nil
		})}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx) }()

	waitTick(t, "ticks", func() bool { return ticks.Load() >= 5 })
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}

	frozen := ticks.Load()

	time.Sleep(20 * time.Millisecond)

	if got := ticks.Load(); got != frozen {
		t.Fatalf("ticked after shutdown: %d -> %d", frozen, got)
	}

	raw, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	buckets, err := storage.ListBuckets(context.Background())
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}

	for _, b := range buckets {
		if !strings.Contains(string(raw), `"`+b.Name+`"`) {
			t.Fatalf("bucket %s is in memory but not in the final save (%d buckets)", b.Name, len(buckets))
		}
	}

	settledGoroutines(t, base)
}
