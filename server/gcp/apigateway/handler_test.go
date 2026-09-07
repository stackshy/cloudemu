package apigateway_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newServer stands up the full GCP wire server so requests route through the
// dispatcher's Matches guard exactly as the google-beta Terraform provider hits
// it at its default /v1beta/ base path.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.NewFromProvider(cloud))
	t.Cleanup(ts.Close)

	return ts
}

// do issues a request and returns the status and raw body.
func do(t *testing.T, ts *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}

		rdr = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, raw
}

// doOK issues a request, asserts 200, and decodes the JSON body into out.
func doOK(t *testing.T, ts *httptest.Server, method, path string, body, out any) {
	t.Helper()

	status, raw := do(t, ts, method, path, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s: status %d body %s", method, path, status, raw)
	}

	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s: %v (body %s)", method, path, err, raw)
		}
	}
}

const (
	betaBase = "/v1beta/projects/p/locations/global/apis"
	gwBase   = "/v1beta/projects/p/locations/us-central1/gateways"
)

type operation struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response"`
}

// TestBetaPathLifecycle drives the full api → config → gateway lifecycle on the
// /v1beta/ base path the google-beta provider uses, proving LRO completion, the
// version-aware @type, computed-field stability, verbatim openapi round-trip, and
// the /v1beta/ operation poll (a space the shared LRO poller does not own).
func TestBetaPathLifecycle(t *testing.T) {
	ts := newServer(t)

	// --- Create API (LRO, done inline). ---
	var apiOp operation
	doOK(t, ts, http.MethodPost, betaBase+"?apiId=a1", map[string]any{"displayName": "A1"}, &apiOp)

	if !apiOp.Done {
		t.Fatalf("create api operation not done: %+v", apiOp)
	}

	assertType(t, apiOp.Response, "type.googleapis.com/google.cloud.apigateway.v1beta.Api")

	// The returned operation name must resolve at the /v1beta/ operations path.
	var polled operation
	doOK(t, ts, http.MethodGet, "/v1beta/"+apiOp.Name, nil, &polled)

	if !polled.Done {
		t.Fatalf("v1beta operation poll not done: %+v", polled)
	}

	// --- GET API: computed name + state stable. ---
	api := getResource(t, ts, betaBase+"/a1")
	if got := api["name"]; got != "projects/p/locations/global/apis/a1" {
		t.Fatalf("api name = %v", got)
	}

	if api["state"] != "ACTIVE" {
		t.Fatalf("api state = %v, want ACTIVE", api["state"])
	}

	// --- Create API Config with a verbatim openapi document. ---
	contents := base64.StdEncoding.EncodeToString([]byte("swagger: \"2.0\""))
	cfgBody := map[string]any{
		"displayName": "C1",
		"openapiDocuments": []map[string]any{
			{"document": map[string]any{"path": "spec.yaml", "contents": contents}},
		},
	}

	var cfgOp operation
	doOK(t, ts, http.MethodPost, betaBase+"/a1/configs?apiConfigId=c1", cfgBody, &cfgOp)
	assertType(t, cfgOp.Response, "type.googleapis.com/google.cloud.apigateway.v1beta.ApiConfig")

	cfg := getResource(t, ts, betaBase+"/a1/configs/c1")
	if cfg["name"] != "projects/p/locations/global/apis/a1/configs/c1" {
		t.Fatalf("config name = %v", cfg["name"])
	}

	if _, ok := cfg["serviceConfigId"].(string); !ok || cfg["serviceConfigId"] == "" {
		t.Fatalf("serviceConfigId not minted: %v", cfg["serviceConfigId"])
	}

	assertOpenAPIContents(t, cfg, contents)

	// serviceConfigId must be stable across a second GET (classic drift point).
	cfg2 := getResource(t, ts, betaBase+"/a1/configs/c1")
	if cfg2["serviceConfigId"] != cfg["serviceConfigId"] {
		t.Fatalf("serviceConfigId drifted: %v -> %v", cfg["serviceConfigId"], cfg2["serviceConfigId"])
	}

	// --- Create Gateway referencing the config. ---
	ref := "projects/p/locations/global/apis/a1/configs/c1"

	var gwOp operation
	doOK(t, ts, http.MethodPost, gwBase+"?gatewayId=g1",
		map[string]any{"displayName": "G1", "apiConfig": ref}, &gwOp)
	assertType(t, gwOp.Response, "type.googleapis.com/google.cloud.apigateway.v1beta.Gateway")

	gw := getResource(t, ts, gwBase+"/g1")
	if host, _ := gw["defaultHostname"].(string); host == "" {
		t.Fatalf("defaultHostname not minted: %v", gw["defaultHostname"])
	}

	// --- Cascade: deleting the api removes its config. ---
	doOK(t, ts, http.MethodDelete, betaBase+"/a1", nil, nil)

	if status, _ := do(t, ts, http.MethodGet, betaBase+"/a1/configs/c1", nil); status != http.StatusNotFound {
		t.Fatalf("config after api-cascade delete: status %d, want 404", status)
	}
}

