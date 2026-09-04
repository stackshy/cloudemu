// Tests for spec.nodeName immutability on a Pod PUT/replace (#895): once a Pod
// is bound to a node, a replace that changes nodeName is rejected 422 Invalid,
// and a replace that omits it carries the stored binding forward rather than
// unscheduling (or, on the multi-node scheduler, re-flipping) a Running Pod.

package kubernetes_test

import (
	"net/http"
	"testing"
)

// createScheduledPod POSTs a minimal Pod pre-bound to node (a caller-set
// spec.nodeName is honored by the scheduler) and returns the nodeName it ended
// up on, asserting the binding is non-empty.
func createScheduledPod(t *testing.T, base, name, node string) string {
	t.Helper()

	body := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"nodeName":   node,
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create pod: status %d, want 201", resp.StatusCode)
	}

	created := decodeMap(t, resp.Body)
	spec, _ := created["spec"].(map[string]any)
	got, _ := spec["nodeName"].(string)

	if got == "" {
		t.Fatalf("created pod has no nodeName; expected it to be scheduled")
	}

	return got
}

func TestPodNodeNameImmutable_RejectsChange(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	bound := createScheduledPod(t, base, "web", "cloudemu-node-1")

	// A replace that points the bound Pod at a different node must be rejected.
	body := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"nodeName":   "cloudemu-node-2",
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})

	resp := do(t, http.MethodPut, base+"/api/v1/namespaces/default/pods/web", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("replace with changed nodeName: status %d, want 422", resp.StatusCode)
	}

	status := decodeMap(t, resp.Body)
	if reason, _ := status["reason"].(string); reason != "Invalid" {
		t.Fatalf("reason: got %q, want Invalid", reason)
	}

	details, _ := status["details"].(map[string]any)
	causes, _ := details["causes"].([]any)
	if len(causes) != 1 {
		t.Fatalf("expected 1 cause, got %d", len(causes))
	}

	cause, _ := causes[0].(map[string]any)
	if field, _ := cause["field"].(string); field != "spec.nodeName" {
		t.Fatalf("cause field: got %q, want spec.nodeName", field)
	}

	// The stored binding must be untouched by a rejected replace.
	if got := podPlacements(t, base, "default")["web"]; got.node != bound || got.phase != "Running" {
		t.Fatalf("after rejected replace: got node=%q phase=%q, want node=%q Running", got.node, got.phase, bound)
	}
}

func TestPodNodeNameImmutable_CarriesForwardOnOmit(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// Bind to node-2, which is NOT the first-fit worker (node-1). Without the
	// carry-forward, an omitted nodeName re-runs the scheduler and flips the Pod
	// onto node-1, so this binding is what proves the fix holds.
	bound := createScheduledPod(t, base, "web", "cloudemu-node-2")

	// A spec-only replace that omits nodeName must keep the existing binding, not
	// re-run the scheduler (which could flip the Pod onto another node or Pending).
	body := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "web", "labels": map[string]any{"app": "web"}},
		"spec": map[string]any{
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})

	resp := do(t, http.MethodPut, base+"/api/v1/namespaces/default/pods/web", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replace omitting nodeName: status %d, want 200", resp.StatusCode)
	}

	updated := decodeMap(t, resp.Body)
	spec, _ := updated["spec"].(map[string]any)
	if node, _ := spec["nodeName"].(string); node != bound {
		t.Fatalf("nodeName after omit: got %q, want carried-forward %q", node, bound)
	}

	if got := podPlacements(t, base, "default")["web"]; got.node != bound || got.phase != "Running" {
		t.Fatalf("after carry-forward replace: got node=%q phase=%q, want node=%q Running", got.node, got.phase, bound)
	}
}
