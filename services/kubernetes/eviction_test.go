package kubernetes_test

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func createWebPod(t *testing.T, base, name string) {
	t.Helper()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app": "web"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}}},
	}

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, pod))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create pod %s: got %d, want 201", name, resp.StatusCode)
	}

	resp.Body.Close()
}

func createWebPDB(t *testing.T, base string, minAvailable int32) {
	t.Helper()

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "web-pdb"},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &intstr.IntOrString{Type: intstr.Int, IntVal: minAvailable},
			Selector:     &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		},
	}

	resp := do(t, http.MethodPost, base+"/apis/policy/v1/namespaces/default/poddisruptionbudgets", mustJSON(t, pdb))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create pdb: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()
}

// TestEviction_BlockedByPDB pins that evicting a Pod which would drop the
// matching set below the PDB's minAvailable is rejected with 429, and the
// Pod is left in place.
func TestEviction_BlockedByPDB(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	createWebPDB(t, base, 2)
	createWebPod(t, base, "web-1")
	createWebPod(t, base, "web-2")

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods/web-1/eviction", nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("evict status: got %d, want 429", resp.StatusCode)
	}

	var status metav1.Status
	mustDecode(t, resp.Body, &status)

	if status.Reason != metav1.StatusReasonTooManyRequests {
		t.Fatalf("status reason: got %q, want TooManyRequests", status.Reason)
	}

	// The Pod must still be there — the eviction was refused, not merely
	// reported as refused.
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/web-1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pod after blocked eviction: got %d, want 200", resp.StatusCode)
	}

	resp.Body.Close()
}

// TestEviction_Allowed pins that evicting a Pod within the PDB's budget
// deletes it, the same as a plain DELETE would.
func TestEviction_Allowed(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	createWebPDB(t, base, 1)
	createWebPod(t, base, "web-1")
	createWebPod(t, base, "web-2")

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods/web-1/eviction", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("evict status: got %d, want 200", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/web-1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pod after allowed eviction: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}

// TestEviction_NotFoundPod pins the 404 path for evicting a Pod that doesn't
// exist.
func TestEviction_NotFoundPod(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods/ghost/eviction", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("evict ghost pod: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}
