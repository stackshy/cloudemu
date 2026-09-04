// Tests for StatefulSet persistentVolumeClaimRetentionPolicy handling and the
// paired namespace-delete registry-store cascade. The default policy is
// Retain/Retain, so volumeClaimTemplate PVCs must OUTLIVE a StatefulSet delete
// (the "helm uninstall does not lose data" property); whenDeleted=Delete opts
// the PVCs into the STS-delete cascade; whenScaled=Delete reaps the PVCs of
// ordinals dropped by a scale-down.

package kubernetes_test

import (
	"net/http"
	"testing"
)

// makeStatefulSet builds a StatefulSet manifest with a single "data"
// volumeClaimTemplate. extraSpec is merged into spec (e.g. a
// persistentVolumeClaimRetentionPolicy).
func makeStatefulSet(name string, replicas int, extraSpec map[string]any) map[string]any {
	spec := map[string]any{
		"replicas":    int64(replicas),
		"serviceName": name,
		"selector":    map[string]any{"matchLabels": map[string]any{"app": name}},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": name}},
			"spec": map[string]any{
				"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
			},
		},
		"volumeClaimTemplates": []any{
			map[string]any{
				"metadata": map[string]any{"name": "data"},
				"spec": map[string]any{
					"accessModes": []any{"ReadWriteOnce"},
					"resources":   map[string]any{"requests": map[string]any{"storage": "1Gi"}},
				},
			},
		},
	}

	for k, v := range extraSpec {
		spec[k] = v
	}

	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "StatefulSet",
		"metadata":   map[string]any{"name": name},
		"spec":       spec,
	}
}

// pvcExists reports whether the named PVC is present in the default namespace.
func pvcExists(t *testing.T, base, name string) bool {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/persistentvolumeclaims/"+name, nil)
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true
	case http.StatusNotFound:
		return false
	default:
		t.Fatalf("get pvc %s: unexpected status %d", name, resp.StatusCode)

		return false
	}
}

func createStatefulSet(t *testing.T, base string, sts map[string]any) {
	t.Helper()

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/statefulsets", mustJSON(t, sts))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create statefulset: got %d, want 201", resp.StatusCode)
	}
}

// TestStatefulSetPVC_SurvivesDelete_DefaultRetain is the ticket fix: with the
// default Retain/Retain policy the volumeClaimTemplate PVC has no owner ref, so
// deleting the StatefulSet (a helm uninstall) leaves the data behind.
func TestStatefulSetPVC_SurvivesDelete_DefaultRetain(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createStatefulSet(t, base, makeStatefulSet("web", 1, nil))

	// The PVC exists and is Bound.
	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/persistentvolumeclaims/data-web-0", nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("get data-web-0: got %d, want 200", resp.StatusCode)
	}

	pvc := decodeMap(t, resp.Body)
	status, _ := pvc["status"].(map[string]any)
	if phase, _ := status["phase"].(string); phase != "Bound" {
		t.Fatalf("data-web-0 phase = %q, want Bound", phase)
	}

	// A Retain PVC must carry NO owner ref, or the STS-delete cascade would reap it.
	meta, _ := pvc["metadata"].(map[string]any)
	if owners, _ := meta["ownerReferences"].([]any); len(owners) != 0 {
		t.Fatalf("data-web-0 has owner refs under Retain: %v", owners)
	}

	// Uninstall: delete the StatefulSet.
	del := do(t, http.MethodDelete, base+"/apis/apps/v1/namespaces/default/statefulsets/web", nil)
	if del.StatusCode != http.StatusOK {
		del.Body.Close()
		t.Fatalf("delete statefulset: got %d, want 200", del.StatusCode)
	}
	del.Body.Close()

	// The PVC must SURVIVE — data is not lost on uninstall.
	if !pvcExists(t, base, "data-web-0") {
		t.Fatalf("data-web-0 was deleted on STS delete under default Retain (data loss)")
	}
}

// TestStatefulSetPVC_WhenDeletedDelete_RemovedOnDelete verifies that opting into
// whenDeleted=Delete stamps the STS owner ref so the STS-delete cascade reaps
// the PVC.
func TestStatefulSetPVC_WhenDeletedDelete_RemovedOnDelete(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createStatefulSet(t, base, makeStatefulSet("web", 1, map[string]any{
		"persistentVolumeClaimRetentionPolicy": map[string]any{
			"whenDeleted": "Delete",
			"whenScaled":  "Retain",
		},
	}))

	if !pvcExists(t, base, "data-web-0") {
		t.Fatalf("data-web-0 not created")
	}

	del := do(t, http.MethodDelete, base+"/apis/apps/v1/namespaces/default/statefulsets/web", nil)
	if del.StatusCode != http.StatusOK {
		del.Body.Close()
		t.Fatalf("delete statefulset: got %d, want 200", del.StatusCode)
	}
	del.Body.Close()

	if pvcExists(t, base, "data-web-0") {
		t.Fatalf("data-web-0 survived STS delete under whenDeleted=Delete (owner-ref cascade did not fire)")
	}
}

