package admin_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/admin"
)

func handler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
}

func TestBackendSwap(t *testing.T) {
	b := admin.NewBackend(handler("first"))

	if got := do(t, b, http.MethodGet, "/"); got != "first" {
		t.Fatalf("before swap = %q, want first", got)
	}

	b.Swap(handler("second"))
	if got := do(t, b, http.MethodGet, "/"); got != "second" {
		t.Fatalf("after swap = %q, want second", got)
	}
}

// TestBackendConcurrentSwap exercises the hot-swap under the race detector:
// many readers serving while a writer swaps the handler must never race.
func TestBackendConcurrentSwap(t *testing.T) {
	b := admin.NewBackend(handler("a"))
	done := make(chan struct{})
	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = do(t, b, http.MethodGet, "/")
				}
			}
		}()
	}
	for i := range 200 {
		b.Swap(handler(string(rune('a' + i%26))))
	}
	close(done)
	wg.Wait()
}

func TestControlReset(t *testing.T) {
	resets := 0
	b := admin.NewBackend(handler("backend"))
	c := admin.NewControl(b, func() { resets++; b.Swap(handler("rebuilt")) })

	// Non-control paths pass through to the backend.
	if got := do(t, c, http.MethodGet, "/some/aws/request"); got != "backend" {
		t.Fatalf("passthrough = %q, want backend", got)
	}

	// POST /_cloudemu/reset runs the reset and swaps the backend.
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, admin.Prefix+"reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200", rec.Code)
	}
	if resets != 1 {
		t.Fatalf("reset ran %d times, want 1", resets)
	}
	if got := do(t, c, http.MethodGet, "/some/aws/request"); got != "rebuilt" {
		t.Fatalf("after reset the backend = %q, want rebuilt", got)
	}
}

func TestControlRoutes(t *testing.T) {
	b := admin.NewBackend(handler("backend"))
	c := admin.NewControl(b, func() {})

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, admin.Prefix + "reset", http.StatusMethodNotAllowed}, // reset is POST-only
		{http.MethodGet, admin.Prefix + "health", http.StatusOK},
		{http.MethodPost, admin.Prefix + "seed", http.StatusNotImplemented}, // #250
		{http.MethodGet, admin.Prefix + "bogus", http.StatusNotFound},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func do(t *testing.T, h http.Handler, method, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Body.String()
}
