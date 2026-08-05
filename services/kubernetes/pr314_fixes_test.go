// Regression tests for PR #314 data-plane fixes: CRD finalizer teardown,
// finalizer-aware owner GC / namespace cascade, patch-resurrection guards,
// ResourceQuota status.used accounting on delete, and dry-run quota enforcement.

package kubernetes_test

import (
	"net/http"
	"testing"
)

// nestedMap walks a decoded JSON object down a path of string keys, returning
// the map at the end (or nil if any hop is missing / not an object).
func nestedMap(m map[string]any, path ...string) map[string]any {
	cur := m
	for _, k := range path {
		next, _ := cur[k].(map[string]any)
		if next == nil {
			return nil
		}

		cur = next
	}

	return cur
}

func createWidgetCRDWithFinalizer(t *testing.T, base string) {
	t.Helper()

	crd := mustJSON(t, map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata": map[string]any{
			"name":       "widgets.example.com",
			"finalizers": []any{"example.com/protect"},
		},
		"spec": map[string]any{
			"group": "example.com",
			"names": map[string]any{
				"plural": "widgets", "singular": "widget", "kind": "Widget", "listKind": "WidgetList",
			},
			"scope": "Namespaced",
			"versions": []any{
				map[string]any{"name": "v1", "served": true, "storage": true},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/apiextensions.k8s.io/v1/customresourcedefinitions", crd)
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create CRD: got %d, want 201", resp.StatusCode)
	}
}

// Finding 1: a CRD deleted via the finalizer-drain path must still run onDelete,
// tearing down its custom-resource store + discovery entry — not just when the
// CRD is deleted immediately.
func TestCRD_FinalizerDrainDeregistersCRStore(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createWidgetCRDWithFinalizer(t, base)

	widget := mustJSON(t, map[string]any{
		"apiVersion": "example.com/v1", "kind": "Widget",
		"metadata": map[string]any{"name": "w1"},
	})
	cr := do(t, http.MethodPost, base+"/apis/example.com/v1/namespaces/default/widgets", widget)
	cr.Body.Close()

	if cr.StatusCode != http.StatusCreated {
		t.Fatalf("create custom resource: got %d, want 201", cr.StatusCode)
	}

	crdURL := base + "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/widgets.example.com"

	// DELETE with a finalizer → CRD goes Terminating, still established.
	del := do(t, http.MethodDelete, crdURL, nil)
	delObj := decodeMap(t, del.Body)
	del.Body.Close()

	if nestedMap(delObj, "metadata")["deletionTimestamp"] == nil {
		t.Fatalf("CRD delete with finalizer: expected deletionTimestamp set (Terminating)")
	}

	// While Terminating, the CR store is still live.
	stillServed := do(t, http.MethodGet, base+"/apis/example.com/v1/namespaces/default/widgets/w1", nil)
	stillServed.Body.Close()

	if stillServed.StatusCode != http.StatusOK {
		t.Fatalf("Terminating CRD: CR should still be served, got %d, want 200", stillServed.StatusCode)
	}

	// Drain the finalizer → the delete completes and onDelete tears down the CR store.
	patch := mustJSON(t, map[string]any{"metadata": map[string]any{"finalizers": []any{}}})
	pr := do(t, http.MethodPatch, crdURL, patch)
	pr.Body.Close()

	// The custom-resource kind is no longer served (store deregistered).
	gone := do(t, http.MethodGet, base+"/apis/example.com/v1/namespaces/default/widgets/w1", nil)
	gone.Body.Close()

	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("after CRD finalizer drain, CR GET: got %d, want 404 (store gone)", gone.StatusCode)
	}
}

