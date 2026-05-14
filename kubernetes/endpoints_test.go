package kubernetes_test

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestEndpoints_AutoCreatedOnServiceCreate verifies that creating a Service
// automatically materialises an Endpoints object with the same name+namespace
// — matching what the endpoints controller does in a real cluster.
func TestEndpoints_AutoCreatedOnServiceCreate(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/services",
		mustJSON(t, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "web"},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{{Port: 80}},
			},
		})).Body.Close()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/endpoints/web", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get endpoints/web: got %d, want 200", resp.StatusCode)
	}

	var ep corev1.Endpoints
	mustDecode(t, resp.Body, &ep)

	if ep.Name != "web" || ep.Namespace != "default" {
		t.Fatalf("auto-created endpoints: got %s/%s", ep.Namespace, ep.Name)
	}
}

func TestEndpoints_DeletedAlongsideService(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/services",
		mustJSON(t, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "tmp"},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{{Port: 80}},
			},
		})).Body.Close()

	// Sanity — endpoints visible before delete.
	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/endpoints/tmp", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pre-delete endpoints: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Drop the service.
	do(t, http.MethodDelete, base+"/api/v1/namespaces/default/services/tmp", nil).Body.Close()

	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/endpoints/tmp", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("endpoints after service delete: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestEndpoints_ReadOnly(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Endpoints aren't user-creatable in Wave 2 — only auto-created by
	// Service. POST on the collection must 405.
	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/endpoints",
		mustJSON(t, &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "manual"}}))
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST endpoints: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestEndpoints_AllNamespacesList(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	for _, ns := range []string{"default", "kube-system"} {
		do(t, http.MethodPost, base+"/api/v1/namespaces/"+ns+"/services",
			mustJSON(t, &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "svc"},
				Spec: corev1.ServiceSpec{
					Type:  corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{{Port: 80}},
				},
			})).Body.Close()
	}

	resp := do(t, http.MethodGet, base+"/api/v1/endpoints", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("all-ns list: got %d", resp.StatusCode)
	}

	var list corev1.EndpointsList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("got %d, want 2 endpoints across namespaces", len(list.Items))
	}
}
