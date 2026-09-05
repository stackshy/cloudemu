package apigatewayv2_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newE2E stands up a full AWS wire server backed by a real provider so the
// apigatewayv2 handler is registered alongside every other AWS service
// (notably API Gateway REST v1 and the S3 catch-all).
func newE2E(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(awsserver.NewFromProvider(cloudemu.NewAWS()))
	t.Cleanup(srv.Close)

	return srv
}

// do issues an HTTP request and returns status + decoded JSON body (nil for an
// empty body).
func do(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 {
		return resp.StatusCode, nil
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s %s (%d): %v; body=%s", method, url, resp.StatusCode, err, raw)
	}

	return resp.StatusCode, out
}

func TestAPILifecycle(t *testing.T) {
	ts := newE2E(t)
	base := ts.URL + "/v2/apis"

	status, api := do(t, http.MethodPost, base, `{"name":"tf-http-api","protocolType":"HTTP"}`)
	if status != http.StatusCreated {
		t.Fatalf("CreateApi status = %d, body=%v", status, api)
	}

	apiID, _ := api["apiId"].(string)
	if apiID == "" {
		t.Fatalf("no apiId in %v", api)
	}

	// Defaults round-trip exactly as the SDK/Terraform expect.
	wantEndpoint := "https://" + apiID + ".execute-api.us-east-1.amazonaws.com"
	if api["apiEndpoint"] != wantEndpoint {
		t.Fatalf("apiEndpoint = %v, want %v", api["apiEndpoint"], wantEndpoint)
	}

	if api["routeSelectionExpression"] != "$request.method $request.path" {
		t.Fatalf("routeSelectionExpression = %v", api["routeSelectionExpression"])
	}

	if api["disableExecuteApiEndpoint"] != false {
		t.Fatalf("disableExecuteApiEndpoint = %v, want false", api["disableExecuteApiEndpoint"])
	}

	// createdDate must be an ISO8601 string (the apigatewayv2 timestamp format).
	cd, _ := api["createdDate"].(string)
	if !strings.HasSuffix(cd, "Z") || !strings.Contains(cd, "T") {
		t.Fatalf("createdDate = %q, want ISO8601", cd)
	}

	status, _ = do(t, http.MethodGet, base+"/"+apiID, "")
	if status != http.StatusOK {
		t.Fatalf("GetApi status = %d", status)
	}

	// PATCH UpdateApi.
	status, upd := do(t, http.MethodPatch, base+"/"+apiID, `{"description":"updated"}`)
	if status != http.StatusOK || upd["description"] != "updated" {
		t.Fatalf("UpdateApi status=%d body=%v", status, upd)
	}

	status, _ = do(t, http.MethodDelete, base+"/"+apiID, "")
	if status != http.StatusNoContent {
		t.Fatalf("DeleteApi status = %d", status)
	}

	status, _ = do(t, http.MethodGet, base+"/"+apiID, "")
	if status != http.StatusNotFound {
		t.Fatalf("GetApi after delete status = %d, want 404", status)
	}
}

func TestRouteIntegrationStage(t *testing.T) {
	ts := newE2E(t)
	base := ts.URL + "/v2/apis"

	_, api := do(t, http.MethodPost, base, `{"name":"a","protocolType":"HTTP"}`)
	apiID := api["apiId"].(string)
	apiBase := base + "/" + apiID

	_, ig := do(t, http.MethodPost, apiBase+"/integrations",
		`{"integrationType":"AWS_PROXY","integrationUri":"arn:aws:lambda:x","payloadFormatVersion":"2.0"}`)
	igID, _ := ig["integrationId"].(string)
	if igID == "" || ig["connectionType"] != "INTERNET" {
		t.Fatalf("CreateIntegration body=%v", ig)
	}

	status, rt := do(t, http.MethodPost, apiBase+"/routes",
		`{"routeKey":"GET /items","target":"integrations/`+igID+`"}`)
	if status != http.StatusCreated || rt["authorizationType"] != "NONE" {
		t.Fatalf("CreateRoute status=%d body=%v", status, rt)
	}

	rtID := rt["routeId"].(string)

	status, stage := do(t, http.MethodPost, apiBase+"/stages", `{"stageName":"$default","autoDeploy":true}`)
	if status != http.StatusCreated || stage["autoDeploy"] != true {
		t.Fatalf("CreateStage status=%d body=%v", status, stage)
	}

	// List each sub-collection.
	for _, sub := range []string{"routes", "integrations", "stages"} {
		status, list := do(t, http.MethodGet, apiBase+"/"+sub, "")
		items, _ := list["items"].([]any)
		if status != http.StatusOK || len(items) != 1 {
			t.Fatalf("GET %s status=%d items=%d", sub, status, len(items))
		}
	}

	// Delete route + stage.
	if s, _ := do(t, http.MethodDelete, apiBase+"/routes/"+rtID, ""); s != http.StatusNoContent {
		t.Fatalf("DeleteRoute status = %d", s)
	}

	if s, _ := do(t, http.MethodDelete, apiBase+"/stages/$default", ""); s != http.StatusNoContent {
		t.Fatalf("DeleteStage status = %d", s)
	}
}

// TestV1NotShadowed proves the v2 /v2/apis handler does not shadow API Gateway
// REST v1 (/restapis): both are reachable on the same server, and neither falls
// through to the S3 catch-all.
func TestV1NotShadowed(t *testing.T) {
	ts := newE2E(t)

	// v1 control plane still answers with its restJson1 collection shape.
	status, v1 := do(t, http.MethodGet, ts.URL+"/restapis", "")
	if status != http.StatusOK {
		t.Fatalf("v1 GET /restapis status = %d, body=%v", status, v1)
	}

	if _, ok := v1["item"]; !ok {
		t.Fatalf("v1 GET /restapis missing 'item' key (shadowed?): %v", v1)
	}

	// v2 collection answers with its own 'items' shape.
	status, v2 := do(t, http.MethodGet, ts.URL+"/v2/apis", "")
	if status != http.StatusOK {
		t.Fatalf("v2 GET /v2/apis status = %d, body=%v", status, v2)
	}

	if _, ok := v2["items"]; !ok {
		t.Fatalf("v2 GET /v2/apis missing 'items' key: %v", v2)
	}
}
