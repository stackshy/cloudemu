// Tests for Phase 5: server-side apply field ownership + conflict detection.

package kubernetes_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

// apply sends a server-side apply patch as fieldManager, returning the response.
func apply(t *testing.T, url, manager string, force bool, obj map[string]any) *http.Response {
	t.Helper()

	body := mustJSON(t, obj)
	u := url + "?fieldManager=" + manager
	if force {
		u += "&force=true"
	}

	req, err := http.NewRequest(http.MethodPatch, u, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new apply request: %v", err)
	}

	req.Header.Set("Content-Type", "application/apply-patch+yaml")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	return resp
}

func TestServerSideApply_OwnershipAndConflict(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	// Seed a NetworkPolicy (a plain registry kind with a free-form spec).
	npURL := base + "/apis/networking.k8s.io/v1/namespaces/default/networkpolicies"
	seed := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "np"},
		"spec":     map[string]any{"podSelector": map[string]any{}},
	})
	c := do(t, http.MethodPost, npURL, seed)
	c.Body.Close()

	// Manager "alice" applies a spec field and takes ownership.
	itemURL := npURL + "/np"
	a := apply(t, itemURL, "alice", false, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "np"},
		"spec":     map[string]any{"policyTypes": []any{"Ingress"}},
	})
	if a.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(a.Body)
		a.Body.Close()
		t.Fatalf("alice apply: got %d, want 200 (%s)", a.StatusCode, body)
	}

	obj := decodeMap(t, a.Body)
	a.Body.Close()

	meta, _ := obj["metadata"].(map[string]any)
	if mf, _ := meta["managedFields"].([]any); len(mf) == 0 {
		t.Fatalf("apply did not record managedFields")
	}

	// Manager "bob" applying a DIFFERENT value to alice's field → 409 conflict.
	b := apply(t, itemURL, "bob", false, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "np"},
		"spec":     map[string]any{"policyTypes": []any{"Egress"}},
	})
	if b.StatusCode != http.StatusConflict {
		b.Body.Close()
		t.Fatalf("bob conflicting apply: got %d, want 409", b.StatusCode)
	}
	b.Body.Close()

	// With force, bob wins.
	f := apply(t, itemURL, "bob", true, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "np"},
		"spec":     map[string]any{"policyTypes": []any{"Egress"}},
	})
	if f.StatusCode != http.StatusOK {
		f.Body.Close()
		t.Fatalf("bob force apply: got %d, want 200", f.StatusCode)
	}
	f.Body.Close()

	// alice re-applying the SAME value she owns must NOT conflict (idempotent).
	a2 := apply(t, itemURL, "alice", false, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "np"},
		"spec":     map[string]any{"podSelector": map[string]any{}},
	})
	if a2.StatusCode != http.StatusOK {
		a2.Body.Close()
		t.Fatalf("alice idempotent re-apply: got %d, want 200", a2.StatusCode)
	}
	a2.Body.Close()
}
