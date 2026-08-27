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

func get(t *testing.T, h *lro.Handler, path string) (int, string) {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, path, nil)
	if !h.Matches(r) {
		t.Fatalf("handler does not match %s", path)
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
