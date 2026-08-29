package serverkit

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/persist"
)

// newPersistTestApp builds an admin-enabled, persisting App with a scheduled
// flusher whose ticker never fires during a unit test (huge interval), so the
// dirty flag can be observed directly without a save racing it.
func newPersistTestApp(t *testing.T, providers []string, ports map[string]string) *App {
	t.Helper()

	app := newTestApp(t, Config{
		Providers:       providers,
		Host:            "127.0.0.1",
		Ports:           ports,
		Admin:           true,
		Persist:         true,
		StateFile:       t.TempDir() + "/state.json",
		PersistStrategy: StrategyScheduled,
		PersistInterval: time.Hour,
		Out:             io.Discard,
	})

	return app
}

// TestDirtySeamProviderRequestMarksDirty asserts a cloud-provider request flips
// the dirty flag (the request-boundary seam that catches Get-then-mutate), while
// a health probe on the admin plane does NOT — so liveness checks never keep an
// idle emulator perpetually dirty.
func TestDirtySeamProviderRequestMarksDirty(t *testing.T) {
	app := newPersistTestApp(t, []string{"aws"}, map[string]string{"aws": "0"})

	if app.flusher.dirty.Load() {
		t.Fatal("dirty set at boot; want clean (restore does not flow through the request seam)")
	}

	// A provider request through the wrapped backend marks dirty.
	rec := httptest.NewRecorder()
	app.backends["aws"].ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !app.flusher.dirty.Load() {
		t.Fatal("a provider request did not mark state dirty")
	}

	// Clear, then a health probe must NOT re-dirty.
	app.flusher.dirty.Store(false)

	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("health probe status = %d, want 200", rec.Code)
	}

	if app.flusher.dirty.Load() {
		t.Fatal("a health probe marked state dirty; the admin/health plane must not")
	}
}

// TestDirtySeamIncludesKubernetes asserts the Kubernetes data-plane handler IS
// wrapped in the dirty seam: since #868 made the shared data plane part of the
// persisted surface, a pure-kubectl mutation (which never touches a provider
// port) must mark state dirty so scheduled/on-request saves capture it. The wrap
// sits inside the swapped backend, BEFORE the admin Control, so admin/health
// probes on the k8s port (answered by Control) still never dirty an idle server.
func TestDirtySeamIncludesKubernetes(t *testing.T) {
	app := newPersistTestApp(t, []string{"aws"}, map[string]string{"aws": "0", "k8s": "0"})

	if app.k8sBackend == nil {
		t.Fatal("precondition: k8s backend not built")
	}

	app.flusher.dirty.Store(false)

	rec := httptest.NewRecorder()
	app.k8sBackend.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/namespaces", nil))

	if !app.flusher.dirty.Load() {
		t.Fatal("a Kubernetes request did not mark state dirty; the k8s data plane must be in the dirty seam")
	}

	// A health probe fronted by the admin Control must still NOT dirty: Control
	// answers it before the wrapped backend is reached.
	app.flusher.dirty.Store(false)

	h := app.handlerFor(app.k8sBackend, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("k8s health probe status = %d, want 200", rec.Code)
	}

	if app.flusher.dirty.Load() {
		t.Fatal("a health probe on the k8s port marked state dirty; the admin/health plane must not")
	}
}

// TestDirtySeamAdminMutationsMarkDirty covers the three admin ops that bypass the
// request seam and must set dirty explicitly: reset, seed, and restore. Missing
// any means a mutate-then-crash on the admin surface loses the change.
func TestDirtySeamAdminMutationsMarkDirty(t *testing.T) {
	app := newPersistTestApp(t, []string{"aws"}, map[string]string{"aws": "0"})
	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))

	// reset
	app.flusher.dirty.Store(false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_cloudemu/reset", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200", rec.Code)
	}

	if !app.flusher.dirty.Load() {
		t.Fatal("reset did not mark state dirty")
	}

	// seed
	app.flusher.dirty.Store(false)
	fixture := `{"buckets":[{"name":"seeded"}]}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_cloudemu/seed", strings.NewReader(fixture)))

	if rec.Code != http.StatusOK {
		t.Fatalf("seed status = %d, want 200", rec.Code)
	}

	if !app.flusher.dirty.Load() {
		t.Fatal("seed did not mark state dirty")
	}

	// restore
	app.flusher.dirty.Store(false)
	snap := persist.Snapshot{SchemaVersion: persist.SchemaVersion}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}

	if err := app.restore(body); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if !app.flusher.dirty.Load() {
		t.Fatal("restore did not mark state dirty (restore-then-crash would lose the restored state)")
	}
}

// TestWrapDirtyMarksDirtyOnPanic asserts the request seam marks state dirty even
// when the handler panics AFTER mutating state. net/http recovers the connection
// and unwinds past wrapDirty, so a sequential mark (rather than a deferred one)
// would silently drop the mutation flag and a later crash would lose the change.
func TestWrapDirtyMarksDirtyOnPanic(t *testing.T) {
	app := newPersistTestApp(t, []string{"aws"}, map[string]string{"aws": "0"})
	app.flusher.dirty.Store(false)

	// A handler that mutates then panics.
	panicky := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom after mutation")
	})
	h := app.wrapDirty(panicky)

	// Stand in for net/http's per-request recover, so the panic doesn't fail the
	// test — the point is that the deferred markDirty still ran during the unwind.
	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	if !app.flusher.dirty.Load() {
		t.Fatal("wrapDirty did not mark state dirty when the handler panicked; the deferred markDirty is missing")
	}
}

// TestNormalizePersistDefaultsAndValidation locks the strategy defaults and the
// invalid-strategy rejection.
func TestNormalizePersistDefaultsAndValidation(t *testing.T) {
	// Off: untouched.
	off := &Config{Persist: false}
	if err := normalizePersist(off); err != nil {
		t.Fatalf("normalizePersist(off) = %v, want nil", err)
	}

	if off.PersistStrategy != "" {
		t.Fatalf("persist off should not default the strategy, got %q", off.PersistStrategy)
	}

	// On, unset: scheduled / 15s.
	on := &Config{Persist: true}
	if err := normalizePersist(on); err != nil {
		t.Fatalf("normalizePersist(on) = %v, want nil", err)
	}

	if on.PersistStrategy != StrategyScheduled {
		t.Fatalf("default strategy = %q, want %q", on.PersistStrategy, StrategyScheduled)
	}

	if on.PersistInterval != DefaultPersistInterval {
		t.Fatalf("default interval = %v, want %v", on.PersistInterval, DefaultPersistInterval)
	}

	// Negative interval defaults.
	neg := &Config{Persist: true, PersistStrategy: StrategyScheduled, PersistInterval: -1}
	if err := normalizePersist(neg); err != nil {
		t.Fatal(err)
	}

	if neg.PersistInterval != DefaultPersistInterval {
		t.Fatalf("negative interval not defaulted: %v", neg.PersistInterval)
	}

	// Invalid strategy rejected.
	bad := &Config{Persist: true, PersistStrategy: "bogus"}
	if err := normalizePersist(bad); !errors.Is(err, errUnknownStrategy) {
		t.Fatalf("normalizePersist(bogus) = %v, want errUnknownStrategy", err)
	}
}
