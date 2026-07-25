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
	"net/http"
	"strings"
	"sync"
)

// Prefix is the reserved path space for the control plane. No emulated cloud
// API uses it, so it never collides with a real SDK request.
const Prefix = "/_cloudemu/"

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
type Control struct {
	backend *Backend
	reset   func()
}

// NewControl wraps backend with the control plane. reset must rebuild every
// backend (including this one) to a clean state.
func NewControl(backend *Backend, reset func()) *Control {
	return &Control{backend: backend, reset: reset}
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
		// Seeding needs the cross-service fixture loader tracked in #250.
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "seed is not implemented yet (tracked in #250); create resources with your SDK client after reset",
		})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown control endpoint"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
