package apigateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// doRaw sends a request and returns the status, body and error-type header
// without failing on a non-2xx status.
func doRaw(t *testing.T, method, url, body string) (status int, raw []byte, errType string) {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	defer resp.Body.Close()

	raw, _ = io.ReadAll(resp.Body)

	return resp.StatusCode, raw, resp.Header.Get("X-Amzn-Errortype")
}

func assertWireError(t *testing.T, method, url, body string, wantStatus int, wantType, wantMsg string) {
	t.Helper()

	status, raw, errType := doRaw(t, method, url, body)
	if status != wantStatus || errType != wantType {
		t.Fatalf("%s %s = %d %s, want %d %s: %s", method, url, status, errType, wantStatus, wantType, raw)
	}

	var out struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)

	if out.Message != wantMsg {
		t.Fatalf("%s %s message = %q, want %q", method, url, out.Message, wantMsg)
	}
}

// TestExecuteAPIHostForV2APIServedByV1UntilV2DataPlane pins today's behaviour
// for an HTTP API id on an execute-api host: the v1 handler owns the host, does
// not know the id and answers 403 Missing Authentication Token. The v2 data
// plane replaces this test when it lands.
func TestExecuteAPIHostForV2APIServedByV1UntilV2DataPlane(t *testing.T) {
	srv := newE2E(t)

	api := doJSON(t, http.MethodPost, srv.URL+"/v2/apis", `{"name":"http","protocolType":"HTTP"}`)

	v2ID, _ := api["apiId"].(string)
	if v2ID == "" {
		t.Fatalf("CreateApi gave no apiId: %v", api)
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/$default/x", nil)
	req.Host = v2ID + ".execute-api.localhost"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute-api request: %v", err)
	}

	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusForbidden || string(body) != `{"message":"Missing Authentication Token"}` {
		t.Fatalf("v2 id on execute-api host = %d %s, want 403 Missing Authentication Token", resp.StatusCode, body)
	}
}

// TestE2E_DeployedStageIgnoresLiveEdits proves over the wire that a stage keeps
// serving its deployment until the API is redeployed.
func TestE2E_DeployedStageIgnoresLiveEdits(t *testing.T) {
	srv := newE2E(t)
	base := srv.URL
	apiID := buildProxyAPI(t, base)
	invoke := base + "/restapis/" + apiID + "/prod/_user_request_/pets"

	resources := doJSON(t, http.MethodGet, base+"/restapis/"+apiID+"/resources", "")
	items, _ := resources["item"].([]any)

	var proxyID string

	for _, it := range items {
		r, _ := it.(map[string]any)
		if r["path"] == "/{proxy+}" {
			proxyID, _ = r["id"].(string)
		}
	}

	deleteOK(t, base+"/restapis/"+apiID+"/resources/"+proxyID+"/methods/ANY")

	if status, raw, _ := doRaw(t, http.MethodGet, invoke, ""); status != http.StatusOK {
		t.Fatalf("deployed route after live delete = %d, want 200: %s", status, raw)
	}

	// The live API now has no methods at all, so it cannot be redeployed.
	assertWireError(t, http.MethodPost, base+"/restapis/"+apiID+"/deployments", `{"stageName":"prod"}`,
		http.StatusBadRequest, "BadRequestException", "The REST API doesn't contain any methods")
}

// TestE2E_ResourceMethodsEmbed proves GetResources lists only method names
// unless ?embed=methods asks for the full Method objects.
func TestE2E_ResourceMethodsEmbed(t *testing.T) {
	srv := newE2E(t)
	base := srv.URL
	apiID := buildProxyAPI(t, base)

	proxyMethod := func(url string) map[string]any {
		out := doJSON(t, http.MethodGet, url, "")
		items, _ := out["item"].([]any)

		for _, it := range items {
			r, _ := it.(map[string]any)
			if r["path"] == "/{proxy+}" {
				methods, _ := r["resourceMethods"].(map[string]any)
				m, _ := methods["ANY"].(map[string]any)

				return m
			}
		}

		t.Fatalf("no /{proxy+} resource in %v", out)

		return nil
	}

	if m := proxyMethod(base + "/restapis/" + apiID + "/resources"); m == nil || len(m) != 0 {
		t.Fatalf("resourceMethods without embed = %v, want {\"ANY\":{}}", m)
	}

	m := proxyMethod(base + "/restapis/" + apiID + "/resources?embed=methods")
	if m["httpMethod"] != "ANY" || m["methodIntegration"] == nil {
		t.Fatalf("resourceMethods with embed=methods = %v, want the full method", m)
	}
}

