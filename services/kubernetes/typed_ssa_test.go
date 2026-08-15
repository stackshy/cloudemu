// Server-side apply, apply-create, and finalizers for the typed core kinds
// (ConfigMap/Secret/Service/Deployment) — they must behave the way registry-
// backed kinds already do.
package kubernetes_test

import (
	"io"
	"net/http"
	"testing"
)

// managedFieldsOf pulls metadata.managedFields out of a decoded object.
func managedFieldsOfObj(t *testing.T, obj map[string]any) []any {
	t.Helper()

	meta, _ := obj["metadata"].(map[string]any)
	mf, _ := meta["managedFields"].([]any)

	return mf
}

// hasApplyEntry reports whether managedFields carries an Apply entry for manager.
func hasApplyEntry(mf []any, manager string) bool {
	for _, e := range mf {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}

		if em["manager"] == manager && em["operation"] == "Apply" {
			return true
		}
	}

	return false
}

// TestTypedServerSideApply_OwnershipAndConflict: applying to a typed ConfigMap
// must record managedFields and a second manager changing an owned field must
// 409 (unless force). Today the typed PATCH path treats apply-patch as a plain
// merge, so no ownership is recorded and the conflicting apply silently wins.
func TestTypedServerSideApply_OwnershipAndConflict(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	collURL := base + "/api/v1/namespaces/default/configmaps"
	cmURL := collURL + "/owned"

	// Seed the object first so this isolates ownership/conflict on an existing
	// object (apply-create is covered by TestServerSideApplyCreate).
	seed := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "owned"},
	})
	if c := do(t, http.MethodPost, collURL, seed); c.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(c.Body)
		c.Body.Close()
		t.Fatalf("seed ConfigMap: got %d, want 201 (%s)", c.StatusCode, body)
	} else {
		c.Body.Close()
	}

	alice := apply(t, cmURL, "alice", false, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "owned"},
		"data":     map[string]any{"foo": "1"},
	})
	if alice.StatusCode != http.StatusOK && alice.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(alice.Body)
		alice.Body.Close()
		t.Fatalf("alice apply: got %d, want 200/201 (%s)", alice.StatusCode, body)
	}

	obj := decodeMap(t, alice.Body)
	alice.Body.Close()

	if mf := managedFieldsOfObj(t, obj); !hasApplyEntry(mf, "alice") {
		t.Fatalf("typed apply did not record an Apply managedFields entry for alice: %v", mf)
	}

	// bob changes alice's owned field without force -> conflict 409.
	bob := apply(t, cmURL, "bob", false, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "owned"},
		"data":     map[string]any{"foo": "2"},
	})
	bobStatus := bob.StatusCode
	bob.Body.Close()

	if bobStatus != http.StatusConflict {
		t.Fatalf("bob apply over alice's field: got %d, want 409 Conflict", bobStatus)
	}

	// bob with force -> ownership transfers, 200.
	bobF := apply(t, cmURL, "bob", true, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "owned"},
		"data":     map[string]any{"foo": "2"},
	})
	bobFStatus := bobF.StatusCode
	bobF.Body.Close()

	if bobFStatus != http.StatusOK {
		t.Fatalf("bob apply with force: got %d, want 200", bobFStatus)
	}
}

