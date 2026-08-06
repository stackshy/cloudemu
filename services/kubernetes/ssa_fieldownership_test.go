// Tests for SSA field-ownership fixes (Review Finding 9): apply removes fields a
// manager previously owned but now omits, and plain PUT/PATCH updates record an
// Update-operation managedFields entry (co-ownership, never a false 409).

package kubernetes_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

// doWithHeaders issues a request with an explicit content-type (used for the
// non-default merge-patch content type the apply harness doesn't cover).
func doWithHeaders(t *testing.T, method, url, contentType string, body []byte) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	return resp
}

// managedFieldsOf extracts metadata.managedFields as a slice of entry maps.
func managedFieldsEntries(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()

	meta, _ := m["metadata"].(map[string]any)
	raw, _ := meta["managedFields"].([]any)

	out := make([]map[string]any, 0, len(raw))

	for _, e := range raw {
		if em, ok := e.(map[string]any); ok {
			out = append(out, em)
		}
	}

	return out
}

// findManagedEntry returns the entry for (manager, operation), or nil.
func findManagedEntry(entries []map[string]any, manager, operation string) map[string]any {
	for _, e := range entries {
		if e["manager"] == manager && e["operation"] == operation {
			return e
		}
	}

	return nil
}

// entryHasLeaf reports whether an entry's fieldsV1 records the f:-prefixed path.
func entryHasLeaf(entry map[string]any, segs ...string) bool {
	cur, _ := entry["fieldsV1"].(map[string]any)

	for _, s := range segs {
		if cur == nil {
			return false
		}

		next, ok := cur["f:"+s].(map[string]any)
		if !ok {
			return false
		}

		cur = next
	}

	return cur != nil
}

// seedNetworkPolicy creates an empty NetworkPolicy and returns its item URL.
func seedNetworkPolicy(t *testing.T, base string) string {
	t.Helper()

	npURL := base + "/apis/networking.k8s.io/v1/namespaces/default/networkpolicies"
	seed := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "np"},
		"spec":     map[string]any{"podSelector": map[string]any{}},
	})

	c := do(t, http.MethodPost, npURL, seed)
	if c.StatusCode != http.StatusCreated {
		c.Body.Close()
		t.Fatalf("seed NetworkPolicy: got %d, want 201", c.StatusCode)
	}

	c.Body.Close()

	return npURL + "/np"
}

func npBody(a, b any) map[string]any {
	spec := map[string]any{"a": a}
	if b != nil {
		spec["b"] = b
	}

	return map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "np"}, "spec": spec,
	}
}

func TestSSA_ApplyRemovesOmittedOwnedField(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	itemURL := seedNetworkPolicy(t, base)

	// mgr-1 owns spec.a and spec.b.
	r1 := apply(t, itemURL, "mgr-1", false, npBody(float64(1), float64(2)))
	if r1.StatusCode != http.StatusOK {
		r1.Body.Close()
		t.Fatalf("first apply: got %d, want 200", r1.StatusCode)
	}
	r1.Body.Close()

	// mgr-1 re-applies WITHOUT spec.b — real SSA removes the omitted owned field.
	r2 := apply(t, itemURL, "mgr-1", false, npBody(float64(1), nil))
	if r2.StatusCode != http.StatusOK {
		r2.Body.Close()
		t.Fatalf("re-apply: got %d, want 200", r2.StatusCode)
	}

	obj := decodeMap(t, r2.Body)

	spec, _ := obj["spec"].(map[string]any)
	if _, ok := spec["b"]; ok {
		t.Fatalf("spec.b should be removed after omit; spec=%v", spec)
	}

	if _, ok := spec["a"]; !ok {
		t.Fatalf("spec.a should remain; spec=%v", spec)
	}

	entry := findManagedEntry(managedFieldsEntries(t, obj), "mgr-1", "Apply")
	if entry == nil {
		t.Fatal("mgr-1 Apply entry missing")
	}

	if entryHasLeaf(entry, "spec", "b") {
		t.Fatal("mgr-1 should no longer own spec.b")
	}

	if !entryHasLeaf(entry, "spec", "a") {
		t.Fatal("mgr-1 should still own spec.a")
	}
}

