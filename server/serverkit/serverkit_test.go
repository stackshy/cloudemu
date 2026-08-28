package serverkit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/persist"
)

// fakeCacheEngine is a no-op CacheEngine that also implements io.Closer, so a
// provider built with it registers it as an engine-closer. Its Close increments
// a shared counter, letting a test observe that serverkit closes the outgoing
// providers across a rebuild — the leak guard for the contrib real-engine case.
type fakeCacheEngine struct{ closed *atomic.Int64 }

func (fakeCacheEngine) Provision(context.Context, config.CacheProvisionRequest) (config.ProvisionResult, error) {
	return config.ProvisionResult{}, nil
}
func (fakeCacheEngine) Deprovision(context.Context, string) error { return nil }
func (f fakeCacheEngine) Close() error {
	f.closed.Add(1)
	return nil
}

func newTestApp(t *testing.T, cfg Config) *App {
	t.Helper()
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	app, err := New(&cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return app
}

// TestProviderClosedOnResetAndRestore is the leak guard: the outgoing provider's
// engines must be Close()d after a rebuild swap on BOTH /_cloudemu/reset and the
// snapshot restore path — not only on final shutdown. Without this, contrib's
// real embedded-postgres/Docker engines would leak on every reset.
func TestProviderClosedOnResetAndRestore(t *testing.T) {
	var closed atomic.Int64
	app := newTestApp(t, Config{
		Providers:   []string{"aws"},
		Host:        "127.0.0.1",
		Ports:       map[string]string{"aws": "0"},
		Admin:       true,
		BaseOptions: []config.Option{config.WithCacheEngine(fakeCacheEngine{closed: &closed})},
		Out:         io.Discard,
	})

	// New's initial build has no predecessor to close.
	if got := closed.Load(); got != 0 {
		t.Fatalf("after New: closed = %d, want 0 (no outgoing provider yet)", got)
	}

	// A reset swaps in a fresh provider and must close the one it replaced.
	app.Rebuild()
	if got := closed.Load(); got != 1 {
		t.Fatalf("after reset: closed = %d, want 1 (outgoing provider closed)", got)
	}

	// A snapshot restore also rebuilds-to-empty, so it too must close the
	// outgoing provider.
	snap := persist.Snapshot{SchemaVersion: persist.SchemaVersion}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.restore(body); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := closed.Load(); got != 2 {
		t.Fatalf("after restore: closed = %d, want 2 (reset + restore both closed)", got)
	}
}

// TestRestoreIsDestructive locks the documented semantic: restore wipes to empty
// before loading (no staging swap), so state present before the restore and
// absent from the posted snapshot is gone afterwards.
func TestRestoreIsDestructive(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, Config{
		Providers: []string{"aws"},
		Host:      "127.0.0.1",
		Ports:     map[string]string{"aws": "0"},
		Admin:     true,
		Out:       io.Discard,
	})

	// Seed a bucket into the running provider.
	if err := app.targets["aws"].Storage.CreateBucket(ctx, "pre-existing"); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	if b, _ := app.targets["aws"].Storage.ListBuckets(ctx); len(b) != 1 {
		t.Fatalf("precondition: want 1 bucket, got %d", len(b))
	}

	// Restore an empty (but schema-valid) snapshot: the wipe must remove the
	// pre-existing bucket.
	snap := persist.Snapshot{SchemaVersion: persist.SchemaVersion}
	body, _ := json.Marshal(snap)
	if err := app.restore(body); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// After the destructive restore the store is empty, and targets point at the
	// freshly-rebuilt provider.
	buckets, err := app.targets["aws"].Storage.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("list after restore: %v", err)
	}
	if len(buckets) != 0 {
		t.Fatalf("after destructive restore: %d buckets, want 0 (wiped)", len(buckets))
	}
}

// TestSnapshotOnShutdownBeforeClose asserts the shutdown ordering: the
// persistence snapshot is written while the providers are still live, and the
// providers are closed only afterwards. A snapshot that captured the seeded
// bucket AND a provider Close that fired both being true proves the snapshot ran
// before teardown (engines readable at snapshot time).
func TestSnapshotOnShutdownBeforeClose(t *testing.T) {
	ctx := context.Background()
	stateFile := filepath.Join(t.TempDir(), "state.json")

	var closed atomic.Int64
	app := newTestApp(t, Config{
		Providers:   []string{"aws"},
		Host:        "127.0.0.1",
		Ports:       map[string]string{"aws": "0"},
		Admin:       true,
		Persist:     true,
		StateFile:   stateFile,
		BaseOptions: []config.Option{config.WithCacheEngine(fakeCacheEngine{closed: &closed})},
		Out:         io.Discard,
	})

	if err := app.targets["aws"].Storage.CreateBucket(ctx, "keep-me"); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}

	// Serve until the context is cancelled, then it shuts down (snapshot, then
	// close).
	sctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Serve(sctx); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	// The snapshot was written and holds the seeded bucket — so it ran while the
	// provider was still live.
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if !strings.Contains(string(data), "keep-me") {
		t.Fatalf("snapshot missing seeded bucket; state=%s", data)
	}
	// And the provider was closed on shutdown.
	if got := closed.Load(); got != 1 {
		t.Fatalf("provider closed %d times on shutdown, want 1", got)
	}
}

// TestDangerWarning covers the non-loopback admin warning gate.
func TestDangerWarning(t *testing.T) {
	if w := dangerWarning(&Config{Admin: true, Host: "0.0.0.0"}); w == "" {
		t.Fatal("non-loopback admin host: want a warning, got none")
	}
	if w := dangerWarning(&Config{Admin: true, Host: "127.0.0.1"}); w != "" {
		t.Fatalf("loopback admin host: want no warning, got %q", w)
	}
	if w := dangerWarning(&Config{Admin: false, Host: "0.0.0.0"}); w != "" {
		t.Fatalf("admin off: want no warning, got %q", w)
	}
}

// TestSeedNilReturns501 checks the admin wiring: a backend fronted without a
// seed function (as the Kubernetes listener is) answers /_cloudemu/seed with 501.
func TestSeedNilReturns501(t *testing.T) {
	app := newTestApp(t, Config{
		Providers: []string{"aws"},
		Host:      "127.0.0.1",
		Ports:     map[string]string{"aws": "0"},
		Admin:     true,
		Out:       io.Discard,
	})

	h := app.handlerFor(app.backends["aws"], nil) // nil seedFn, like the k8s port
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_cloudemu/seed", strings.NewReader("{}")))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("seed with nil seedFn = %d, want 501", rec.Code)
	}
}

// TestSeedBodyCapReturns413 checks the 32 MiB fixture cap is enforced through the
// assembled control plane.
func TestSeedBodyCapReturns413(t *testing.T) {
	app := newTestApp(t, Config{
		Providers: []string{"aws"},
		Host:      "127.0.0.1",
		Ports:     map[string]string{"aws": "0"},
		Admin:     true,
		Out:       io.Discard,
	})

	const overCap = (32 << 20) + 1 // one byte past the 32 MiB cap
	body := bytes.NewReader(make([]byte, overCap))

	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_cloudemu/seed", body))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized fixture = %d, want 413", rec.Code)
	}
}
