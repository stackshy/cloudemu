package serverkit

import (
	"log"
	"net/http"
	"time"
)

// wrap optionally decorates a provider handler with request logging. When
// logging is off it returns the handler unchanged, so there is zero overhead on
// the hot path.
func wrap(h http.Handler, provider string, logReqs bool) http.Handler {
	if !logReqs {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(sw, r)
		// The request method/path are the intended content of a local dev access
		// log, not an untrusted sink — this is opt-in via --log-requests.
		//nolint:gosec // G706: local dev request log of the request line
		log.Printf("[%s] %s %s → %d (%s)", provider, r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Microsecond))
	})
}

// wrapDirty marks the emulator state dirty after a request returns, so the
// flusher persists it. It is applied to the four cloud provider handlers
// (aws/azure/gcp/oci) AND — since #868 made the shared Kubernetes data plane part
// of the persisted surface — the Kubernetes data-plane handler, so a pure-kubectl
// mutation that never touches a provider port is still saved. It is applied at
// backend-swap time (swapFresh), BEFORE the admin Control fronts the backend, so
// the admin/health plane never reaches it and liveness probes don't keep an idle
// emulator perpetually dirty. When persistence is off the handler is returned
// unchanged (zero overhead). Marking after the handler runs (rather than
// classifying reads vs writes) is deliberately coarse: a pure read triggers at
// most one extra save per interval/cap, which is bounded and acceptable — the
// Kubernetes port's chatty reflector list/watch traffic makes this coarseness
// more visible under on-request, but it stays bounded by the debounce cap.
func (a *App) wrapDirty(h http.Handler) http.Handler {
	if !a.cfg.Persist {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// defer, not sequential: a handler that mutates state and then panics
		// (net/http recovers the connection but unwinds past this point) must still
		// mark the mutation dirty, or a crash after the panic would silently lose it.
		defer a.markDirty()
		h.ServeHTTP(w, r)
	})
}

// wrapLatency delays every emulated wire request by the configured --latency
// duration, so the standalone/Docker path honors the same artificial-latency
// simulation the typed library's portable layer applies per op. It is applied
// inside the backend swap chain (like wrapDirty), BEFORE the admin Control fronts
// the backend, so the /_cloudemu control plane (health/reset/snapshot/...) is
// never delayed — latency simulation is for the emulated cloud APIs, not the
// control plane. When latency is unset (0) the handler is returned unchanged, so
// there is zero overhead on the hot path. The sleep is a real per-request
// time.Sleep — that is the point of latency simulation — and honors request
// cancellation so a client that gives up doesn't pin the goroutine for the full
// delay.
func (a *App) wrapLatency(h http.Handler) http.Handler {
	d := a.cfg.Latency
	if d <= 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
		}

		h.ServeHTTP(w, r)
	})
}

// statusWriter captures the first response status for logging while remaining a
// transparent http.ResponseWriter. Write is not overridden — it promotes from
// the embedded writer, so a handler that writes a body without an explicit
// WriteHeader is logged as the default 200.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}

	w.ResponseWriter.WriteHeader(code)
}
