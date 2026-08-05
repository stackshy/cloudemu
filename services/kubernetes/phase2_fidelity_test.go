// Tests for the Phase 2 core-semantics work: deterministic clock, list
// pagination (limit/continue), and finalizer-gated deletion on the registry
// and typed paths.

package kubernetes_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// newFixtureWithClock is newFixture with a caller-supplied clock wired in
// before the cluster is registered, so every timestamp is deterministic.
func newFixtureWithClock(t *testing.T, clock config.Clock) (string, func()) {
	t.Helper()

	api := kubernetes.NewAPIServer()
	api.SetClock(clock)
	uid, _ := api.RegisterCluster()
	ts := httptest.NewServer(api)
	api.SetBaseURL(ts.URL)

	return ts.URL + "/k8s/" + uid, ts.Close
}

func TestDeterministicClock_CreationTimestamp(t *testing.T) {
	fixed := time.Date(2021, 6, 15, 8, 30, 0, 0, time.UTC)
	base, done := newFixtureWithClock(t, config.NewFakeClock(fixed))
	defer done()

	body := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "stamped"},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps", body)
	defer resp.Body.Close()

	obj := decodeMap(t, resp.Body)
	meta, _ := obj["metadata"].(map[string]any)

	if got := meta["creationTimestamp"]; got != "2021-06-15T08:30:00Z" {
		t.Fatalf("creationTimestamp = %v, want deterministic 2021-06-15T08:30:00Z", got)
	}
}

func TestListPagination_LimitAndContinue(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	for _, n := range []string{"a", "b", "c"} {
		body := mustJSON(t, map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cm-" + n},
		})
		resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps", body)
		resp.Body.Close()
	}

	// First page: limit=2 → 2 items + a continue token.
	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/configmaps?limit=2", nil)
	page1 := decodeMap(t, resp.Body)
	resp.Body.Close()

	items1, _ := page1["items"].([]any)
	if len(items1) != 2 {
		t.Fatalf("page 1: got %d items, want 2", len(items1))
	}

	meta1, _ := page1["metadata"].(map[string]any)

	cont, _ := meta1["continue"].(string)
	if cont == "" {
		t.Fatalf("page 1: expected a continue token, got none")
	}

	// Second page: resume → the remaining item, no further continue.
	resp2 := do(t, http.MethodGet, base+"/api/v1/namespaces/default/configmaps?limit=2&continue="+cont, nil)
	page2 := decodeMap(t, resp2.Body)
	resp2.Body.Close()

	items2, _ := page2["items"].([]any)
	if len(items2) != 1 {
		t.Fatalf("page 2: got %d items, want 1", len(items2))
	}

	meta2, _ := page2["metadata"].(map[string]any)
	if c, _ := meta2["continue"].(string); c != "" {
		t.Fatalf("page 2: expected no further continue token, got %q", c)
	}
}

func TestFinalizers_RegistryKindGatedDeletion(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	npURL := base + "/apis/networking.k8s.io/v1/namespaces/default/networkpolicies"

	create := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "np", "finalizers": []any{"example.com/protect"}},
		"spec":     map[string]any{"podSelector": map[string]any{}},
	})
	resp := do(t, http.MethodPost, npURL, create)
	resp.Body.Close()

	// DELETE with a finalizer present → object goes Terminating, not removed.
	del := do(t, http.MethodDelete, npURL+"/np", nil)
	delObj := decodeMap(t, del.Body)
	del.Body.Close()

	meta, _ := delObj["metadata"].(map[string]any)
	if meta["deletionTimestamp"] == nil {
		t.Fatalf("delete with finalizer: expected deletionTimestamp to be set")
	}

	// Still retrievable.
	get := do(t, http.MethodGet, npURL+"/np", nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("after finalizer delete, GET: got %d, want 200 (still Terminating)", get.StatusCode)
	}
	get.Body.Close()

	// Removing the last finalizer completes the delete.
	patch := mustJSON(t, map[string]any{"metadata": map[string]any{"finalizers": []any{}}})
	pr := do(t, http.MethodPatch, npURL+"/np", patch)
	pr.Body.Close()

	gone := do(t, http.MethodGet, npURL+"/np", nil)
	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("after finalizer removed, GET: got %d, want 404", gone.StatusCode)
	}
	gone.Body.Close()
}

func TestFinalizers_TypedPodGatedDeletion(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	podURL := base + "/api/v1/namespaces/default/pods"

	create := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "fpod", "finalizers": []any{"example.com/protect"}},
		"spec": map[string]any{
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})
	resp := do(t, http.MethodPost, podURL, create)
	resp.Body.Close()

	del := do(t, http.MethodDelete, podURL+"/fpod", nil)
	delObj := decodeMap(t, del.Body)
	del.Body.Close()

	meta, _ := delObj["metadata"].(map[string]any)
	if meta["deletionTimestamp"] == nil {
		t.Fatalf("pod delete with finalizer: expected deletionTimestamp set (Terminating)")
	}

	get := do(t, http.MethodGet, podURL+"/fpod", nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("Terminating pod GET: got %d, want 200", get.StatusCode)
	}
	get.Body.Close()

	patch := mustJSON(t, map[string]any{"metadata": map[string]any{"finalizers": []any{}}})
	pr := do(t, http.MethodPatch, podURL+"/fpod", patch)
	pr.Body.Close()

	gone := do(t, http.MethodGet, podURL+"/fpod", nil)
	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("after finalizer removed, pod GET: got %d, want 404", gone.StatusCode)
	}
	gone.Body.Close()
}
