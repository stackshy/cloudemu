package kubernetes_test

import (
	"bytes"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestService_ClusterIPAllocatedSequentially(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// First three Services with empty ClusterIP — should get 10.96.0.1, .2, .3.
	wantIPs := []string{"10.96.0.1", "10.96.0.2", "10.96.0.3"}

	for i, want := range wantIPs {
		body := mustJSON(t, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "s-" + want},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeClusterIP,
				Selector: map[string]string{"app": "demo"},
				Ports:    []corev1.ServicePort{{Port: 80}},
			},
		})

		resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/services", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d: got %d", i, resp.StatusCode)
		}

		var got corev1.Service
		mustDecode(t, resp.Body, &got)

		if got.Spec.ClusterIP != want {
			t.Fatalf("ClusterIP[%d]: got %q, want %q", i, got.Spec.ClusterIP, want)
		}

		if len(got.Spec.ClusterIPs) != 1 || got.Spec.ClusterIPs[0] != want {
			t.Fatalf("ClusterIPs[%d]: got %v, want [%s]", i, got.Spec.ClusterIPs, want)
		}
	}
}

func TestService_HeadlessKeepsClusterIPEmpty(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	body := mustJSON(t, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "headless"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
			Selector:  map[string]string{"app": "demo"},
		},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/services", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d", resp.StatusCode)
	}

	var got corev1.Service
	mustDecode(t, resp.Body, &got)

	if got.Spec.ClusterIP != "None" {
		t.Fatalf("Headless ClusterIP: got %q, want None", got.Spec.ClusterIP)
	}

	if len(got.Spec.ClusterIPs) != 0 {
		t.Fatalf("Headless ClusterIPs: got %v, want empty", got.Spec.ClusterIPs)
	}
}

func TestService_ExplicitClusterIPAccepted(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/services",
		mustJSON(t, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "fixed"},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.96.99.99",
				Ports:     []corev1.ServicePort{{Port: 80}},
			},
		}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d", resp.StatusCode)
	}

	var got corev1.Service
	mustDecode(t, resp.Body, &got)

	if got.Spec.ClusterIP != "10.96.99.99" {
		t.Fatalf("explicit IP: got %q", got.Spec.ClusterIP)
	}
}

func TestService_ClusterIPImmutableOnUpdate(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	body := mustJSON(t, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "stable"},
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{{Port: 80}},
		},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/services", body)
	var created corev1.Service
	mustDecode(t, resp.Body, &created)

	allocatedIP := created.Spec.ClusterIP
	if allocatedIP == "" {
		t.Fatal("create did not allocate ClusterIP")
	}

	// Try to change it via PUT — should be ignored, allocated IP must survive.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/services/stable",
		mustJSON(t, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "stable"},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.96.42.42",
				Ports:     []corev1.ServicePort{{Port: 8080}},
			},
		}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: got %d", resp.StatusCode)
	}

	var updated corev1.Service
	mustDecode(t, resp.Body, &updated)

	if updated.Spec.ClusterIP != allocatedIP {
		t.Fatalf("ClusterIP after update: got %q, want %q (immutable)", updated.Spec.ClusterIP, allocatedIP)
	}

	if updated.Spec.Ports[0].Port != 8080 {
		t.Fatalf("Port: got %d, want 8080", updated.Spec.Ports[0].Port)
	}
}

func TestService_PatchPreservesClusterIP(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/services",
		mustJSON(t, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "patchable"},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{{Port: 80}},
			},
		}))

	var created corev1.Service
	mustDecode(t, resp.Body, &created)

	allocatedIP := created.Spec.ClusterIP

	// Patch attempts to overwrite ClusterIP and change ports.
	patch := []byte(`{"spec":{"clusterIP":"10.96.42.42","ports":[{"port":9090}]}}`)

	req, _ := http.NewRequest(http.MethodPatch,
		base+"/api/v1/namespaces/default/services/patchable",
		bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status: got %d", resp.StatusCode)
	}

	var patched corev1.Service
	mustDecode(t, resp.Body, &patched)

	if patched.Spec.ClusterIP != allocatedIP {
		t.Fatalf("ClusterIP after patch: got %q, want %q (immutable)", patched.Spec.ClusterIP, allocatedIP)
	}

	if patched.Spec.Ports[0].Port != 9090 {
		t.Fatalf("Port after patch: got %d, want 9090", patched.Spec.Ports[0].Port)
	}

	// Get the service back too — exercises getService happy path.
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/services/patchable", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: got %d", resp.StatusCode)
	}

	var got corev1.Service
	mustDecode(t, resp.Body, &got)

	if got.Spec.ClusterIP != allocatedIP {
		t.Fatalf("ClusterIP after Get: got %q", got.Spec.ClusterIP)
	}
}

func TestService_LifecycleAndAllNamespacesList(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	for _, ns := range []string{"default", "kube-system"} {
		do(t, http.MethodPost, base+"/api/v1/namespaces/"+ns+"/services",
			mustJSON(t, &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "svc"},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Ports: []corev1.ServicePort{{Port: 80}}},
			})).Body.Close()
	}

	resp := do(t, http.MethodGet, base+"/api/v1/services", nil)
	var list corev1.ServiceList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("all-ns list: got %d items", len(list.Items))
	}

	// Delete
	resp = do(t, http.MethodDelete, base+"/api/v1/namespaces/default/services/svc", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/services/svc", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-after-delete: got %d", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestService_ErrorPaths(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Missing namespace.
	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/nope/services",
		mustJSON(t, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "s"}}))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing-ns: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Missing name.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/services",
		mustJSON(t, &corev1.Service{}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-name: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Duplicate.
	body := mustJSON(t, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "dup"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Ports: []corev1.ServicePort{{Port: 80}}},
	})
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/services", body).Body.Close()

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/services", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Wrong API group.
	resp = do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/services", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong-group: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Cluster-wide non-GET.
	resp = do(t, http.MethodPost, base+"/api/v1/services", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("cluster POST: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT name mismatch.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/services/dup",
		mustJSON(t, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "diff"}}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("name-mismatch: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Collection PUT.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/services", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Item POST.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/services/dup", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("item POST: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT bad body.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/services/dup", []byte(`{not`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("put bad-body: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PATCH on missing.
	req, _ := http.NewRequest(http.MethodPatch,
		base+"/api/v1/namespaces/default/services/ghost",
		nil,
	)
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch missing: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Get + Put + Delete on missing.
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		resp = do(t, method, base+"/api/v1/namespaces/default/services/ghost", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s missing: got %d", method, resp.StatusCode)
		}

		resp.Body.Close()
	}

	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/services/ghost",
		mustJSON(t, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "ghost"}}))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("put missing: got %d", resp.StatusCode)
	}

	resp.Body.Close()
}