// TestE2E_DeploymentAPISummaryEmbed proves GetDeployment returns apiSummary only
// when asked with ?embed=apisummary.
func TestE2E_DeploymentAPISummaryEmbed(t *testing.T) {
	srv := newE2E(t)
	base := srv.URL
	apiID := buildProxyAPI(t, base)

	stage := doJSON(t, http.MethodGet, base+"/restapis/"+apiID+"/stages/prod", "")
	depURL := base + "/restapis/" + apiID + "/deployments/" + stage["deploymentId"].(string)

	if plain := doJSON(t, http.MethodGet, depURL, ""); plain["apiSummary"] != nil {
		t.Fatalf("apiSummary returned without embed: %v", plain)
	}

	got := doJSON(t, http.MethodGet, depURL+"?embed=apisummary", "")
	summary, _ := got["apiSummary"].(map[string]any)
	path, _ := summary["/{proxy+}"].(map[string]any)
	method, _ := path["ANY"].(map[string]any)

	if method["authorizationType"] != "NONE" {
		t.Fatalf("apiSummary = %v, want /{proxy+} ANY NONE", got["apiSummary"])
	}
}

// TestE2E_ValidationErrors checks the BadRequest and Conflict shapes the
// method, integration, deployment and stage validations return on the wire.
func TestE2E_ValidationErrors(t *testing.T) {
	srv := newE2E(t)
	base := srv.URL

	api := doJSON(t, http.MethodPost, base+"/restapis", `{"name":"v"}`)
	apiID, _ := api["id"].(string)
	rootID, _ := api["rootResourceId"].(string)
	methods := base + "/restapis/" + apiID + "/resources/" + rootID + "/methods/"

	assertWireError(t, http.MethodPost, base+"/restapis/"+apiID+"/deployments", `{"stageName":"prod"}`,
		http.StatusBadRequest, "BadRequestException", "The REST API doesn't contain any methods")

	assertWireError(t, http.MethodPut, methods+"FOO", `{"authorizationType":"NONE"}`,
		http.StatusBadRequest, "BadRequestException", "Invalid HTTP method specified")

	doJSON(t, http.MethodPut, methods+"GET", `{"authorizationType":"NONE"}`)

	assertWireError(t, http.MethodPut, methods+"GET", `{"authorizationType":"NONE"}`,
		http.StatusConflict, "ConflictException", "Method already exists for this resource")

	assertWireError(t, http.MethodPost, base+"/restapis/"+apiID+"/deployments", `{"stageName":"prod"}`,
		http.StatusBadRequest, "BadRequestException", "No integration defined for method")

	assertWireError(t, http.MethodPut, methods+"GET/integration", `{"type":"HTTP","uri":"http://example.com"}`,
		http.StatusBadRequest, "BadRequestException", "Enumeration value for HttpMethod must be non-empty")

	doJSON(t, http.MethodPut, methods+"GET/integration", `{"type":"MOCK"}`)

	dep := doJSON(t, http.MethodPost, base+"/restapis/"+apiID+"/deployments", `{"stageName":"prod"}`)
	depID, _ := dep["id"].(string)

	assertWireError(t, http.MethodPost, base+"/restapis/"+apiID+"/stages", `{"stageName":"prod","deploymentId":"`+depID+`"}`,
		http.StatusConflict, "ConflictException", "Stage already exists")
}
