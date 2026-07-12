package kubernetes_test

import (
	"bytes"
	"net/http"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ptrInt32(v int32) *int32 { return &v }

func makeDeployment(name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptrInt32(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "main", Image: "nginx:1.27"}},
				},
			},
		},
	}
}

func TestDeployment_StatusMirrorsSpecReplicas(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeployment("web", 3)))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d", resp.StatusCode)
	}

	var got appsv1.Deployment
	mustDecode(t, resp.Body, &got)

	if got.Status.Replicas != 3 || got.Status.ReadyReplicas != 3 || got.Status.AvailableReplicas != 3 {
		t.Fatalf("status not mirrored: replicas=%d ready=%d available=%d",
			got.Status.Replicas, got.Status.ReadyReplicas, got.Status.AvailableReplicas)
	}
}

func TestDeployment_LifecycleAndPatch(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeployment("api", 2))).Body.Close()

	// Get
	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/api", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// List
	resp = do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments", nil)
	var list appsv1.DeploymentList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 1 {
		t.Fatalf("list: got %d items, want 1", len(list.Items))
	}

	// Patch to scale up.
	patch := []byte(`{"spec":{"replicas":5}}`)
	req, _ := http.NewRequest(http.MethodPatch,
		base+"/apis/apps/v1/namespaces/default/deployments/api", bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}

	var patched appsv1.Deployment
	mustDecode(t, resp.Body, &patched)

	if *patched.Spec.Replicas != 5 {
		t.Fatalf("spec.replicas after patch: got %d, want 5", *patched.Spec.Replicas)
	}

	if patched.Status.Replicas != 5 {
		t.Fatalf("status.replicas after patch: got %d, want 5", patched.Status.Replicas)
	}

	// Update via PUT.
	resp = do(t, http.MethodPut, base+"/apis/apps/v1/namespaces/default/deployments/api",
		mustJSON(t, makeDeployment("api", 1)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: got %d", resp.StatusCode)
	}

	var updated appsv1.Deployment
	mustDecode(t, resp.Body, &updated)

	if updated.Status.Replicas != 1 {
		t.Fatalf("status.replicas after PUT: got %d, want 1", updated.Status.Replicas)
	}

	// Delete
	resp = do(t, http.MethodDelete, base+"/apis/apps/v1/namespaces/default/deployments/api", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestDeployment_AllNamespacesListAndDefaultReplicas(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Replicas nil should default to 1.
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "no-replicas"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "x"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "c", Image: "x"}},
				},
			},
		},
	}

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", mustJSON(t, d))
	var got appsv1.Deployment
	mustDecode(t, resp.Body, &got)

	if got.Status.Replicas != 1 {
		t.Fatalf("default replicas: got %d, want 1", got.Status.Replicas)
	}

	// Across-namespaces list.
	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/kube-system/deployments",
		mustJSON(t, makeDeployment("sys", 1))).Body.Close()

	resp = do(t, http.MethodGet, base+"/apis/apps/v1/deployments", nil)
	var list appsv1.DeploymentList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("all-ns list: got %d items", len(list.Items))
	}
}

func TestDeployment_ErrorPaths(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Wrong API group.
	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/deployments", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong-group: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Missing namespace.
	resp = do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/nope/deployments",
		mustJSON(t, makeDeployment("d", 1)))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing-ns: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Missing name.
	resp = do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, &appsv1.Deployment{}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-name: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Duplicate.
	body := mustJSON(t, makeDeployment("dup", 1))
	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", body).Body.Close()

	resp = do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT name mismatch.
	resp = do(t, http.MethodPut, base+"/apis/apps/v1/namespaces/default/deployments/dup",
		mustJSON(t, makeDeployment("different", 1)))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("name-mismatch: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Cluster-wide non-GET.
	resp = do(t, http.MethodPost, base+"/apis/apps/v1/deployments", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("cluster POST: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Collection PUT.
	resp = do(t, http.MethodPut, base+"/apis/apps/v1/namespaces/default/deployments", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Item POST.
	resp = do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments/dup", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("item POST: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT bad body.
	resp = do(t, http.MethodPut, base+"/apis/apps/v1/namespaces/default/deployments/dup", []byte(`{not`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("put bad-body: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PATCH on missing.
	req, _ := http.NewRequest(http.MethodPatch,
		base+"/apis/apps/v1/namespaces/default/deployments/ghost", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch missing: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Get + Delete + Put on missing.
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		resp = do(t, method, base+"/apis/apps/v1/namespaces/default/deployments/ghost", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s missing: got %d", method, resp.StatusCode)
		}

		resp.Body.Close()
	}

	resp = do(t, http.MethodPut, base+"/apis/apps/v1/namespaces/default/deployments/ghost",
		mustJSON(t, makeDeployment("ghost", 1)))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("put missing: got %d", resp.StatusCode)
	}

	resp.Body.Close()
}
