package kubernetes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// A merge-patch to a registry object's replicas (kubectl scale, kubectl patch)
// must survive the patch/merge round-trip. Regression guard: the merged bytes
// were once decoded into a plain map[string]any, turning JSON integers into
// float64, so unstructured.NestedInt64 failed its type assertion and replicas
// silently became 0 — scaling *up* actually scaled the workload to zero.
func TestRegistry_MergePatchScalePreservesReplicas(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	ns := do(t, http.MethodPost, base+"/api/v1/namespaces",
		[]byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"default"}}`))
	ns.Body.Close()

	rs := []byte(`{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"web"},` +
		`"spec":{"replicas":1,"selector":{"matchLabels":{"app":"web"}},` +
		`"template":{"metadata":{"labels":{"app":"web"}},"spec":{"containers":[{"name":"c","image":"nginx"}]}}}}`)
	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/replicasets", rs)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create replicaset: status %d", resp.StatusCode)
	}

	// kubectl scale sends a merge-patch to the /scale subresource.
	patch := []byte(`{"spec":{"replicas":4}}`)
	req, _ := http.NewRequest(http.MethodPatch,
		base+"/apis/apps/v1/namespaces/default/replicasets/web/scale", bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/merge-patch+json")
	pr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch scale: %v", err)
	}
	defer pr.Body.Close()
	if pr.StatusCode != http.StatusOK {
		t.Fatalf("patch scale: status %d", pr.StatusCode)
	}

	if got := scaleReplicas(t, pr); got != 4 {
		t.Fatalf("scale replicas after merge-patch = %d, want 4", got)
	}

	// And the object itself must reflect the new replica count.
	gr := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/replicasets/web", nil)
	defer gr.Body.Close()
	var obj map[string]any
	if err := json.NewDecoder(gr.Body).Decode(&obj); err != nil {
		t.Fatalf("decode replicaset: %v", err)
	}
	spec, _ := obj["spec"].(map[string]any)
	if r, _ := spec["replicas"].(float64); int(r) != 4 {
		t.Fatalf("replicaset spec.replicas = %v, want 4", spec["replicas"])
	}
}

func scaleReplicas(t *testing.T, resp *http.Response) int {
	t.Helper()

	var scale map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&scale); err != nil {
		t.Fatalf("decode scale: %v", err)
	}
	spec, _ := scale["spec"].(map[string]any)
	r, _ := spec["replicas"].(float64)

	return int(r)
}
