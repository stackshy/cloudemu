// Tests for the Phase 1 surface-fidelity work: server-side dry-run, Event
// field selectors, pod log/exec subresources, the Job reconcile shrink fix,
// and pod-count-clamp surfacing.

package kubernetes_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// decodeMap decodes a JSON response body into a generic map.
func decodeMap(t *testing.T, body io.ReadCloser) map[string]any {
	t.Helper()

	out := map[string]any{}
	mustDecode(t, body, &out)

	return out
}

func nestedInt(t *testing.T, m map[string]any, path ...string) (int64, bool) {
	t.Helper()

	cur := any(m)
	for _, p := range path {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}

		cur, ok = asMap[p]
		if !ok {
			return 0, false
		}
	}

	f, ok := cur.(float64) // encoding/json numbers decode to float64
	if !ok {
		return 0, false
	}

	return int64(f), true
}

func TestDryRun_TypedCreateNotPersisted(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	body := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "dry"},
		"data":     map[string]any{"k": "v"},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps?dryRun=All", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("dry-run create: got %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	get := do(t, http.MethodGet, base+"/api/v1/namespaces/default/configmaps/dry", nil)
	if get.StatusCode != http.StatusNotFound {
		t.Fatalf("after dry-run, GET: got %d, want 404 (must not persist)", get.StatusCode)
	}
	get.Body.Close()
}

func TestDryRun_RegistryCreateNotPersisted(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	body := mustJSON(t, map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{"name": "dryjob"},
		"spec": map[string]any{
			"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "c", "image": "img"}},
			}},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/batch/v1/namespaces/default/jobs?dryRun=All", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("dry-run job create: got %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	get := do(t, http.MethodGet, base+"/apis/batch/v1/namespaces/default/jobs/dryjob", nil)
	if get.StatusCode != http.StatusNotFound {
		t.Fatalf("after dry-run, GET job: got %d, want 404 (must not persist)", get.StatusCode)
	}
	get.Body.Close()

	// A dry-run Job must not have materialized any Pods either.
	pods := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods", nil)
	defer pods.Body.Close()

	list := decodeMap(t, pods.Body)
	if items, ok := list["items"].([]any); ok && len(items) != 0 {
		t.Fatalf("dry-run job left %d pods behind", len(items))
	}
}

func TestEventFieldSelector(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	mkEvent := func(name, involved, reason string) {
		body := mustJSON(t, map[string]any{
			"apiVersion":     "v1",
			"kind":           "Event",
			"metadata":       map[string]any{"name": name},
			"involvedObject": map[string]any{"kind": "Pod", "name": involved},
			"reason":         reason,
			"type":           "Normal",
		})
		resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/events", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create event %s: got %d", name, resp.StatusCode)
		}
		resp.Body.Close()
	}

	mkEvent("e1", "pod-a", "Started")
	mkEvent("e2", "pod-b", "Killing")

	resp := do(t, http.MethodGet,
		base+"/api/v1/namespaces/default/events?fieldSelector=involvedObject.name=pod-a", nil)
	defer resp.Body.Close()

	list := decodeMap(t, resp.Body)

	items, ok := list["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("involvedObject.name=pod-a: got %d items, want 1", len(items))
	}

	// And a reason selector.
	resp2 := do(t, http.MethodGet,
		base+"/api/v1/namespaces/default/events?fieldSelector=reason=Killing", nil)
	defer resp2.Body.Close()

	list2 := decodeMap(t, resp2.Body)
	if items, ok := list2["items"].([]any); !ok || len(items) != 1 {
		t.Fatalf("reason=Killing: got %d items, want 1", len(items))
	}
}

func TestPodLog_Synthetic(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createPodNamed(t, base, "logpod")

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/logpod/log", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pod log: got %d, want 200", resp.StatusCode)
	}

	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), "synthetic log") {
		t.Fatalf("pod log body = %q, want synthetic marker", string(out))
	}
}

func TestPodExec_TypedNotImplemented(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createPodNamed(t, base, "execpod")

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/execpod/exec", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("pod exec: got %d, want 501", resp.StatusCode)
	}

	status := decodeMap(t, resp.Body)
	if status["kind"] != "Status" {
		t.Fatalf("pod exec: want typed Status object, got kind=%v", status["kind"])
	}
}

func TestJobReconcile_ShrinkCorrectsSucceeded(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	job := func(completions int) []byte {
		return mustJSON(t, map[string]any{
			"apiVersion": "batch/v1", "kind": "Job",
			"metadata": map[string]any{"name": "j1"},
			"spec": map[string]any{
				"completions": completions,
				"template": map[string]any{"spec": map[string]any{
					"containers": []any{map[string]any{"name": "c", "image": "img"}},
				}},
			},
		})
	}

	resp := do(t, http.MethodPost, base+"/apis/batch/v1/namespaces/default/jobs", job(3))
	created := decodeMap(t, resp.Body)
	resp.Body.Close()

	if got, _ := nestedInt(t, created, "status", "succeeded"); got != 3 {
		t.Fatalf("initial job status.succeeded: got %d, want 3", got)
	}

	// Shrink completions to 1: succeeded must follow down, not stay at 3.
	resp2 := do(t, http.MethodPut, base+"/apis/batch/v1/namespaces/default/jobs/j1", job(1))
	updated := decodeMap(t, resp2.Body)
	resp2.Body.Close()

	if got, _ := nestedInt(t, updated, "status", "succeeded"); got != 1 {
		t.Fatalf("after shrink, job status.succeeded: got %d, want 1 (overstated succeeded bug)", got)
	}

	// Exactly one owned Pod should remain.
	pods := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods", nil)
	defer pods.Body.Close()

	list := decodeMap(t, pods.Body)

	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("after shrink, pod count: got %d, want 1", len(items))
	}
}

func TestPodCountClamp_Annotation(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	body := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "ReplicaSet",
		"metadata": map[string]any{"name": "big"},
		"spec": map[string]any{
			"replicas": 600,
			"selector": map[string]any{"matchLabels": map[string]any{"app": "x"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "x"}},
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "c", "image": "i"}},
				},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/replicasets", body)
	defer resp.Body.Close()

	obj := decodeMap(t, resp.Body)

	if got, _ := nestedInt(t, obj, "status", "replicas"); got != 500 {
		t.Fatalf("clamped status.replicas: got %d, want 500", got)
	}

	meta, _ := obj["metadata"].(map[string]any)
	anns, _ := meta["annotations"].(map[string]any)

	if anns["cloudemu.io/pod-count-clamped"] == nil {
		t.Fatalf("expected clamp annotation, got annotations=%v", anns)
	}
}

// createPodNamed POSTs a minimal Pod and fails the test on a non-201.
func createPodNamed(t *testing.T, base, name string) {
	t.Helper()

	body := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"containers": []any{map[string]any{"name": "main", "image": "nginx"}},
		},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create pod %s: got %d, want 201", name, resp.StatusCode)
	}
}
