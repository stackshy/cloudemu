package kubernetes_test

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMetricsEndpoint_PodMetrics(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	pod := &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}},
		},
	}

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, pod)).Body.Close()

	resp := do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/namespaces/default/pods/web", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get pod metrics: got %d, want 200", resp.StatusCode)
	}

	var got map[string]any
	mustDecode(t, resp.Body, &got)

	if got["kind"] != "PodMetrics" {
		t.Fatalf("kind: got %v, want PodMetrics", got["kind"])
	}

	containers, ok := got["containers"].([]any)
	if !ok || len(containers) != 1 {
		t.Fatalf("containers: got %v", got["containers"])
	}

	usage, ok := containers[0].(map[string]any)["usage"].(map[string]any)
	if !ok || usage["cpu"] == "" || usage["memory"] == "" {
		t.Fatalf("usage missing: got %v", containers[0])
	}

	// Namespace list should also surface the Pod.
	resp = do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/namespaces/default/pods", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list pod metrics: got %d, want 200", resp.StatusCode)
	}

	var list map[string]any
	mustDecode(t, resp.Body, &list)

	items, ok := list["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items: got %v", list["items"])
	}

	// Cluster-wide pods and the single synthetic Node's metrics should also
	// resolve, since `kubectl top pods -A` / `kubectl top nodes` hit these.
	resp = do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/pods", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("all-ns pod metrics: got %d, want 200", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/nodes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("node metrics list: got %d, want 200", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestMetricsEndpoint_GroupDiscovery(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/apis", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apis: got %d, want 200", resp.StatusCode)
	}

	var groups map[string]any
	mustDecode(t, resp.Body, &groups)

	found := false

	for _, g := range groups["groups"].([]any) {
		if g.(map[string]any)["name"] == "metrics.k8s.io" {
			found = true
		}
	}

	if !found {
		t.Fatalf("metrics.k8s.io not advertised in /apis: %v", groups["groups"])
	}

	resp = do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("group-version discovery: got %d, want 200", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestMetricsEndpoint_NodeMetricsItem(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/nodes/cloudemu-node-0", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("node metrics item: got %d, want 200", resp.StatusCode)
	}

	var got map[string]any
	mustDecode(t, resp.Body, &got)

	if got["kind"] != "NodeMetrics" {
		t.Fatalf("kind: got %v, want NodeMetrics", got["kind"])
	}

	usage, ok := got["usage"].(map[string]any)
	if !ok || usage["cpu"] == "" || usage["memory"] == "" {
		t.Fatalf("usage missing: got %v", got["usage"])
	}
}

func TestMetricsEndpoint_NotFoundAndMethodNotAllowed(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/namespaces/default/pods/ghost", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing pod: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/nodes/ghost", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing node: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodPost, base+"/apis/metrics.k8s.io/v1beta1/pods", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST pods: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/widgets", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unrecognized path: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	// Namespaced path whose trailing resource isn't "pods".
	resp = do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/namespaces/default/services", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong namespaced resource: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	// Bare "namespaces/{ns}" with no resource segment.
	resp = do(t, http.MethodGet, base+"/apis/metrics.k8s.io/v1beta1/namespaces/default", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("namespace with no resource: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}
