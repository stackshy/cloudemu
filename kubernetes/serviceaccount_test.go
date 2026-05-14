package kubernetes_test

import (
	"bytes"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServiceAccount_DefaultBootstrapInEveryNamespace(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Bootstrap namespaces (default/kube-system/kube-public) should each
	// have a "default" ServiceAccount already present.
	for _, ns := range []string{"default", "kube-system", "kube-public"} {
		resp := do(t, http.MethodGet, base+"/api/v1/namespaces/"+ns+"/serviceaccounts/default", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("default SA in %s: got %d, want 200", ns, resp.StatusCode)
		}

		var sa corev1.ServiceAccount
		mustDecode(t, resp.Body, &sa)

		if sa.Name != "default" || sa.Namespace != ns {
			t.Fatalf("default SA in %s: got name=%q ns=%q", ns, sa.Name, sa.Namespace)
		}
	}

	// Creating a fresh namespace auto-creates a default SA in it too.
	do(t, http.MethodPost, base+"/api/v1/namespaces",
		mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-a"}})).Body.Close()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/tenant-a/serviceaccounts/default", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("default SA in tenant-a after namespace create: got %d", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestServiceAccount_LifecycleAndPatch(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Create a custom SA.
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "deployer",
			Labels: map[string]string{"team": "platform"},
		},
	}

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/serviceaccounts", mustJSON(t, sa))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d, want 201", resp.StatusCode)
	}

	// Get
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/serviceaccounts/deployer", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// List in namespace — should include "default" and "deployer".
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/serviceaccounts", nil)
	var list corev1.ServiceAccountList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("list: got %d items, want 2 (default + deployer)", len(list.Items))
	}

	// Patch
	patch := []byte(`{"metadata":{"annotations":{"version":"v2"}}}`)
	req, _ := http.NewRequest(http.MethodPatch,
		base+"/api/v1/namespaces/default/serviceaccounts/deployer", bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}

	var patched corev1.ServiceAccount
	mustDecode(t, resp.Body, &patched)

	if patched.Annotations["version"] != "v2" {
		t.Fatalf("annotation after patch: got %q", patched.Annotations["version"])
	}

	// Update via PUT
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/serviceaccounts/deployer",
		mustJSON(t, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "deployer",
				Labels: map[string]string{"team": "infra"},
			},
		}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: got %d", resp.StatusCode)
	}

	var updated corev1.ServiceAccount
	mustDecode(t, resp.Body, &updated)

	if updated.Labels["team"] != "infra" {
		t.Fatalf("label after put: got %q", updated.Labels["team"])
	}

	// Delete
	resp = do(t, http.MethodDelete, base+"/api/v1/namespaces/default/serviceaccounts/deployer", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestServiceAccount_AllNamespacesList(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/api/v1/serviceaccounts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("all-ns list: got %d", resp.StatusCode)
	}

	var list corev1.ServiceAccountList
	mustDecode(t, resp.Body, &list)

	// 3 bootstrap namespaces × 1 default SA = 3.
	if len(list.Items) != 3 {
		t.Fatalf("all-ns list: got %d items, want 3 bootstrap default SAs", len(list.Items))
	}
}

func TestServiceAccount_ErrorPaths(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Missing namespace.
	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/nope/serviceaccounts",
		mustJSON(t, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "sa"}}))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing-ns: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Duplicate — "default" already exists in "default" namespace.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/serviceaccounts",
		mustJSON(t, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "default"}}))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Missing name.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/serviceaccounts",
		mustJSON(t, &corev1.ServiceAccount{}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-name: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Wrong API group.
	resp = do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/serviceaccounts", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong-group: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Cluster-wide non-GET.
	resp = do(t, http.MethodPost, base+"/api/v1/serviceaccounts", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("cluster POST: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Collection PUT.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/serviceaccounts", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Item POST.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/serviceaccounts/default", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("item POST: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT name mismatch.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/serviceaccounts/default",
		mustJSON(t, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "diff"}}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("name-mismatch: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT bad body.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/serviceaccounts/default",
		[]byte(`{not`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("put bad-body: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Get/Put/Patch/Delete on missing.
	for method, payload := range map[string][]byte{
		http.MethodGet:    nil,
		http.MethodPut:    mustJSON(t, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "ghost"}}),
		http.MethodPatch:  []byte(`{}`),
		http.MethodDelete: nil,
	} {
		req, _ := http.NewRequest(method,
			base+"/api/v1/namespaces/default/serviceaccounts/ghost", bytes.NewReader(payload))
		if method == http.MethodPatch {
			req.Header.Set("Content-Type", "application/merge-patch+json")
		}

		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s missing: got %d", method, resp.StatusCode)
		}

		resp.Body.Close()
	}
}
