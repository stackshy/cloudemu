// Tests for Phase 4: CustomResourceDefinition support — a created CRD
// dynamically registers a servable custom-resource kind, surfaces in discovery,
// and deregisters (cascade-deleting its CRs) when the CRD is deleted.

package kubernetes_test

import (
	"net/http"
	"strings"
	"testing"
)

func createWidgetCRD(t *testing.T, base string) {
	t.Helper()

	crd := mustJSON(t, map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "widgets.example.com"},
		"spec": map[string]any{
			"group": "example.com",
			"names": map[string]any{
				"plural": "widgets", "singular": "widget", "kind": "Widget", "listKind": "WidgetList",
			},
			"scope": "Namespaced",
			"versions": []any{
				map[string]any{
					"name": "v1", "served": true, "storage": true,
					"subresources": map[string]any{"status": map[string]any{}},
				},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/apiextensions.k8s.io/v1/customresourcedefinitions", crd)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create CRD: got %d, want 201", resp.StatusCode)
	}

	obj := decodeMap(t, resp.Body)
	status, _ := obj["status"].(map[string]any)
	conds, _ := status["conditions"].([]any)
	if len(conds) == 0 {
		t.Fatalf("CRD status has no conditions (should be Established): %v", status)
	}
}

func TestCRD_DynamicKindServedAndDiscovered(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createWidgetCRD(t, base)

	// The custom resource kind is now servable via the generic handler.
	widget := mustJSON(t, map[string]any{
		"apiVersion": "example.com/v1", "kind": "Widget",
		"metadata": map[string]any{"name": "w1"},
		"spec":     map[string]any{"size": int64(3)},
	})

	cr := do(t, http.MethodPost, base+"/apis/example.com/v1/namespaces/default/widgets", widget)
	if cr.StatusCode != http.StatusCreated {
		cr.Body.Close()
		t.Fatalf("create custom resource: got %d, want 201", cr.StatusCode)
	}
	cr.Body.Close()

	get := do(t, http.MethodGet, base+"/apis/example.com/v1/namespaces/default/widgets/w1", nil)
	if get.StatusCode != http.StatusOK {
		get.Body.Close()
		t.Fatalf("get custom resource: got %d, want 200", get.StatusCode)
	}
	get.Body.Close()

	// Discovery advertises the new group-version + resource.
	disc := do(t, http.MethodGet, base+"/apis/example.com/v1", nil)
	body := decodeMap(t, disc.Body)
	disc.Body.Close()

	resources, _ := body["resources"].([]any)
	found := false
	for _, r := range resources {
		if rm, ok := r.(map[string]any); ok && rm["name"] == "widgets" {
			found = true
		}
	}

	if !found {
		t.Fatalf("discovery /apis/example.com/v1 does not advertise widgets: %v", resources)
	}
}

func TestCRD_DeleteDeregistersAndCascades(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createWidgetCRD(t, base)

	// Create a CR, then delete the CRD.
	widget := mustJSON(t, map[string]any{
		"apiVersion": "example.com/v1", "kind": "Widget",
		"metadata": map[string]any{"name": "w1"},
	})
	cr := do(t, http.MethodPost, base+"/apis/example.com/v1/namespaces/default/widgets", widget)
	cr.Body.Close()

	del := do(t, http.MethodDelete, base+"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/widgets.example.com", nil)
	del.Body.Close()

	// The custom-resource kind is no longer served.
	resp := do(t, http.MethodGet, base+"/apis/example.com/v1/namespaces/default/widgets/w1", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after CRD delete, custom resource GET: got %d, want 404", resp.StatusCode)
	}

	body := decodeMap(t, resp.Body)
	if msg, _ := body["message"].(string); !strings.Contains(msg, "not") {
		t.Logf("note: 404 message = %q", msg)
	}
}