// TestServerSideApplyCreate: apply-patch to a non-existent object must create it,
// for both a typed kind and a registry kind. Today both return 404.
func TestServerSideApplyCreate(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	// Typed: apply to a ConfigMap that does not exist yet.
	cmURL := base + "/api/v1/namespaces/default/configmaps/fresh"
	tr := apply(t, cmURL, "kubectl", false, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "fresh"},
		"data":     map[string]any{"k": "v"},
	})
	trStatus := tr.StatusCode
	tr.Body.Close()

	if trStatus != http.StatusOK && trStatus != http.StatusCreated {
		t.Fatalf("apply-create typed ConfigMap: got %d, want 200/201 (create-on-apply)", trStatus)
	}

	g := do(t, http.MethodGet, cmURL, nil)
	if g.StatusCode != http.StatusOK {
		g.Body.Close()
		t.Fatalf("GET after apply-create ConfigMap: got %d, want 200", g.StatusCode)
	}

	got := decodeMap(t, g.Body)
	g.Body.Close()

	if mf := managedFieldsOfObj(t, got); !hasApplyEntry(mf, "kubectl") {
		t.Fatalf("apply-created ConfigMap has no Apply managedFields entry for kubectl: %v", mf)
	}

	// Typed workload: apply-create a Deployment (the headline `kubectl apply
	// --server-side -f deployment.yaml` path) — must create and reconcile.
	depURL := base + "/apis/apps/v1/namespaces/default/deployments/web"
	dr := apply(t, depURL, "kubectl", false, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
				},
			},
		},
	})
	drStatus := dr.StatusCode
	dr.Body.Close()

	if drStatus != http.StatusOK && drStatus != http.StatusCreated {
		t.Fatalf("apply-create Deployment: got %d, want 200/201", drStatus)
	}

	if gd := do(t, http.MethodGet, depURL, nil); gd.StatusCode != http.StatusOK {
		gd.Body.Close()
		t.Fatalf("GET after apply-create Deployment: got %d, want 200", gd.StatusCode)
	} else {
		gd.Body.Close()
	}

	// The headline of apply-create is that it preserves per-kind reconcile — the
	// Deployment must materialize its Pods, not just store the object. Assert the
	// child Pod count == spec.replicas so a future change to the apply-create
	// path can't silently stop reconciling while this test stays green.
	pl := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods?labelSelector=app%3Dweb", nil)
	pods := decodeMap(t, pl.Body)
	pl.Body.Close()

	if items, _ := pods["items"].([]any); len(items) != 1 {
		t.Fatalf("apply-create Deployment materialized %d Pods, want 1 (spec.replicas)", len(items))
	}

	// Registry: apply to a NetworkPolicy that does not exist yet.
	npURL := base + "/apis/networking.k8s.io/v1/namespaces/default/networkpolicies/freshnp"
	rr := apply(t, npURL, "kubectl", false, map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "freshnp"},
		"spec":     map[string]any{"podSelector": map[string]any{}},
	})
	rrStatus := rr.StatusCode
	rr.Body.Close()

	if rrStatus != http.StatusOK && rrStatus != http.StatusCreated {
		t.Fatalf("apply-create registry NetworkPolicy: got %d, want 200/201 (create-on-apply)", rrStatus)
	}
}

// identityOf pulls the server-owned uid + creationTimestamp out of an object.
func identityOf(t *testing.T, obj map[string]any) (uid, creation string) {
	t.Helper()

	meta, _ := obj["metadata"].(map[string]any)
	uid, _ = meta["uid"].(string)
	creation, _ = meta["creationTimestamp"].(string)

	return uid, creation
}

// TestTypedApply_PreservesServerMetadata: an apply body that carries
// `creationTimestamp: null` (kubectl always sends it) or omits `uid` must NOT
// blank the server-owned identity metadata. This re-GETs after the apply — the
// exact check that catches the metadata-drop the apply path used to have. Run
// across two typed kinds so it isn't ConfigMap-specific.
func TestTypedApply_PreservesServerMetadata(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	for _, tc := range []struct {
		name, kind, coll string
	}{
		{"configmap", "ConfigMap", "/api/v1/namespaces/default/configmaps"},
		{"secret", "Secret", "/api/v1/namespaces/default/secrets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collURL := base + tc.coll
			itemURL := collURL + "/meta"

			// Create it and capture the server-assigned identity.
			create := mustJSON(t, map[string]any{
				"apiVersion": "v1", "kind": tc.kind,
				"metadata": map[string]any{"name": "meta"},
			})
			c := do(t, http.MethodPost, collURL, create)
			if c.StatusCode != http.StatusCreated {
				b, _ := io.ReadAll(c.Body)
				c.Body.Close()
				t.Fatalf("create %s: got %d, want 201 (%s)", tc.kind, c.StatusCode, b)
			}
			uid, creation := identityOf(t, decodeMap(t, c.Body))
			c.Body.Close()

			if uid == "" || creation == "" {
				t.Fatalf("created %s missing uid/creationTimestamp: uid=%q creation=%q", tc.kind, uid, creation)
			}

			// Apply with an explicit creationTimestamp:null (kubectl's behavior)
			// plus a real change.
			r := apply(t, itemURL, "kubectl", false, map[string]any{
				"apiVersion": "v1", "kind": tc.kind,
				"metadata": map[string]any{
					"name":              "meta",
					"creationTimestamp": nil,
					"labels":            map[string]any{"applied": "yes"},
				},
			})
			if r.StatusCode != http.StatusOK && r.StatusCode != http.StatusCreated {
				b, _ := io.ReadAll(r.Body)
				r.Body.Close()
				t.Fatalf("apply %s: got %d, want 200/201 (%s)", tc.kind, r.StatusCode, b)
			}
			r.Body.Close()

			// Re-GET: identity must be exactly what create assigned.
			g := do(t, http.MethodGet, itemURL, nil)
			got := decodeMap(t, g.Body)
			g.Body.Close()

			gUID, gCreation := identityOf(t, got)
			if gUID != uid {
				t.Fatalf("%s uid changed across apply: %q -> %q", tc.kind, uid, gUID)
			}
			if gCreation != creation {
				t.Fatalf("%s creationTimestamp blanked/changed across apply: %q -> %q", tc.kind, creation, gCreation)
			}
		})
	}
}

