package admin_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	c := admin.NewControl(b, func() { resets++; b.Swap(handler("rebuilt")) }, nil, nil, nil, nil)

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
	c := admin.NewControl(b, func() {}, nil, nil, nil, nil) // nil seed → seed endpoint disabled

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, admin.Prefix + "reset", http.StatusMethodNotAllowed}, // reset is POST-only
		{http.MethodGet, admin.Prefix + "health", http.StatusOK},              //
		{http.MethodGet, admin.Prefix + "seed", http.StatusMethodNotAllowed},  // seed is POST-only
		{http.MethodPost, admin.Prefix + "seed", http.StatusNotImplemented},   // nil seed → 501
		{http.MethodGet, admin.Prefix + "bogus", http.StatusNotFound},         //
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestControlSeed(t *testing.T) {
	var gotFixture string
	b := admin.NewBackend(handler("backend"))
	c := admin.NewControl(b, func() {}, func(fixture []byte) (int, error) {
		gotFixture = string(fixture)
		return 4, nil
	}, nil, nil, nil)

	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, admin.Prefix+"seed", strings.NewReader(`{"buckets":[]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status = %d, want 200", rec.Code)
	}
	if gotFixture != `{"buckets":[]}` {
		t.Fatalf("seed received fixture %q", gotFixture)
	}
	if !strings.Contains(rec.Body.String(), `"applied":4`) {
		t.Fatalf("seed body = %s, want applied:4", rec.Body.String())
	}

	// A seeder error surfaces as 400.
	cErr := admin.NewControl(b, func() {}, func([]byte) (int, error) {
		return 0, errFixture
	}, nil, nil, nil)
	rec = httptest.NewRecorder()
	cErr.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, admin.Prefix+"seed", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("seed error status = %d, want 400", rec.Code)
	}
}

func TestControlSnapshot(t *testing.T) {
	b := admin.NewBackend(handler("backend"))

	var restored string
	c := admin.NewControl(b, func() {}, nil,
		func() ([]byte, error) { return []byte(`{"schemaVersion":1}`), nil },
		func(body []byte) error { restored = string(body); return nil },
		nil,
	)

	// GET returns the snapshot bytes verbatim.
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, admin.Prefix+"snapshot", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"schemaVersion":1}` {
		t.Fatalf("GET snapshot = %d %q", rec.Code, rec.Body.String())
	}

	// POST hands the body to restore.
	rec = httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, admin.Prefix+"snapshot", strings.NewReader(`{"schemaVersion":1}`)))
	if rec.Code != http.StatusOK || restored != `{"schemaVersion":1}` {
		t.Fatalf("POST snapshot = %d, restored %q", rec.Code, restored)
	}

	// With nil snapshot/restore the endpoint is disabled (501).
	off := admin.NewControl(b, func() {}, nil, nil, nil, nil)
	rec = httptest.NewRecorder()
	off.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, admin.Prefix+"snapshot", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("disabled snapshot = %d, want 501", rec.Code)
	}
}

func TestControlExtraHandler(t *testing.T) {
	b := admin.NewBackend(handler("backend"))

	extra := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("extra:" + r.URL.Path))
	})
	c := admin.NewControl(b, func() {}, nil, nil, nil, extra)

	// An unknown control path is delegated to the extra handler.
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, admin.Prefix+"net/can-connect", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "net/can-connect") {
		t.Fatalf("extra delegation = %d %q", rec.Code, rec.Body.String())
	}

	// With no extra handler, an unknown control path is still a 404.
	c2 := admin.NewControl(b, func() {}, nil, nil, nil, nil)
	rec = httptest.NewRecorder()
	c2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, admin.Prefix+"unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nil extra unknown path = %d, want 404", rec.Code)
	}
}

var errFixture = fmt.Errorf("bad fixture")

func do(t *testing.T, h http.Handler, method, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Body.String()
}