// Finding 2: owner GC must honor a child's finalizers — a child carrying a
// finalizer goes Terminating rather than being hard-reaped, and only vanishes
// once its finalizers drain. A finalizer-free sibling is deleted immediately.
func TestOwnerGC_ChildFinalizerGoesTerminating(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	npURL := base + "/apis/networking.k8s.io/v1/namespaces/default/networkpolicies"
	np := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "owner"},
		"spec":     map[string]any{"podSelector": map[string]any{}},
	})
	npResp := do(t, http.MethodPost, npURL, np)
	npObj := decodeMap(t, npResp.Body)
	npResp.Body.Close()

	ownerUID, _ := nestedMap(npObj, "metadata")["uid"].(string)
	if ownerUID == "" {
		t.Fatal("owner NetworkPolicy has no uid")
	}

	ownerRefs := []any{map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"name": "owner", "uid": ownerUID, "controller": true,
	}}

	podURL := base + "/api/v1/namespaces/default/pods"
	mkPod := func(name string, finalizers []any) []byte {
		return mustJSON(t, map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{
				"name": name, "ownerReferences": ownerRefs, "finalizers": finalizers,
			},
			"spec": map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}},
		})
	}

	child := do(t, http.MethodPost, podURL, mkPod("child", []any{"example.com/protect"}))
	child.Body.Close()
	orphan := do(t, http.MethodPost, podURL, mkPod("orphan", nil))
	orphan.Body.Close()

	// Delete the owner → cascade GC.
	del := do(t, http.MethodDelete, npURL+"/owner", nil)
	del.Body.Close()

	// The finalizer-free child is hard-deleted.
	og := do(t, http.MethodGet, podURL+"/orphan", nil)
	og.Body.Close()

	if og.StatusCode != http.StatusNotFound {
		t.Fatalf("finalizer-free child after GC: got %d, want 404", og.StatusCode)
	}

	// The finalizer-bearing child is Terminating, not gone.
	cg := do(t, http.MethodGet, podURL+"/child", nil)
	cgObj := decodeMap(t, cg.Body)
	cg.Body.Close()

	if cg.StatusCode != http.StatusOK {
		t.Fatalf("finalizer child after GC: got %d, want 200 (Terminating)", cg.StatusCode)
	}

	if nestedMap(cgObj, "metadata")["deletionTimestamp"] == nil {
		t.Fatalf("finalizer child after GC: expected deletionTimestamp set")
	}

	// Draining the finalizer completes the delete.
	patch := mustJSON(t, map[string]any{"metadata": map[string]any{"finalizers": []any{}}})
	pr := do(t, http.MethodPatch, podURL+"/child", patch)
	pr.Body.Close()

	gone := do(t, http.MethodGet, podURL+"/child", nil)
	gone.Body.Close()

	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("child after finalizer drain: got %d, want 404", gone.StatusCode)
	}
}

// Finding 3: a merge-patch nulling deletionTimestamp must not resurrect a
// Terminating object — the server-owned timestamp is restored after the patch.
func TestPatch_CannotResurrectTerminatingPod(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	podURL := base + "/api/v1/namespaces/default/pods"
	create := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "term", "finalizers": []any{"example.com/protect"}},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}},
	})
	resp := do(t, http.MethodPost, podURL, create)
	resp.Body.Close()

	del := do(t, http.MethodDelete, podURL+"/term", nil)
	del.Body.Close()

	// RFC-7396 null-delete of the server-owned deletionTimestamp.
	patch := mustJSON(t, map[string]any{"metadata": map[string]any{"deletionTimestamp": nil}})
	pr := do(t, http.MethodPatch, podURL+"/term", patch)
	prObj := decodeMap(t, pr.Body)
	pr.Body.Close()

	if nestedMap(prObj, "metadata")["deletionTimestamp"] == nil {
		t.Fatalf("patch resurrected Terminating pod: deletionTimestamp was cleared")
	}

	// Still present and still Terminating.
	get := do(t, http.MethodGet, podURL+"/term", nil)
	getObj := decodeMap(t, get.Body)
	get.Body.Close()

	if get.StatusCode != http.StatusOK || nestedMap(getObj, "metadata")["deletionTimestamp"] == nil {
		t.Fatalf("after patch, pod GET: status %d, deletionTimestamp %v",
			get.StatusCode, nestedMap(getObj, "metadata")["deletionTimestamp"])
	}
}

