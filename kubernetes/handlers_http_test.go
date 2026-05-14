// External HTTP-level tests covering the error paths and uncovered verbs
// for Namespace and ConfigMap handlers. Complements apiserver_test.go (the
// happy paths) so per-handler coverage clears 90%.

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

// newFixture spins up an APIServer with one registered cluster and returns
// the test URL prefix for that cluster.
func newFixture(t *testing.T) (string, func()) {
	t.Helper()

	api := kubernetes.NewAPIServer()
	uid, _ := api.RegisterCluster()
	ts := httptest.NewServer(api)
	api.SetBaseURL(ts.URL)

	return ts.URL + "/k8s/" + uid, ts.Close
}

func TestAPIServer_SetGetBaseURL(t *testing.T) {
	api := kubernetes.NewAPIServer()

	if api.BaseURL() != "" {
		t.Fatalf("default BaseURL: got %q, want empty", api.BaseURL())
	}

	api.SetBaseURL("http://example.test")

	if got := api.BaseURL(); got != "http://example.test" {
		t.Fatalf("after SetBaseURL: got %q", got)
	}
}

func TestAPIServer_Matches(t *testing.T) {
	api := kubernetes.NewAPIServer()

	cases := map[string]bool{
		"/k8s/abc/api/v1/pods": true,
		"/k8s/":                true,
		"/clusters":            false,
		"/api/v1/pods":         false,
		"/":                    false,
	}

	for path, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://x"+path, nil)
		if got := api.Matches(req); got != want {
			t.Fatalf("Matches(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestServeHTTP_RootK8sPathReturns404(t *testing.T) {
	api := kubernetes.NewAPIServer()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)

	resp := do(t, http.MethodGet, ts.URL+"/k8s/", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestServeHTTP_UnknownResourceReturns404(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/api/v1/pods", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestServeHTTP_UnparseablePathReturns404(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/garbage/path", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestNamespace_Update(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// Create
	body := mustJSON(t, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "stage"},
	})
	resp := do(t, http.MethodPost, base+"/api/v1/namespaces", body)
	resp.Body.Close()

	// Update with new labels
	updated := mustJSON(t, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "stage", Labels: map[string]string{"team": "platform"}},
	})

	resp = do(t, http.MethodPut, base+"/api/v1/namespaces/stage", updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status: got %d, want 200", resp.StatusCode)
	}

	var got corev1.Namespace
	mustDecode(t, resp.Body, &got)

	if got.Labels["team"] != "platform" {
		t.Fatalf("label after update: got %q", got.Labels["team"])
	}

	if got.ResourceVersion != "2" {
		t.Fatalf("resourceVersion after update: got %q, want 2", got.ResourceVersion)
	}
}

func TestNamespace_Patch(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	body := mustJSON(t, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
	})
	do(t, http.MethodPost, base+"/api/v1/namespaces", body).Body.Close()

	patch := []byte(`{"metadata":{"labels":{"env":"prod"}}}`)

	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/namespaces/demo", bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var got corev1.Namespace
	mustDecode(t, resp.Body, &got)

	if got.Labels["env"] != "prod" {
		t.Fatalf("label after patch: got %q", got.Labels["env"])
	}
}

func TestNamespace_DuplicateCreateReturns409(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	body := mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "dup"}})

	do(t, http.MethodPost, base+"/api/v1/namespaces", body).Body.Close()

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want 409", resp.StatusCode)
	}
}

func TestNamespace_CreateMissingNameReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	body := mustJSON(t, &corev1.Namespace{})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestNamespace_GetUpdatePatchDeleteMissing(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	for method, payload := range map[string][]byte{
		http.MethodGet:    nil,
		http.MethodPut:    mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ghost"}}),
		http.MethodPatch:  []byte(`{}`),
		http.MethodDelete: nil,
	} {
		req, _ := http.NewRequest(method, base+"/api/v1/namespaces/ghost", bytes.NewReader(payload))
		if method == http.MethodPatch {
			req.Header.Set("Content-Type", "application/merge-patch+json")
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s missing-ns status: got %d, want 404", method, resp.StatusCode)
		}

		resp.Body.Close()
	}
}

func TestNamespace_UpdateNameMismatchReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces",
		mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "a"}})).Body.Close()

	wrong := mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "b"}})

	resp := do(t, http.MethodPut, base+"/api/v1/namespaces/a", wrong)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestNamespace_MethodNotAllowed(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPut, base+"/api/v1/namespaces", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()

	do(t, http.MethodPost, base+"/api/v1/namespaces",
		mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "x"}})).Body.Close()

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/x", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("item POST: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestNamespace_BadAPIGroupReturns404(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestConfigMap_Get(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	cm := mustJSON(t, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "c"},
		Data:       map[string]string{"k": "v"},
	})
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps", cm).Body.Close()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/configmaps/c", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: got %d", resp.StatusCode)
	}

	var got corev1.ConfigMap
	mustDecode(t, resp.Body, &got)

	if got.Data["k"] != "v" {
		t.Fatalf("data: got %v", got.Data)
	}
}

func TestConfigMap_Update(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "u"},
			Data:       map[string]string{"a": "1"},
		})).Body.Close()

	resp := do(t, http.MethodPut, base+"/api/v1/namespaces/default/configmaps/u",
		mustJSON(t, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "u"},
			Data:       map[string]string{"a": "2", "b": "3"},
		}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: got %d", resp.StatusCode)
	}

	var got corev1.ConfigMap
	mustDecode(t, resp.Body, &got)

	if got.Data["a"] != "2" || got.Data["b"] != "3" {
		t.Fatalf("data after update: got %v", got.Data)
	}

	if got.ResourceVersion != "2" {
		t.Fatalf("resourceVersion: got %q, want 2", got.ResourceVersion)
	}
}

