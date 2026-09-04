package lro_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
)

const opPath = "/v1/projects/p/locations/us/operations/op-1"
const cancelPath = opPath + ":cancel"

func get(t *testing.T, h *lro.Handler, path string) (int, string) {
	t.Helper()
	return do(t, h, http.MethodGet, path)
}

func do(t *testing.T, h *lro.Handler, method, path string) (int, string) {
	t.Helper()

	r := httptest.NewRequest(method, path, nil)
	if !h.Matches(r) {
		t.Fatalf("handler does not match %s %s", method, path)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	return w.Code, w.Body.String()
}

// TestRegisteredOperationReturnsResponse verifies a registered operation
// resolves to done with its typed response echoed back.
func TestRegisteredOperationReturnsResponse(t *testing.T) {
	reg := lro.NewRegistry()
	reg.Register("projects/p/locations/us/operations/op-1",
		map[string]any{"name": "widget-1", "state": "READY"})

	code, body := get(t, lro.New(reg), opPath)
	if code != http.StatusOK {
		t.Fatalf("code=%d body=%s (want 200)", code, body)
	}

	for _, want := range []string{`"done":true`, `"response"`, "widget-1", "READY"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %s missing %q", body, want)
		}
	}
}

// TestUnknownOperationIs404 verifies an operation name that was never registered
// is 404 NOT_FOUND rather than masked as done.
func TestUnknownOperationIs404(t *testing.T) {
	code, body := get(t, lro.New(lro.NewRegistry()), opPath)
	if code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s (want 404)", code, body)
	}

	if strings.Contains(body, `"done":true`) {
		t.Fatalf("unknown op should not report done: %s", body)
	}
}

// TestNilRegistryLegacyDone verifies a handler built without a registry keeps
// the legacy always-done behaviour used by standalone package servers.
func TestNilRegistryLegacyDone(t *testing.T) {
	code, body := get(t, lro.New(nil), opPath)
	if code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("code=%d body=%s (want 200 done:true)", code, body)
	}
}

// TestUnknownOperationCancelIs404 verifies POST …:cancel on an operation name
// that was never registered is 404 NOT_FOUND, not a fabricated success — the
// cross-cutting bug this package closes: a bogus operation must 404 on every
// verb, not just GET.
func TestUnknownOperationCancelIs404(t *testing.T) {
	code, body := do(t, lro.New(lro.NewRegistry()), http.MethodPost, cancelPath)
	if code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s (want 404)", code, body)
	}
}

// TestUnknownOperationDeleteIs404 verifies DELETE on an operation name that
// was never registered is 404 NOT_FOUND, not a fabricated success.
func TestUnknownOperationDeleteIs404(t *testing.T) {
	code, body := do(t, lro.New(lro.NewRegistry()), http.MethodDelete, opPath)
	if code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s (want 404)", code, body)
	}
}

// TestCancelRegisteredOperationMarksCanceled verifies Cancel on a real,
// registered operation succeeds and a subsequent Get reports it canceled.
func TestCancelRegisteredOperationMarksCanceled(t *testing.T) {
	reg := lro.NewRegistry()
	reg.Register("projects/p/locations/us/operations/op-1", map[string]any{"ok": true})
	h := lro.New(reg)

	code, body := do(t, h, http.MethodPost, cancelPath)
	if code != http.StatusOK {
		t.Fatalf("cancel: code=%d body=%s (want 200)", code, body)
	}

	code, body = get(t, h, opPath)
	if code != http.StatusOK {
		t.Fatalf("post-cancel get: code=%d body=%s (want 200)", code, body)
	}

	for _, want := range []string{`"done":true`, `"error"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("post-cancel get body %s missing %q", body, want)
		}
	}

	if strings.Contains(body, `"response"`) {
		t.Fatalf("canceled operation should not carry a response: %s", body)
	}
}

// TestDeleteRegisteredOperationRemovesIt verifies Delete on a real, registered
// operation succeeds and a subsequent Get 404s, matching real GCP's
// Operations.Delete semantics.
func TestDeleteRegisteredOperationRemovesIt(t *testing.T) {
	reg := lro.NewRegistry()
	reg.Register("projects/p/locations/us/operations/op-1", map[string]any{"ok": true})
	h := lro.New(reg)

	code, body := do(t, h, http.MethodDelete, opPath)
	if code != http.StatusOK {
		t.Fatalf("delete: code=%d body=%s (want 200)", code, body)
	}

	code, body = get(t, h, opPath)
	if code != http.StatusNotFound {
		t.Fatalf("post-delete get: code=%d body=%s (want 404)", code, body)
	}
}

// TestNilRegistryLegacyCancelAndDelete verifies a handler built without a
// registry keeps unconditional success on cancel/delete too (standalone
// package servers), matching its GET legacy fallback.
func TestNilRegistryLegacyCancelAndDelete(t *testing.T) {
	h := lro.New(nil)

	if code, body := do(t, h, http.MethodPost, cancelPath); code != http.StatusOK {
		t.Fatalf("cancel: code=%d body=%s (want 200)", code, body)
	}

	if code, body := do(t, h, http.MethodDelete, opPath); code != http.StatusOK {
		t.Fatalf("delete: code=%d body=%s (want 200)", code, body)
	}
}

// TestRegistryConcurrentAccess exercises concurrent registration and polling so
// the race detector guards the registry's mutex.
func TestRegistryConcurrentAccess(t *testing.T) {
	reg := lro.NewRegistry()
	h := lro.New(reg)

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			reg.Register("projects/p/locations/us/operations/op-1", map[string]any{"ok": true})
		}()

		go func() {
			defer wg.Done()

			r := httptest.NewRequest(http.MethodGet, opPath, nil)
			h.ServeHTTP(httptest.NewRecorder(), r)
		}()
	}

	wg.Wait()
}
