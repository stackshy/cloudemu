// Package admin serves the cloudemu control plane at /_cloudemu/*, in front of
// a hot-swappable backend handler.
//
// It exists for the standalone server (cmd/cloudemu): out-of-process or
// parallel test suites share one long-lived process, so they need a way to get
// a clean slate between tests without restarting it. A POST to
// /_cloudemu/reset runs a caller-supplied reset — which rebuilds every provider
// backend to empty state and swaps it in atomically — so in-flight requests
// finish against the old state while new requests see the fresh one.
//
// State lifecycle only lives here; the wire handlers and the core server.Server
// are untouched. In-process users don't need this — they just construct a fresh
// provider.
package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Prefix is the reserved path space for the control plane. No emulated cloud
// API uses it, so it never collides with a real SDK request.
const Prefix = "/_cloudemu/"

// maxFixtureBytes caps a seed request body.
const maxFixtureBytes = 32 << 20 // 32 MiB

// maxSnapshotBytes caps a restore request body. Snapshots can carry object
// bodies, so this is larger than a seed fixture.
const maxSnapshotBytes = 512 << 20 // 512 MiB

// Backend is a hot-swappable http.Handler. Requests read the current handler
// under a read lock; Swap replaces it under a write lock. A zero Backend is not
// usable — construct with NewBackend.
type Backend struct {
	mu sync.RWMutex
	h  http.Handler
}

// NewBackend wraps an initial handler.
func NewBackend(h http.Handler) *Backend {
	return &Backend{h: h}
}

// Swap atomically replaces the current handler. In-flight requests already
// dispatched to the old handler are unaffected.
func (b *Backend) Swap(h http.Handler) {
	b.mu.Lock()
	b.h = h
	b.mu.Unlock()
}

// ServeHTTP dispatches to the current handler.
func (b *Backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.RLock()
	h := b.h
	b.mu.RUnlock()
	h.ServeHTTP(w, r)
}

// Control fronts a Backend with the /_cloudemu control plane. Requests outside
// the control prefix pass through to the backend unchanged. reset is shared
// across every provider's Control so a single call rebuilds the whole emulator.
// seed applies a fixture body to this provider's drivers and returns how many
// top-level resources it created; a nil seed disables the seed endpoint (501).
type Control struct {
	backend  *Backend
	reset    func()
	seed     func(fixture []byte) (int, error)
	snapshot func() ([]byte, error)
	restore  func(snapshot []byte) error
}

// NewControl wraps backend with the control plane. reset must rebuild every
// backend (including this one) to a clean state. seed, snapshot, and restore
// may each be nil, which disables the corresponding endpoint. snapshot returns
// the whole-emulator state as JSON; restore replaces it from that JSON.
func NewControl(
	backend *Backend,
	reset func(),
	seed func(fixture []byte) (int, error),
	snapshot func() ([]byte, error),
	restore func(snapshot []byte) error,
) *Control {
	return &Control{backend: backend, reset: reset, seed: seed, snapshot: snapshot, restore: restore}
}

// ServeHTTP routes control-plane paths to the control handler and everything
// else to the wrapped backend.
func (c *Control) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, Prefix) {
		c.serveControl(w, r)
		return
	}
	c.backend.ServeHTTP(w, r)
}

func (c *Control) serveControl(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, Prefix) {
	case "reset":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "reset requires POST"})
			return
		}
		c.reset()
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	case "health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case "seed":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "seed requires POST"})
			return
		}
		if c.seed == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "seeding is not available on this server"})
			return
		}
		// Read one byte past the cap so an over-limit body is a clear 413
		// rather than a silently-truncated body that fails JSON parsing.
		fixture, err := io.ReadAll(io.LimitReader(r.Body, maxFixtureBytes+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read fixture: " + err.Error()})
			return
		}
		if len(fixture) > maxFixtureBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "fixture exceeds 32 MiB"})
			return
		}
		applied, err := c.seed(fixture)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "seeded", "applied": applied})
	case "snapshot":
		c.serveSnapshot(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown control endpoint"})
	}
}

// serveSnapshot handles GET /_cloudemu/snapshot (export the whole-emulator state
// as JSON) and POST /_cloudemu/snapshot (replace it from the posted JSON). Both
// act on every provider, like reset, so a call to any provider port covers the
// whole emulator.
func (c *Control) serveSnapshot(w http.ResponseWriter, r *http.Request) {
	if c.snapshot == nil || c.restore == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "snapshots are not available on this server"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		data, err := c.snapshot()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, maxSnapshotBytes+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read snapshot: " + err.Error()})
			return
		}

		if len(body) > maxSnapshotBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "snapshot exceeds 512 MiB"})
			return
		}

		if err := c.restore(body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "snapshot requires GET or POST"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