// TestStatefulSetPVC_WhenScaledDelete_ReapsOrdinals verifies the explicit
// out-of-range-ordinal reap: with whenScaled=Delete, scaling 3 -> 1 removes the
// PVCs for ordinals 1 and 2 while keeping ordinal 0.
func TestStatefulSetPVC_WhenScaledDelete_ReapsOrdinals(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	policy := map[string]any{
		"persistentVolumeClaimRetentionPolicy": map[string]any{
			"whenDeleted": "Retain",
			"whenScaled":  "Delete",
		},
	}

	createStatefulSet(t, base, makeStatefulSet("web", 3, policy))

	for _, n := range []string{"data-web-0", "data-web-1", "data-web-2"} {
		if !pvcExists(t, base, n) {
			t.Fatalf("%s not created at replicas=3", n)
		}
	}

	// Scale down to 1.
	put := do(t, http.MethodPut, base+"/apis/apps/v1/namespaces/default/statefulsets/web",
		mustJSON(t, makeStatefulSet("web", 1, policy)))
	if put.StatusCode != http.StatusOK {
		put.Body.Close()
		t.Fatalf("scale statefulset: got %d, want 200", put.StatusCode)
	}
	put.Body.Close()

	// Ordinal 0 kept; ordinals 1 and 2 reaped.
	if !pvcExists(t, base, "data-web-0") {
		t.Fatalf("data-web-0 removed on scale-down (ordinal 0 must survive)")
	}

	for _, n := range []string{"data-web-1", "data-web-2"} {
		if pvcExists(t, base, n) {
			t.Fatalf("%s survived whenScaled=Delete scale-down (explicit reap did not fire)", n)
		}
	}
}

// TestNamespaceDelete_CascadesRegistryStores verifies the paired fix: deleting a
// namespace removes registry-backed namespaced objects (PVCs, StatefulSets, and
// custom resources), which the typed-map-only cascade previously leaked.
func TestNamespaceDelete_CascadesRegistryStores(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	// A CRD so we can put a namespaced custom resource in the doomed namespace.
	createWidgetCRD(t, base)

	nsBody := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Namespace",
		"metadata": map[string]any{"name": "doomed"},
	})

	nsResp := do(t, http.MethodPost, base+"/api/v1/namespaces", nsBody)
	if nsResp.StatusCode != http.StatusCreated {
		nsResp.Body.Close()
		t.Fatalf("create namespace: got %d, want 201", nsResp.StatusCode)
	}
	nsResp.Body.Close()

	// A StatefulSet (registry-backed) with a volumeClaimTemplate → also a PVC.
	sts := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/doomed/statefulsets",
		mustJSON(t, makeStatefulSet("web", 1, nil)))
	if sts.StatusCode != http.StatusCreated {
		sts.Body.Close()
		t.Fatalf("create statefulset in doomed: got %d, want 201", sts.StatusCode)
	}
	sts.Body.Close()

	// A custom resource.
	cr := do(t, http.MethodPost, base+"/apis/example.com/v1/namespaces/doomed/widgets",
		mustJSON(t, map[string]any{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]any{"name": "w1"},
			"spec":     map[string]any{"size": int64(3)},
		}))
	if cr.StatusCode != http.StatusCreated {
		cr.Body.Close()
		t.Fatalf("create widget in doomed: got %d, want 201", cr.StatusCode)
	}
	cr.Body.Close()

	// Sanity: everything is present before the namespace delete.
	present := map[string]string{
		"statefulset": "/apis/apps/v1/namespaces/doomed/statefulsets/web",
		"pvc":         "/api/v1/namespaces/doomed/persistentvolumeclaims/data-web-0",
		"widget":      "/apis/example.com/v1/namespaces/doomed/widgets/w1",
	}

	for kind, path := range present {
		resp := do(t, http.MethodGet, base+path, nil)
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("%s before delete: got %d, want 200", kind, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Delete the namespace.
	del := do(t, http.MethodDelete, base+"/api/v1/namespaces/doomed", nil)
	if del.StatusCode != http.StatusOK {
		del.Body.Close()
		t.Fatalf("delete namespace: got %d, want 200", del.StatusCode)
	}
	del.Body.Close()

	// Every registry-backed object in the namespace is gone.
	for kind, path := range present {
		resp := do(t, http.MethodGet, base+path, nil)
		if resp.StatusCode != http.StatusNotFound {
			resp.Body.Close()
			t.Fatalf("%s after namespace delete: got %d, want 404 (leaked)", kind, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