// Finding 3 (registry path): same guard on the generic registry patch handler.
func TestPatch_CannotResurrectTerminatingRegistryObject(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	npURL := base + "/apis/networking.k8s.io/v1/namespaces/default/networkpolicies"
	create := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "term", "finalizers": []any{"example.com/protect"}},
		"spec":     map[string]any{"podSelector": map[string]any{}},
	})
	resp := do(t, http.MethodPost, npURL, create)
	resp.Body.Close()

	del := do(t, http.MethodDelete, npURL+"/term", nil)
	del.Body.Close()

	patch := mustJSON(t, map[string]any{"metadata": map[string]any{"deletionTimestamp": nil}})
	pr := do(t, http.MethodPatch, npURL+"/term", patch)
	prObj := decodeMap(t, pr.Body)
	pr.Body.Close()

	if nestedMap(prObj, "metadata")["deletionTimestamp"] == nil {
		t.Fatalf("patch resurrected Terminating NetworkPolicy: deletionTimestamp was cleared")
	}

	get := do(t, http.MethodGet, npURL+"/term", nil)
	get.Body.Close()

	if get.StatusCode != http.StatusOK {
		t.Fatalf("after patch, NetworkPolicy GET: got %d, want 200 (still Terminating)", get.StatusCode)
	}
}

// Finding 4: ResourceQuota status.used must track the live object count on
// delete, not climb monotonically.
func TestQuota_UsedTracksLiveCountOnDelete(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	quota := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "ResourceQuota",
		"metadata": map[string]any{"name": "pod-quota"},
		"spec":     map[string]any{"hard": map[string]any{"pods": "5"}},
	})
	qr := do(t, http.MethodPost, base+"/api/v1/namespaces/default/resourcequotas", quota)
	qr.Body.Close()

	podURL := base + "/api/v1/namespaces/default/pods"
	for _, name := range []string{"p1", "p2", "p3"} {
		pod := mustJSON(t, map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"name": name},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}},
		})
		resp := do(t, http.MethodPost, podURL, pod)
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create pod %s: got %d, want 201", name, resp.StatusCode)
		}
	}

	quotaURL := base + "/api/v1/namespaces/default/resourcequotas/pod-quota"

	if got := quotaUsedPods(t, quotaURL); got != "3" {
		t.Fatalf("status.used[pods] after 3 creates: got %q, want 3", got)
	}

	dp := do(t, http.MethodDelete, podURL+"/p2", nil)
	dp.Body.Close()

	if got := quotaUsedPods(t, quotaURL); got != "2" {
		t.Fatalf("status.used[pods] after 1 delete: got %q, want 2 (must not be monotonic)", got)
	}
}

func quotaUsedPods(t *testing.T, quotaURL string) string {
	t.Helper()

	resp := do(t, http.MethodGet, quotaURL, nil)
	obj := decodeMap(t, resp.Body)
	resp.Body.Close()

	used, _ := nestedMap(obj, "status", "used")["pods"].(string)

	return used
}

// Finding 5: a server-side dry-run create against an at-limit namespace must
// report the same 403 a real create would, not a false success.
func TestQuota_DryRunReportsForbiddenAtLimit(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	quota := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "ResourceQuota",
		"metadata": map[string]any{"name": "pod-quota"},
		"spec":     map[string]any{"hard": map[string]any{"pods": "1"}},
	})
	qr := do(t, http.MethodPost, base+"/api/v1/namespaces/default/resourcequotas", quota)
	qr.Body.Close()

	podURL := base + "/api/v1/namespaces/default/pods"
	first := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "p1"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}},
	})
	fr := do(t, http.MethodPost, podURL, first)
	fr.Body.Close()

	if fr.StatusCode != http.StatusCreated {
		t.Fatalf("create first pod: got %d, want 201", fr.StatusCode)
	}

	second := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "p2"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}},
	})
	dr := do(t, http.MethodPost, podURL+"?dryRun=All", second)
	dr.Body.Close()

	if dr.StatusCode != http.StatusForbidden {
		t.Fatalf("dry-run create at quota limit: got %d, want 403", dr.StatusCode)
	}
}
