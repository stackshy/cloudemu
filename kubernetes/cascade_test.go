package kubernetes_test

import (
	"net/http"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestCascadeDelete_DropsAllNamespacedResources verifies that deleting a
// namespace also removes every namespaced resource that lived inside it.
func TestCascadeDelete_DropsAllNamespacedResources(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Set up a namespace populated with one of every resource.
	do(t, http.MethodPost, base+"/api/v1/namespaces",
		mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "doomed"}})).Body.Close()

	creates := []struct {
		path string
		body any
	}{
		{
			"/api/v1/namespaces/doomed/configmaps",
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm"}, Data: map[string]string{"a": "b"}},
		},
		{
			"/api/v1/namespaces/doomed/pods",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p"},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "c", Image: "x"}},
				},
			},
		},
		{
			"/api/v1/namespaces/doomed/secrets",
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s"}, StringData: map[string]string{"k": "v"}},
		},
		{
			"/api/v1/namespaces/doomed/serviceaccounts",
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "sa"}},
		},
		{
			"/api/v1/namespaces/doomed/services",
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "svc"},
				Spec: corev1.ServiceSpec{
					Type:  corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{{Port: 80}},
				},
			},
		},
		{
			"/apis/apps/v1/namespaces/doomed/deployments",
			makeDeployment("dep", 1),
		},
	}

	for _, c := range creates {
		resp := do(t, http.MethodPost, base+c.path, mustJSON(t, c.body))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed %s: got %d", c.path, resp.StatusCode)
		}

		resp.Body.Close()
	}

	// Sanity — namespaced lists are non-empty before delete.
	mustHaveItems(t, base, "/api/v1/namespaces/doomed/configmaps", 1)
	mustHaveItems(t, base, "/api/v1/namespaces/doomed/pods", 1)
	mustHaveItems(t, base, "/api/v1/namespaces/doomed/secrets", 1)
	// SA list contains the auto-bootstrap "default" SA + our "sa" = 2.
	mustHaveItems(t, base, "/api/v1/namespaces/doomed/serviceaccounts", 2)
	mustHaveItems(t, base, "/api/v1/namespaces/doomed/services", 1)
	mustHaveItems(t, base, "/apis/apps/v1/namespaces/doomed/deployments", 1)

	// Drop the namespace.
	resp := do(t, http.MethodDelete, base+"/api/v1/namespaces/doomed", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete-namespace: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Every individual lookup should now 404.
	gone := []string{
		"/api/v1/namespaces/doomed/configmaps/cm",
		"/api/v1/namespaces/doomed/pods/p",
		"/api/v1/namespaces/doomed/secrets/s",
		"/api/v1/namespaces/doomed/serviceaccounts/sa",
		"/api/v1/namespaces/doomed/serviceaccounts/default",
		"/api/v1/namespaces/doomed/services/svc",
		"/apis/apps/v1/namespaces/doomed/deployments/dep",
	}

	for _, path := range gone {
		resp := do(t, http.MethodGet, base+path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s after cascade: got %d, want 404", path, resp.StatusCode)
		}

		resp.Body.Close()
	}

	// Cross-namespace check — resources in *other* namespaces stay alive.
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/serviceaccounts/default", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("default ns SA disturbed by cascade: got %d", resp.StatusCode)
	}

	resp.Body.Close()
}

// mustHaveItems hits a List endpoint and asserts the decoded items count.
// Works for any K8s list type because we only inspect the `items` field
// via a minimal struct.
func mustHaveItems(t *testing.T, base, path string, want int) {
	t.Helper()

	resp := do(t, http.MethodGet, base+path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %s: got %d", path, resp.StatusCode)
	}

	// We decode into the type that matches the path. Keeping a single
	// minimal-items helper would force interface{} dancing; type-asserting
	// by path is simpler.
	switch {
	case strings.HasSuffix(path, "configmaps"):
		var list corev1.ConfigMapList
		mustDecode(t, resp.Body, &list)

		if len(list.Items) != want {
			t.Fatalf("configmaps in %s: got %d, want %d", path, len(list.Items), want)
		}
	case strings.HasSuffix(path, "pods"):
		var list corev1.PodList
		mustDecode(t, resp.Body, &list)

		if len(list.Items) != want {
			t.Fatalf("pods in %s: got %d, want %d", path, len(list.Items), want)
		}
	case strings.HasSuffix(path, "secrets"):
		var list corev1.SecretList
		mustDecode(t, resp.Body, &list)

		if len(list.Items) != want {
			t.Fatalf("secrets in %s: got %d, want %d", path, len(list.Items), want)
		}
	case strings.HasSuffix(path, "serviceaccounts"):
		var list corev1.ServiceAccountList
		mustDecode(t, resp.Body, &list)

		if len(list.Items) != want {
			t.Fatalf("sas in %s: got %d, want %d", path, len(list.Items), want)
		}
	case strings.HasSuffix(path, "services"):
		var list corev1.ServiceList
		mustDecode(t, resp.Body, &list)

		if len(list.Items) != want {
			t.Fatalf("services in %s: got %d, want %d", path, len(list.Items), want)
		}
	case strings.HasSuffix(path, "deployments"):
		var list appsv1.DeploymentList
		mustDecode(t, resp.Body, &list)

		if len(list.Items) != want {
			t.Fatalf("deployments in %s: got %d, want %d", path, len(list.Items), want)
		}
	default:
		t.Fatalf("mustHaveItems: unknown path suffix %s", path)
	}
}