func TestSSA_ApplySharedFieldNotRemoved(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	itemURL := seedNetworkPolicy(t, base)

	// mgr-1 owns spec.a and spec.b via Apply.
	r1 := apply(t, itemURL, "mgr-1", false, npBody(float64(1), float64(2)))
	r1.Body.Close()

	// A plain PUT co-owns spec.b (Update entry) without stripping mgr-1's Apply.
	p := do(t, http.MethodPut, itemURL+"?fieldManager=kubectl-update", mustJSON(t, npBody(float64(1), float64(2))))
	if p.StatusCode != http.StatusOK {
		p.Body.Close()
		t.Fatalf("PUT: got %d, want 200", p.StatusCode)
	}
	p.Body.Close()

	// mgr-1 re-applies WITHOUT spec.b. Because kubectl-update still owns b, it
	// must NOT be deleted.
	r2 := apply(t, itemURL, "mgr-1", false, npBody(float64(1), nil))
	if r2.StatusCode != http.StatusOK {
		r2.Body.Close()
		t.Fatalf("re-apply: got %d, want 200", r2.StatusCode)
	}

	obj := decodeMap(t, r2.Body)

	spec, _ := obj["spec"].(map[string]any)
	if _, ok := spec["b"]; !ok {
		t.Fatalf("shared spec.b must survive mgr-1's omit; spec=%v", spec)
	}
}

func TestUpdate_PUTRegistersUpdateManager(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	itemURL := seedNetworkPolicy(t, base)

	a := apply(t, itemURL, "mgr-1", false, npBody(float64(1), nil))
	a.Body.Close()

	p := do(t, http.MethodPut, itemURL+"?fieldManager=kubectl-update", mustJSON(t, npBody(float64(1), nil)))
	if p.StatusCode != http.StatusOK {
		p.Body.Close()
		t.Fatalf("PUT: got %d, want 200", p.StatusCode)
	}

	obj := decodeMap(t, p.Body)

	if findManagedEntry(managedFieldsEntries(t, obj), "kubectl-update", "Update") == nil {
		t.Fatal("PUT did not record an Update managedFields entry for kubectl-update")
	}

	// mgr-1's Apply ownership must survive the co-owning PUT.
	if findManagedEntry(managedFieldsEntries(t, obj), "mgr-1", "Apply") == nil {
		t.Fatal("mgr-1 Apply entry should be preserved across a PUT")
	}
}

func TestUpdate_NoFalse409OverApplyOwnedField(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	itemURL := seedNetworkPolicy(t, base)

	a := apply(t, itemURL, "mgr-1", false, npBody(float64(1), nil))
	a.Body.Close()

	// PUT overwriting mgr-1's Apply-owned spec.a must succeed (no conflict).
	p := do(t, http.MethodPut, itemURL+"?fieldManager=kubectl-update", mustJSON(t, npBody(float64(5), nil)))
	if p.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(p.Body)
		p.Body.Close()
		t.Fatalf("PUT over apply-owned field: got %d, want 200 (%s)", p.StatusCode, body)
	}

	obj := decodeMap(t, p.Body)
	if spec, _ := obj["spec"].(map[string]any); spec["a"] != float64(5) {
		t.Fatalf("PUT should overwrite spec.a; spec=%v", obj["spec"])
	}

	// A merge PATCH over the same field also succeeds and stamps an Update entry.
	pj := doWithHeaders(t, http.MethodPatch, itemURL+"?fieldManager=kubectl-patch",
		"application/merge-patch+json", mustJSON(t, map[string]any{"spec": map[string]any{"a": float64(9)}}))
	if pj.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(pj.Body)
		pj.Body.Close()
		t.Fatalf("merge PATCH over apply-owned field: got %d, want 200 (%s)", pj.StatusCode, body)
	}

	obj = decodeMap(t, pj.Body)
	if findManagedEntry(managedFieldsEntries(t, obj), "kubectl-patch", "Update") == nil {
		t.Fatal("PATCH did not record an Update managedFields entry")
	}
}

func TestUpdate_PodPUTRegistersUpdateManager(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	podURL := base + "/api/v1/namespaces/default/pods"
	pod := map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"containers": []any{map[string]any{"name": "app", "image": "nginx:1.27"}},
		},
	}

	c := do(t, http.MethodPost, podURL, mustJSON(t, pod))
	if c.StatusCode != http.StatusCreated {
		c.Body.Close()
		t.Fatalf("create pod: got %d, want 201", c.StatusCode)
	}
	c.Body.Close()

	pod["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"] = "nginx:1.28"

	p := do(t, http.MethodPut, podURL+"/web?fieldManager=kubectl-update", mustJSON(t, pod))
	if p.StatusCode != http.StatusOK {
		p.Body.Close()
		t.Fatalf("pod PUT: got %d, want 200", p.StatusCode)
	}

	obj := decodeMap(t, p.Body)
	if findManagedEntry(managedFieldsEntries(t, obj), "kubectl-update", "Update") == nil {
		t.Fatal("pod PUT did not record an Update managedFields entry")
	}
}
