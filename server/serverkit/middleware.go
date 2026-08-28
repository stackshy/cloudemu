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
