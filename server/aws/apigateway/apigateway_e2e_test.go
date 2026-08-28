package apigateway_test

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
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// newE2E stands up a full AWS wire server (API Gateway + Lambda both registered)
// backed by a real provider, and registers a Lambda that returns an API Gateway
// proxy-shaped response echoing the request path.
func newE2E(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewAWS()

	if _, err := cloud.Lambda.CreateFunction(context.Background(), sdrv.FunctionConfig{
		Name: "hello", Runtime: "go1.x", Handler: "main",
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// The handler receives the proxy event and returns a proxy response whose
	// body echoes the event's path, proving the whole request reached Lambda.
	cloud.Lambda.RegisterHandler("hello", func(_ context.Context, payload []byte) ([]byte, error) {
		var event struct {
			Path       string `json:"path"`
			HTTPMethod string `json:"httpMethod"`
		}
		_ = json.Unmarshal(payload, &event)

		body, _ := json.Marshal(map[string]string{"seen": event.HTTPMethod + " " + event.Path})

		resp, _ := json.Marshal(map[string]any{
			"statusCode": 200,
			"headers":    map[string]string{"Content-Type": "application/json"},
			"body":       string(body),
		})

		return resp, nil
	})

	srv := httptest.NewServer(awsserver.NewFromProvider(cloud))
	t.Cleanup(srv.Close)

	return srv
}

func doJSON(t *testing.T, method, url, body string) map[string]any {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s -> %d: %s", method, url, resp.StatusCode, raw)
	}

	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}

	return out
}

const lambdaURI = "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/" +
	"arn:aws:lambda:us-east-1:123456789012:function:hello/invocations"

// buildProxyAPI drives the control plane over HTTP to build a REST API with a
// {proxy+} AWS_PROXY integration to the hello function, deployed to "prod", and
// returns the api id.
func buildProxyAPI(t *testing.T, base string) string {
	t.Helper()

	api := doJSON(t, http.MethodPost, base+"/restapis", `{"name":"petstore"}`)
	apiID, _ := api["id"].(string)
	rootID, _ := api["rootResourceId"].(string)

	if apiID == "" || rootID == "" {
		t.Fatalf("CreateRestApi gave no id/rootResourceId: %v", api)
	}

	res := doJSON(t, http.MethodPost, base+"/restapis/"+apiID+"/resources/"+rootID, `{"pathPart":"{proxy+}"}`)
	resID, _ := res["id"].(string)

	doJSON(t, http.MethodPut, base+"/restapis/"+apiID+"/resources/"+resID+"/methods/ANY",
		`{"authorizationType":"NONE"}`)

	doJSON(t, http.MethodPut, base+"/restapis/"+apiID+"/resources/"+resID+"/methods/ANY/integration",
		`{"type":"AWS_PROXY","integrationHttpMethod":"POST","uri":"`+lambdaURI+`"}`)

	doJSON(t, http.MethodPost, base+"/restapis/"+apiID+"/deployments", `{"stageName":"prod"}`)

	return apiID
}

// TestE2E_RequestReachesLambdaAndReturnsResponse is the headline flow: build a
// REST API + Lambda proxy integration, deploy it, then make an HTTP request to
// the execute-api URL and assert the Lambda ran and its {statusCode,body} came
// back.
func TestE2E_RequestReachesLambdaAndReturnsResponse(t *testing.T) {
	srv := newE2E(t)
	apiID := buildProxyAPI(t, srv.URL)

	// Path-form execute-api URL: /restapis/{id}/{stage}/_user_request_/{path}
	url := srv.URL + "/restapis/" + apiID + "/prod/_user_request_/pets/123"

	resp, err := http.Get(url) //nolint:noctx // test
	if err != nil {
		t.Fatalf("GET data plane: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("data-plane status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	var out struct {
		Seen string `json:"seen"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("response not JSON: %v (%s)", err, body)
	}

	if out.Seen != "GET /pets/123" {
		t.Fatalf("lambda did not see the request path; got %q, want %q", out.Seen, "GET /pets/123")
	}
}

// TestE2E_HostFormRouting exercises the "{apiId}.execute-api" Host addressing.
func TestE2E_HostFormRouting(t *testing.T) {
	srv := newE2E(t)
	apiID := buildProxyAPI(t, srv.URL)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/prod/orders", strings.NewReader("{}"))
	req.Host = apiID + ".execute-api.us-east-1.amazonaws.com"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("host-form request: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("host-form status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	var out struct {
		Seen string `json:"seen"`
	}
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &out)

	if out.Seen != "POST /orders" {
		t.Fatalf("host-form routing wrong; got %q, want %q", out.Seen, "POST /orders")
	}
}

// TestE2E_UndefinedRouteForbidden asserts an unmatched data-plane route returns
// 403 with the Missing Authentication Token body, like real API Gateway.
func TestE2E_UndefinedRouteForbidden(t *testing.T) {
	srv := newE2E(t)

	// A REST API that exists but has no matching resource for this path.
	api := doJSON(t, http.MethodPost, srv.URL+"/restapis", `{"name":"empty"}`)
	apiID, _ := api["id"].(string)
	doJSON(t, http.MethodPost, srv.URL+"/restapis/"+apiID+"/deployments", `{"stageName":"prod"}`)

	resp, err := http.Get(srv.URL + "/restapis/" + apiID + "/prod/_user_request_/anything") //nolint:noctx // test
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("undefined route status = %d, want 403", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Missing Authentication Token") {
		t.Fatalf("expected Missing Authentication Token, got %s", body)
	}
}

// TestE2E_ControlPlaneRoundTrip verifies the management API list/get after create.
func TestE2E_ControlPlaneRoundTrip(t *testing.T) {
	srv := newE2E(t)

	api := doJSON(t, http.MethodPost, srv.URL+"/restapis", `{"name":"petstore","description":"d"}`)
	apiID, _ := api["id"].(string)

	got := doJSON(t, http.MethodGet, srv.URL+"/restapis/"+apiID, "")
	if got["name"] != "petstore" || got["description"] != "d" {
		t.Fatalf("GetRestApi mismatch: %v", got)
	}

	list := doJSON(t, http.MethodGet, srv.URL+"/restapis", "")
	items, _ := list["item"].([]any)
	if len(items) == 0 {
		t.Fatalf("GetRestApis returned no items: %v", list)
	}
}
