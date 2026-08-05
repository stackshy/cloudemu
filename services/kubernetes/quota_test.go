package kubernetes_test

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestResourceQuota_ObjectCountEnforced pins the object-count enforcement a
// real apiserver's quota admission plugin performs: a ResourceQuota capping
// "pods" at 1 lets the first Pod through and rejects the second with 403.
func TestResourceQuota_ObjectCountEnforced(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	quota := &corev1.ResourceQuota{
		TypeMeta:   metav1.TypeMeta{Kind: "ResourceQuota", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "pod-quota"},
		Spec:       corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{"pods": resource.MustParse("1")}},
	}

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/resourcequotas", mustJSON(t, quota))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create quota: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()

	first := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}}},
	}

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, first))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create first pod: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()

	second := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-2"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}}},
	}

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, second))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create second pod: got %d, want 403", resp.StatusCode)
	}

	var status metav1.Status
	mustDecode(t, resp.Body, &status)

	if status.Reason != metav1.StatusReasonForbidden {
		t.Fatalf("status reason: got %q, want Forbidden", status.Reason)
	}

	// The rejected create must not have been counted — the namespace still has
	// exactly the one Pod the quota allowed.
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods", nil)

	var list corev1.PodList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 1 {
		t.Fatalf("pods after denied create: got %d, want 1", len(list.Items))
	}
}

// TestResourceQuota_NoQuotaNoEnforcement pins that a namespace with no
// ResourceQuota object never gets denials — existing tests and callers that
// create many Pods in a namespace without a quota must keep working.
func TestResourceQuota_NoQuotaNoEnforcement(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	for i := range 3 {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "web-" + string(rune('a'+i))},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}}},
		}

		resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, pod))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create pod %d: got %d, want 201", i, resp.StatusCode)
		}

		resp.Body.Close()
	}
}
