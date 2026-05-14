package kubernetes_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stackshy/cloudemu/kubernetes"
)

func TestAPIServer_RegisterAndLookup(t *testing.T) {
	api := kubernetes.NewAPIServer()

	uid, state := api.RegisterCluster()
	if uid == "" {
		t.Fatal("RegisterCluster returned empty UID")
	}

	if state == nil {
		t.Fatal("RegisterCluster returned nil state")
	}

	if got := api.Lookup(uid); got != state {
		t.Fatalf("Lookup returned %p, want %p", got, state)
	}

	api.DeregisterCluster(uid)

	if got := api.Lookup(uid); got != nil {
		t.Fatalf("Lookup after deregister: got %p, want nil", got)
	}
}

func TestAPIServer_UnknownClusterReturns404(t *testing.T) {
	api := kubernetes.NewAPIServer()
	ts := httptest.NewServer(api)

	t.Cleanup(ts.Close)

	resp := mustDo(t, http.MethodGet, ts.URL+"/k8s/does-not-exist/api/v1/namespaces", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestNamespace_CreateGetListDelete(t *testing.T) {
	api := kubernetes.NewAPIServer()
	uid, _ := api.RegisterCluster()
	ts := httptest.NewServer(api)

	t.Cleanup(ts.Close)

	base := ts.URL + "/k8s/" + uid

	// Create
	ns := &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
	}
	body, _ := json.Marshal(ns)

	resp := mustDo(t, http.MethodPost, base+"/api/v1/namespaces", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()

	// Get
	resp = mustDo(t, http.MethodGet, base+"/api/v1/namespaces/demo", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status: got %d, want 200", resp.StatusCode)
	}

	got := decodeNamespace(t, resp.Body)
	if got.Name != "demo" {
		t.Fatalf("name: got %q, want demo", got.Name)
	}

	if got.UID == "" {
		t.Fatal("UID should be auto-populated")
	}

	// List — should include the three implicit namespaces plus "demo".
	resp = mustDo(t, http.MethodGet, base+"/api/v1/namespaces", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status: got %d, want 200", resp.StatusCode)
	}

	var list corev1.NamespaceList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	resp.Body.Close()

	wantNames := map[string]bool{"default": true, "kube-system": true, "kube-public": true, "demo": true}
	for _, item := range list.Items {
		delete(wantNames, item.Name)
	}

	if len(wantNames) > 0 {
		t.Fatalf("missing namespaces in list: %v", wantNames)
	}

	// Delete
	resp = mustDo(t, http.MethodDelete, base+"/api/v1/namespaces/demo", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status: got %d, want 200", resp.StatusCode)
	}

	resp.Body.Close()

	// Get after delete → 404.
	resp = mustDo(t, http.MethodGet, base+"/api/v1/namespaces/demo", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-after-delete status: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestConfigMap_LifecycleInNamespace(t *testing.T) {
	api := kubernetes.NewAPIServer()
	uid, _ := api.RegisterCluster()
	ts := httptest.NewServer(api)

	t.Cleanup(ts.Close)

	base := ts.URL + "/k8s/" + uid

	cm := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "settings"},
		Data:       map[string]string{"log_level": "debug"},
	}
	body, _ := json.Marshal(cm)

	resp := mustDo(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()

	// Patch with merge-patch+json
	patch := []byte(`{"data":{"log_level":"info","new_key":"value"}}`)
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/namespaces/default/configmaps/settings", bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status: got %d, want 200", resp.StatusCode)
	}

	patched := decodeConfigMap(t, resp.Body)
	if patched.Data["log_level"] != "info" {
		t.Fatalf("log_level after patch: got %q, want info", patched.Data["log_level"])
	}

	if patched.Data["new_key"] != "value" {
		t.Fatalf("new_key after patch: missing")
	}

	// Cascading delete on namespace removes the configmap.
	resp = mustDo(t, http.MethodDelete, base+"/api/v1/namespaces/default", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete-namespace status: got %d, want 200", resp.StatusCode)
	}

	resp.Body.Close()

	resp = mustDo(t, http.MethodGet, base+"/api/v1/namespaces/default/configmaps/settings", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get configmap after ns delete: got %d, want 404", resp.StatusCode)
	}

	resp.Body.Close()
}

// mustDo issues an HTTP request and fatally fails the test on transport error.
func mustDo(t *testing.T, method, url string, body []byte) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	return resp
}

func decodeNamespace(t *testing.T, body io.ReadCloser) corev1.Namespace {
	t.Helper()

	defer body.Close()

	var ns corev1.Namespace
	if err := json.NewDecoder(body).Decode(&ns); err != nil {
		t.Fatalf("decode namespace: %v", err)
	}

	return ns
}

func decodeConfigMap(t *testing.T, body io.ReadCloser) corev1.ConfigMap {
	t.Helper()

	defer body.Close()

	var cm corev1.ConfigMap
	if err := json.NewDecoder(body).Decode(&cm); err != nil {
		t.Fatalf("decode configmap: %v", err)
	}

	return cm
}