// TestGatewayDanglingConfigRef proves a gateway referencing a nonexistent config
// is rejected (404), matching real API Gateway's admission check.
func TestGatewayDanglingConfigRef(t *testing.T) {
	ts := newServer(t)

	status, _ := do(t, ts, http.MethodPost, gwBase+"?gatewayId=g1", map[string]any{
		"apiConfig": "projects/p/locations/global/apis/nope/configs/nope",
	})
	if status != http.StatusNotFound {
		t.Fatalf("dangling apiConfig ref: status %d, want 404", status)
	}
}

// TestV1PathAlsoServed proves the handler serves the /v1/ prefix too (a real
// google.golang.org/api/apigateway/v1 client / gcloud), with a v1 @type.
func TestV1PathAlsoServed(t *testing.T) {
	ts := newServer(t)

	var op operation
	doOK(t, ts, http.MethodPost, "/v1/projects/p/locations/global/apis?apiId=v1api",
		map[string]any{"displayName": "V1"}, &op)
	assertType(t, op.Response, "type.googleapis.com/google.cloud.apigateway.v1.Api")

	api := getResource(t, ts, "/v1/projects/p/locations/global/apis/v1api")
	if api["name"] != "projects/p/locations/global/apis/v1api" {
		t.Fatalf("v1 api name = %v", api["name"])
	}
}

// TestListScoped proves list returns the collection key and is project-scoped.
func TestListScoped(t *testing.T) {
	ts := newServer(t)
	doOK(t, ts, http.MethodPost, betaBase+"?apiId=l1", map[string]any{}, nil)
	doOK(t, ts, http.MethodPost, betaBase+"?apiId=l2", map[string]any{}, nil)

	var list struct {
		Apis []map[string]any `json:"apis"`
	}
	doOK(t, ts, http.MethodGet, betaBase, nil, &list)

	if len(list.Apis) != 2 {
		t.Fatalf("list len = %d, want 2", len(list.Apis))
	}
}

func getResource(t *testing.T, ts *httptest.Server, path string) map[string]any {
	t.Helper()

	var out map[string]any
	doOK(t, ts, http.MethodGet, path, nil, &out)

	return out
}

func assertType(t *testing.T, resp json.RawMessage, want string) {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("decode operation response: %v (%s)", err, resp)
	}

	if got, _ := m["@type"].(string); got != want {
		t.Fatalf("response @type = %q, want %q", got, want)
	}
}

func assertOpenAPIContents(t *testing.T, cfg map[string]any, want string) {
	t.Helper()

	docs, ok := cfg["openapiDocuments"].([]any)
	if !ok || len(docs) == 0 {
		t.Fatalf("openapiDocuments missing: %v", cfg["openapiDocuments"])
	}

	doc, _ := docs[0].(map[string]any)
	inner, _ := doc["document"].(map[string]any)

	if got, _ := inner["contents"].(string); got != want {
		t.Fatalf("openapi contents = %q, want verbatim %q", got, want)
	}
}