func TestConfigMap_UpdateNameMismatchReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "a"}})).Body.Close()

	resp := do(t, http.MethodPut, base+"/api/v1/namespaces/default/configmaps/a",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "b"}}))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}

func TestConfigMap_Delete(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "d"}})).Body.Close()

	resp := do(t, http.MethodDelete, base+"/api/v1/namespaces/default/configmaps/d", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	resp = do(t, http.MethodGet, base+"/api/v1/namespaces/default/configmaps/d", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-after-delete: got %d", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestConfigMap_AllNamespacesList(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces",
		mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "n1"}})).Body.Close()
	do(t, http.MethodPost, base+"/api/v1/namespaces",
		mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "n2"}})).Body.Close()
	do(t, http.MethodPost, base+"/api/v1/namespaces/n1/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "in-n1"}})).Body.Close()
	do(t, http.MethodPost, base+"/api/v1/namespaces/n2/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "in-n2"}})).Body.Close()

	resp := do(t, http.MethodGet, base+"/api/v1/configmaps", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp.StatusCode)
	}

	var list corev1.ConfigMapList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("got %d items, want 2 from across namespaces", len(list.Items))
	}
}

func TestConfigMap_AllNamespacesNonGetReturns405(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/api/v1/configmaps", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", resp.StatusCode)
	}
}

func TestConfigMap_NamespaceNotFoundReturns404(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/nope/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c"}}))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestConfigMap_DuplicateReturns409(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	body := mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "dup"}})

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps", body).Body.Close()

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want 409", resp.StatusCode)
	}
}

func TestConfigMap_CreateMissingNameReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{}))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestConfigMap_BadAPIGroupReturns404(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/configmaps", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestConfigMap_MethodNotAllowed(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// PUT on collection
	resp := do(t, http.MethodPut, base+"/api/v1/namespaces/default/configmaps", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()

	// POST on item — also not allowed
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "x"}})).Body.Close()

	resp = do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps/x", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("item POST: got %d, want 405", resp.StatusCode)
	}

	resp.Body.Close()
}

func TestConfigMap_PatchBadContentTypeReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "p"}})).Body.Close()

	req, _ := http.NewRequest(http.MethodPatch,
		base+"/api/v1/namespaces/default/configmaps/p", bytes.NewReader([]byte(`[]`)))
	req.Header.Set("Content-Type", "application/strategic-merge-patch+json")

	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestCreateNamespace_BadBodyReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces", []byte(`{not-json`))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestConfigMap_ListInNamespace(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "a"}})).Body.Close()
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "b"}})).Body.Close()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/configmaps", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp.StatusCode)
	}

	var list corev1.ConfigMapList
	mustDecode(t, resp.Body, &list)

	if len(list.Items) != 2 {
		t.Fatalf("items: got %d, want 2", len(list.Items))
	}
}

func TestConfigMap_UpdatePatchDeleteOnMissingReturns404(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	for method, payload := range map[string][]byte{
		http.MethodPut:    mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ghost"}}),
		http.MethodPatch:  []byte(`{}`),
		http.MethodDelete: nil,
	} {
		req, _ := http.NewRequest(method,
			base+"/api/v1/namespaces/default/configmaps/ghost",
			bytes.NewReader(payload),
		)
		if method == http.MethodPatch {
			req.Header.Set("Content-Type", "application/merge-patch+json")
		}

		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s missing-cm: got %d, want 404", method, resp.StatusCode)
		}

		resp.Body.Close()
	}
}

func TestConfigMap_UpdateBadBodyReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "u"}})).Body.Close()

	resp := do(t, http.MethodPut,
		base+"/api/v1/namespaces/default/configmaps/u",
		[]byte(`{not-json`),
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestConfigMap_PatchBadJSONReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "p"}})).Body.Close()

	req, _ := http.NewRequest(http.MethodPatch,
		base+"/api/v1/namespaces/default/configmaps/p",
		bytes.NewReader([]byte(`{not-json`)),
	)
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestConfigMap_PatchTypeIncompatibleReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps",
		mustJSON(t, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "t"}})).Body.Close()

	// `data` must be map[string]string; passing a scalar makes the merged
	// payload fail to unmarshal back into corev1.ConfigMap.
	req, _ := http.NewRequest(http.MethodPatch,
		base+"/api/v1/namespaces/default/configmaps/t",
		bytes.NewReader([]byte(`{"data":"not-a-map"}`)),
	)
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestNamespace_PatchOnMissingReturns404(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	req, _ := http.NewRequest(http.MethodPatch,
		base+"/api/v1/namespaces/missing",
		bytes.NewReader([]byte(`{}`)),
	)
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestNamespace_UpdateBadBodyReturns400(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/api/v1/namespaces",
		mustJSON(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "u"}})).Body.Close()

	resp := do(t, http.MethodPut, base+"/api/v1/namespaces/u", []byte(`{not-json`))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

// do issues a request and returns the response; fails the test on transport
// error.
func do(t *testing.T, method, url string, body []byte) *http.Response {
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

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return b
}

func mustDecode(t *testing.T, body io.ReadCloser, v any) {
	t.Helper()

	defer body.Close()

	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
