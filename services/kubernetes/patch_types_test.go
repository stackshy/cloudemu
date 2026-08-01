package kubernetes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// kubectl uses strategic-merge-patch for `set`/`edit`/`label` and json-patch
// for `patch --type=json`; client-go uses merge-patch. All must work on the
// typed handlers (Deployment) — previously only merge-patch was accepted.

func patchReq(t *testing.T, url, contentType string, body []byte) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	return resp
}

func TestTypedPatch_StrategicAndJSONPatch(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	ns := do(t, http.MethodPost, base+"/api/v1/namespaces",
		[]byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"default"}}`))
	ns.Body.Close()

	dep := []byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web"},` +
		`"spec":{"replicas":1,"selector":{"matchLabels":{"app":"web"}},` +
		`"template":{"metadata":{"labels":{"app":"web"}},"spec":{"containers":[` +
		`{"name":"web","image":"nginx:1.27","ports":[{"containerPort":80}]}]}}}}`)
	c := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", dep)
	c.Body.Close()
	if c.StatusCode != http.StatusCreated {
		t.Fatalf("create deployment: status %d", c.StatusCode)
	}

	depURL := base + "/apis/apps/v1/namespaces/default/deployments/web"

	// strategic-merge-patch (kubectl set image) — the container list merges by
	// name, so the image changes while the existing port is preserved.
	smp := []byte(`{"spec":{"template":{"spec":{"containers":[{"name":"web","image":"nginx:1.28"}]}}}}`)
	resp := patchReq(t, depURL, "application/strategic-merge-patch+json", smp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("strategic-merge patch: status %d", resp.StatusCode)
	}

	img, port := deployImageAndPort(t, base)
	if img != "nginx:1.28" {
		t.Fatalf("image after strategic merge = %q, want nginx:1.28", img)
	}
	if port != 80 {
		t.Fatalf("containerPort after strategic merge = %d, want 80 (strategic merge must not drop it)", port)
	}

	// json-patch (RFC 6902) — replace replicas.
	jp := []byte(`[{"op":"replace","path":"/spec/replicas","value":3}]`)
	resp2 := patchReq(t, depURL, "application/json-patch+json", jp)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("json-patch: status %d", resp2.StatusCode)
	}
	if r := deployReplicas(t, base); r != 3 {
		t.Fatalf("replicas after json-patch = %d, want 3", r)
	}
}

func deployImageAndPort(t *testing.T, base string) (string, int) {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/web", nil)
	defer resp.Body.Close()

	var dep struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Image string `json:"image"`
						Ports []struct {
							ContainerPort int `json:"containerPort"`
						} `json:"ports"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	c := dep.Spec.Template.Spec.Containers
	if len(c) == 0 {
		t.Fatal("deployment has no containers")
	}
	port := 0
	if len(c[0].Ports) > 0 {
		port = c[0].Ports[0].ContainerPort
	}

	return c[0].Image, port
}

func deployReplicas(t *testing.T, base string) int {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/web", nil)
	defer resp.Body.Close()

	var dep struct {
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}

	return dep.Spec.Replicas
}

// TestOpenAPI_Endpoints guards the OpenAPI documents kubectl validates against:
// a v3 discovery root, a per-group v3 doc carrying the GVK, and a v2 document
// served as protobuf bytes (with a mime-safe content type) for the legacy path.
func TestOpenAPI_Endpoints(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// v3 root lists the apps group.
	root := do(t, http.MethodGet, base+"/openapi/v3", nil)
	defer root.Body.Close()
	var rootDoc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.NewDecoder(root.Body).Decode(&rootDoc); err != nil {
		t.Fatalf("decode v3 root: %v", err)
	}
	if _, ok := rootDoc.Paths["apis/apps/v1"]; !ok {
		t.Fatalf("v3 root missing apis/apps/v1; got %v", rootDoc.Paths)
	}

	// v3 apps doc carries the Deployment GVK so kubectl resolves it (and does
	// not fall back to the protobuf v2 path).
	doc := do(t, http.MethodGet, base+"/openapi/v3/apis/apps/v1", nil)
	defer doc.Body.Close()
	body, _ := readAll(t, doc)
	if !bytes.Contains(body, []byte(`"kind":"Deployment"`)) {
		t.Fatalf("v3 apps doc missing Deployment GVK: %s", body)
	}

	// v2 protobuf: mime-safe content type + non-empty body kubectl can unmarshal.
	req, _ := http.NewRequest(http.MethodGet, base+"/openapi/v2", nil)
	req.Header.Set("Accept", "application/com.github.proto-openapi.spec.v2@v1.0+protobuf")
	v2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get v2 protobuf: %v", err)
	}
	defer v2.Body.Close()
	if ct := v2.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("v2 protobuf content-type = %q, want application/octet-stream (mime-safe)", ct)
	}
	pb, _ := readAll(t, v2)
	if len(pb) == 0 {
		t.Fatal("v2 protobuf body is empty")
	}
}

func readAll(t *testing.T, resp *http.Response) ([]byte, error) {
	t.Helper()

	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(resp.Body)

	return buf.Bytes(), err
}
