package serverkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// timeTravelApp builds an admin-enabled single-provider App and returns its
// control-plane handler.
func timeTravelApp(t *testing.T) (*App, http.Handler) {
	t.Helper()

	app := newTestApp(t, Config{
		Providers: []string{"aws"},
		Host:      "127.0.0.1",
		Ports:     map[string]string{"aws": "0"},
		Admin:     true,
		Out:       io.Discard,
	})

	return app, app.handlerFor(app.backends["aws"], app.seedFor("aws"))
}

func ttDo(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, r))

	return rec
}

// TestTimeTravelServeRewind drives the named-snapshot registry over the real
// admin HTTP surface: seed a bucket, save a named point, reset (wipe), then
// rewind — the bucket comes back.
func TestTimeTravelServeRewind(t *testing.T) {
	_, h := timeTravelApp(t)

	if rec := ttDo(t, h, http.MethodPost, "/_cloudemu/seed", `{"buckets":[{"name":"b-seed"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("seed = %d %q", rec.Code, rec.Body.String())
	}

	if rec := ttDo(t, h, http.MethodPost, "/_cloudemu/snapshot/v1", ""); rec.Code != http.StatusOK {
		t.Fatalf("save v1 = %d %q", rec.Code, rec.Body.String())
	}

	if rec := ttDo(t, h, http.MethodPost, "/_cloudemu/reset", ""); rec.Code != http.StatusOK {
		t.Fatalf("reset = %d", rec.Code)
	}

	// After reset the export must not carry the bucket.
	if rec := ttDo(t, h, http.MethodGet, "/_cloudemu/snapshot", ""); strings.Contains(rec.Body.String(), "b-seed") {
		t.Fatalf("bucket still present after reset: %s", rec.Body.String())
	}

	if rec := ttDo(t, h, http.MethodPost, "/_cloudemu/snapshot/v1/rewind", ""); rec.Code != http.StatusOK {
		t.Fatalf("rewind = %d %q", rec.Code, rec.Body.String())
	}

	if rec := ttDo(t, h, http.MethodGet, "/_cloudemu/snapshot", ""); !strings.Contains(rec.Body.String(), "b-seed") {
		t.Fatalf("bucket not restored by rewind: %s", rec.Body.String())
	}
}

// TestTimeTravelServeListForkErrors exercises list, fork, and the error paths.
func TestTimeTravelServeListForkErrors(t *testing.T) {
	_, h := timeTravelApp(t)

	if rec := ttDo(t, h, http.MethodPost, "/_cloudemu/snapshot/base", ""); rec.Code != http.StatusOK {
		t.Fatalf("save base = %d %q", rec.Code, rec.Body.String())
	}

	if rec := ttDo(t, h, http.MethodPost, "/_cloudemu/snapshot/base/fork/branch", ""); rec.Code != http.StatusOK {
		t.Fatalf("fork = %d %q", rec.Code, rec.Body.String())
	}

	rec := ttDo(t, h, http.MethodGet, "/_cloudemu/snapshot/", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "base") || !strings.Contains(rec.Body.String(), "branch") {
		t.Fatalf("list = %d %q, want base+branch", rec.Code, rec.Body.String())
	}

	// Rewind a missing snapshot -> 404.
	if rec := ttDo(t, h, http.MethodPost, "/_cloudemu/snapshot/missing/rewind", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("rewind missing = %d, want 404", rec.Code)
	}

	// Fork onto an existing name -> 409.
	if rec := ttDo(t, h, http.MethodPost, "/_cloudemu/snapshot/base/fork/branch", ""); rec.Code != http.StatusConflict {
		t.Fatalf("fork onto existing = %d, want 409", rec.Code)
	}

	// Wrong method on save path -> 405.
	if rec := ttDo(t, h, http.MethodPut, "/_cloudemu/snapshot/base", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT save = %d, want 405", rec.Code)
	}

	// Delete then confirm it's gone from the list.
	if rec := ttDo(t, h, http.MethodDelete, "/_cloudemu/snapshot/branch", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete branch = %d %q", rec.Code, rec.Body.String())
	}

	if rec := ttDo(t, h, http.MethodGet, "/_cloudemu/snapshot/", ""); strings.Contains(rec.Body.String(), "branch") {
		t.Fatalf("branch still listed after delete: %s", rec.Body.String())
	}
}