// TestTypedApply_TwoManagerRetention: the core SSA property — two managers each
// owning a different field both survive. alice applies data.x, bob applies
// data.y (disjoint, so no conflict); a re-GET must show both.
func TestTypedApply_TwoManagerRetention(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	collURL := base + "/api/v1/namespaces/default/configmaps"
	itemURL := collURL + "/shared"

	seed := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "shared"},
	})
	do(t, http.MethodPost, collURL, seed).Body.Close()

	a := apply(t, itemURL, "alice", false, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "shared"},
		"data":     map[string]any{"x": "1"},
	})
	a.Body.Close()

	b := apply(t, itemURL, "bob", false, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "shared"},
		"data":     map[string]any{"y": "2"},
	})
	if b.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(b.Body)
		b.Body.Close()
		t.Fatalf("bob apply (disjoint field): got %d, want 200 (%s)", b.StatusCode, body)
	}
	b.Body.Close()

	g := do(t, http.MethodGet, itemURL, nil)
	got := decodeMap(t, g.Body)
	g.Body.Close()

	data, _ := got["data"].(map[string]any)
	if data["x"] != "1" || data["y"] != "2" {
		t.Fatalf("two-manager retention failed: both fields should survive, got data=%v", data)
	}
}

// TestTypedFinalizerGatedDelete: deleting a typed ConfigMap that carries a
// finalizer must leave it Terminating (deletionTimestamp set), not hard-delete
// it; removing the finalizer then completes the delete. Today it is hard-deleted.
func TestTypedFinalizerGatedDelete(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	collURL := base + "/api/v1/namespaces/default/configmaps"
	itemURL := collURL + "/guarded"

	create := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{
			"name":       "guarded",
			"finalizers": []any{"cloudemu.dev/hold"},
		},
		"data": map[string]any{"k": "v"},
	})
	if c := do(t, http.MethodPost, collURL, create); c.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(c.Body)
		c.Body.Close()
		t.Fatalf("create guarded ConfigMap: got %d, want 201 (%s)", c.StatusCode, body)
	} else {
		c.Body.Close()
	}

	// DELETE must not remove it — it should go Terminating.
	del := do(t, http.MethodDelete, itemURL, nil)
	del.Body.Close()

	g := do(t, http.MethodGet, itemURL, nil)
	if g.StatusCode != http.StatusOK {
		g.Body.Close()
		t.Fatalf("GET after finalizer-gated delete: got %d, want 200 (object should be Terminating)", g.StatusCode)
	}

	obj := decodeMap(t, g.Body)
	g.Body.Close()

	meta, _ := obj["metadata"].(map[string]any)
	if meta["deletionTimestamp"] == nil {
		t.Fatalf("finalizer-gated delete did not set deletionTimestamp; object was hard-deleted")
	}

	// Remove the finalizer -> delete completes.
	clear := mustJSON(t, map[string]any{"metadata": map[string]any{"finalizers": []any{}}})
	if p := patchReq(t, itemURL, "application/merge-patch+json", clear); p.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(p.Body)
		p.Body.Close()
		t.Fatalf("remove finalizer: got %d, want 200 (%s)", p.StatusCode, body)
	} else {
		p.Body.Close()
	}

	if g2 := do(t, http.MethodGet, itemURL, nil); g2.StatusCode != http.StatusNotFound {
		g2.Body.Close()
		t.Fatalf("GET after draining finalizer: got %d, want 404 (object should be gone)", g2.StatusCode)
	} else {
		g2.Body.Close()
	}
}
