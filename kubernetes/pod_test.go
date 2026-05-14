package kubernetes_test

import (
	"bytes"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPod_LifecycleInNamespace(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	pod := &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}},
		},
	}

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, pod))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status: got %d, want 201", resp.StatusCode)
	}

	var created corev1.Pod
	mustDecode(t, resp.Body, &created)

	if created.Status.Phase != corev1.PodPending {
		t.Fatalf("Phase on create: got %q, want Pending", created.Status.Phase)
	}

	if created.UID == "" {
		t.Fatal("Pod missing UID")
	}

	// Get
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/web", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: got %d", resp.StatusCode)
	}

	var got corev1.Pod
	mustDecode(t, resp.Body, &got)

	if got.Spec.Containers[0].Image != "nginx:1.27" {
		t.Fatalf("image: got %q", got.Spec.Containers[0].Image)
	}

	// List in namespace
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: got %d", resp.StatusCode)
	}

	var list corev1.PodList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 1 {
		t.Fatalf("list items: got %d, want 1", len(list.Items))
	}

	// Delete
	resp = do(t, http.MethodDelete, base+"/api/v1/namespaces/default/pods/web", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/web", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-after-delete: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestPod_PatchUpdatesImage(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods",
		mustJSON(t, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "c", Image: "nginx:1.27"}},
			},
		})).Body.Close()

	patch := []byte(`{"spec":{"containers":[{"name":"c","image":"nginx:1.29"}]}}`)
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/namespaces/default/pods/p", bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}

	var got corev1.Pod
	mustDecode(t, resp.Body, &got)

	if got.Spec.Containers[0].Image != "nginx:1.29" {
		t.Fatalf("image after patch: got %q", got.Spec.Containers[0].Image)
	}
}

func TestPod_AllNamespacesListAndUpdate(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	for _, ns := range []string{"default", "kube-system"} {
		do(t, http.MethodPost, base+"/api/v1/namespaces/"+ns+"/pods",
			mustJSON(t, &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p-" + ns},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "c", Image: "x"}},
				},
			})).Body.Close()
	}

	// All-namespaces list
	resp := do(t, http.MethodGet, base+"/api/v1/pods", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("all-ns list: got %d", resp.StatusCode)
	}

	var list corev1.PodList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("all-ns list: got %d items, want 2", len(list.Items))
	}

	// Update via PUT — name must match URL.
	updated := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p-default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "x-v2"}},
		},
	}

	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/pods/p-default", mustJSON(t, updated))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: got %d", resp.StatusCode)
	}

	var got corev1.Pod
	mustDecode(t, resp.Body, &got)

	if got.Spec.Containers[0].Image != "x-v2" {
		t.Fatalf("image after PUT: got %q", got.Spec.Containers[0].Image)
	}

	if got.ResourceVersion != "2" {
		t.Fatalf("resourceVersion after PUT: got %q, want 2", got.ResourceVersion)
	}
}

func TestPod_ErrorPaths(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Missing namespace.
	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/nope/pods",
		mustJSON(t, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}}))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing-ns: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	// Missing name in body.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", mustJSON(t, &corev1.Pod{}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-name: got %d, want 400", resp.StatusCode)
	}

	resp.Body.Close()

	// Duplicate.
	body := mustJSON(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "dup"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "x"}}},
	})
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", body).Body.Close()

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup: got %d, want 409", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT name mismatch.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/pods/dup",
		mustJSON(t, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "different"}}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("put name-mismatch: got %d, want 400", resp.StatusCode)
	}

	resp.Body.Close()

	// Method on collection.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/pods", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()

	// Method on cluster-wide.
	resp = do(t, http.MethodPost, base+"/api/v1/pods", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("cluster POST: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()

	// Wrong API group.
	resp = do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/pods", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong-group: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	// Get missing.
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/ghost", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-missing: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	// Delete missing.
	resp = do(t, http.MethodDelete, base+"/api/v1/namespaces/default/pods/ghost", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete-missing: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT bad body.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/pods/dup", []byte(`{not-json`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("put bad body: got %d, want 400", resp.StatusCode)
	}

	resp.Body.Close()

	// PATCH missing pod.
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/namespaces/default/pods/ghost",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch missing: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()

	// Method on item.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods/dup", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("item POST: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()
}
