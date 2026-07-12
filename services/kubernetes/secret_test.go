package kubernetes_test

import (
	"bytes"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSecret_LifecycleAndStringDataMerge(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Create with StringData — should merge into Data on persist.
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds"},
		StringData: map[string]string{"username": "admin", "password": "s3cret"},
		Data:       map[string][]byte{"token": []byte("preexisting")},
	}

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/secrets", mustJSON(t, sec))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d, want 201", resp.StatusCode)
	}

	var created corev1.Secret
	mustDecode(t, resp.Body, &created)

	if len(created.StringData) != 0 {
		t.Fatalf("StringData should be cleared after merge, got %v", created.StringData)
	}

	if string(created.Data["username"]) != "admin" {
		t.Fatalf("username: got %q", string(created.Data["username"]))
	}

	if string(created.Data["password"]) != "s3cret" {
		t.Fatalf("password: got %q", string(created.Data["password"]))
	}

	if string(created.Data["token"]) != "preexisting" {
		t.Fatalf("token preserved: got %q", string(created.Data["token"]))
	}

	if created.Type != corev1.SecretTypeOpaque {
		t.Fatalf("default Type: got %q, want Opaque", created.Type)
	}

	// Get
	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/secrets/creds", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Update via PUT — re-introduce StringData, expect another merge + type preservation.
	updated := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds"},
		StringData: map[string]string{"password": "rotated"},
		// Type omitted — should preserve existing.
	}

	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/secrets/creds", mustJSON(t, updated))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: got %d", resp.StatusCode)
	}

	var got corev1.Secret
	mustDecode(t, resp.Body, &got)

	if string(got.Data["password"]) != "rotated" {
		t.Fatalf("password after update: got %q", string(got.Data["password"]))
	}

	if got.Type != corev1.SecretTypeOpaque {
		t.Fatalf("Type preserved on update: got %q", got.Type)
	}

	if got.ResourceVersion != "2" {
		t.Fatalf("RV after update: got %q, want 2", got.ResourceVersion)
	}

	// Patch
	patch := []byte(`{"data":{"extra":"ZXh0cmEtdmFsdWU="}}`) // base64("extra-value")
	req, _ := http.NewRequest(http.MethodPatch,
		base+"/api/v1/namespaces/default/secrets/creds", bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}

	var patched corev1.Secret
	mustDecode(t, resp.Body, &patched)

	if string(patched.Data["extra"]) != "extra-value" {
		t.Fatalf("patched data.extra: got %q", string(patched.Data["extra"]))
	}

	// Delete
	resp = do(t, http.MethodDelete, base+"/api/v1/namespaces/default/secrets/creds", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/secrets/creds", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-after-delete: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestSecret_AllNamespacesList(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	for _, ns := range []string{"default", "kube-system"} {
		do(t, http.MethodPost, base+"/api/v1/namespaces/"+ns+"/secrets",
			mustJSON(t, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "s-" + ns},
				StringData: map[string]string{"k": "v"},
			})).Body.Close()
	}

	resp := do(t, http.MethodGet, base+"/api/v1/secrets", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("all-ns list: got %d", resp.StatusCode)
	}

	var list corev1.SecretList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("all-ns list: got %d items", len(list.Items))
	}
}

func TestSecret_ErrorPaths(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Missing namespace.
	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/nope/secrets",
		mustJSON(t, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s"}}))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing-ns: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Missing name.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/secrets",
		mustJSON(t, &corev1.Secret{}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-name: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Duplicate.
	body := mustJSON(t, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "dup"}})
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/secrets", body).Body.Close()

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/secrets", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT name mismatch.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/secrets/dup",
		mustJSON(t, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "diff"}}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("name-mismatch: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// All-ns non-GET.
	resp = do(t, http.MethodPost, base+"/api/v1/secrets", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("cluster POST: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Bad API group.
	resp = do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/secrets", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong-group: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Collection PUT.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/secrets", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Item POST.
	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/secrets/dup", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("item POST: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// PUT bad body.
	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/default/secrets/dup", []byte(`{not`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("put bad-body: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Get/Update/Delete/Patch on missing.
	for method, payload := range map[string][]byte{
		http.MethodGet:    nil,
		http.MethodPut:    mustJSON(t, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ghost"}}),
		http.MethodPatch:  []byte(`{}`),
		http.MethodDelete: nil,
	} {
		req, _ := http.NewRequest(method,
			base+"/api/v1/namespaces/default/secrets/ghost", bytes.NewReader(payload))
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
