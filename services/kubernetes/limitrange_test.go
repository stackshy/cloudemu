package kubernetes_test

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestLimitRange_DefaultsApplied pins that a Container-type LimitRange's
// default is applied to a Pod created without an explicit cpu limit.
func TestLimitRange_DefaultsApplied(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	lr := &corev1.LimitRange{
		TypeMeta:   metav1.TypeMeta{Kind: "LimitRange", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "defaults"},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{
				{Type: corev1.LimitTypeContainer, Default: corev1.ResourceList{"cpu": resource.MustParse("250m")}},
			},
		},
	}

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/limitranges", mustJSON(t, lr))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create limitrange: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}}},
	}

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, pod))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create pod: got %d, want 201", resp.StatusCode)
	}

	var created corev1.Pod
	mustDecode(t, resp.Body, &created)

	got, ok := created.Spec.Containers[0].Resources.Limits["cpu"]
	if !ok {
		t.Fatal("container cpu limit not defaulted")
	}

	if got.String() != "250m" {
		t.Fatalf("defaulted cpu limit: got %q, want 250m", got.String())
	}
}

// TestLimitRange_MaxViolationRejected pins that a Pod requesting more than a
// LimitRange's max is rejected with 403, and never persisted.
func TestLimitRange_MaxViolationRejected(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	lr := &corev1.LimitRange{
		TypeMeta:   metav1.TypeMeta{Kind: "LimitRange", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "caps"},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{
				{Type: corev1.LimitTypeContainer, Max: corev1.ResourceList{"cpu": resource.MustParse("500m")}},
			},
		},
	}

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/limitranges", mustJSON(t, lr))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create limitrange: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "too-big"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app", Image: "nginx:1.27",
			Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("1")}},
		}}},
	}

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, pod))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create over-limit pod: got %d, want 403", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/too-big", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("rejected pod persisted: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}
