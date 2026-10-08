package apigateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// dpFixture is a mock with a Lambda stand-in, a fake clock and one API whose GET
// /items method the tests configure before deploying.
type dpFixture struct {
	m     *apigateway.Mock
	clk   *config.FakeClock
	inv   *fakeInvoker
	api   *driver.RestAPI
	resID string
}

func newDP(t *testing.T) *dpFixture {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2025, 3, 5, 12, 0, 0, 0, time.UTC))
	m := apigateway.New(config.NewOptions(config.WithClock(clk), config.WithRegion("us-east-1"), config.WithAccountID("000000000000")))
	inv := &fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)}
	m.SetLambdaInvoker(inv)

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "dp"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := m.CreateResource(ctx(), api.ID, api.RootResourceID, "items")
	if err != nil {
		t.Fatal(err)
	}

	return &dpFixture{m: m, clk: clk, inv: inv, api: api, resID: res.ID}
}

// method configures GET /items with the lambda proxy integration and deploys.
func (f *dpFixture) method(t *testing.T, in driver.PutMethodInput, ig *driver.PutIntegrationInput) {
	t.Helper()

	if _, err := f.m.PutMethod(ctx(), f.api.ID, f.resID, "GET", in); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}

	if ig == nil {
		ig = &driver.PutIntegrationInput{Type: driver.IntegrationAWSProxy, IntegrationHTTPMethod: "POST", URI: lambdaURI}
	}

	if _, err := f.m.PutIntegration(ctx(), f.api.ID, f.resID, "GET", *ig); err != nil {
		t.Fatalf("PutIntegration: %v", err)
	}

	f.deploy(t, nil)
}

func (f *dpFixture) deploy(t *testing.T, vars map[string]string) {
	t.Helper()

	if _, err := f.m.CreateDeployment(ctx(), f.api.ID, driver.CreateDeploymentInput{StageName: "prod", Variables: vars}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
}

func (f *dpFixture) get(t *testing.T, headers map[string]string, query map[string]string) *driver.ProxyResponse {
	t.Helper()

	resp, err := f.m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: f.api.ID, StageName: "prod", HTTPMethod: "GET", Path: "/items", Headers: headers, Query: query, SourceIP: "1.2.3.4",
	})
	if err != nil {
		t.Fatal(err)
	}

	return resp
}

func status(t *testing.T, resp *driver.ProxyResponse, want int) {
	t.Helper()

	if resp.StatusCode != want {
		t.Fatalf("status = %d (%s), want %d", resp.StatusCode, resp.Body, want)
	}
}

func TestAPIKeyRequiredIsEnforced(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{APIKeyRequired: true}, nil)

	status(t, f.get(t, nil, nil), 403) // no key

	key, _ := f.m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", Enabled: true})
	hdr := map[string]string{"X-Api-Key": key.Value}

	status(t, f.get(t, hdr, nil), 403) // key in no usage plan

	plan, _ := f.m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{
		Name: "p", APIStages: []driver.UsagePlanStage{{RestAPIID: f.api.ID, Stage: "prod"}},
	})
	_, _ = f.m.CreateUsagePlanKey(ctx(), plan.ID, key.ID, "API_KEY")

	resp := f.get(t, hdr, nil)
	status(t, resp, 200)

	var event struct {
		RequestContext struct {
			Identity struct {
				APIKeyID string `json:"apiKeyId"`
			} `json:"identity"`
		} `json:"requestContext"`
	}

	_ = json.Unmarshal(f.inv.lastPayload, &event)
	if event.RequestContext.Identity.APIKeyID != key.ID {
		t.Fatalf("apiKeyId must reach the proxy event: %s", f.inv.lastPayload)
	}

	if _, err := f.m.UpdateAPIKey(ctx(), key.ID, []driver.PatchOperation{{Op: "replace", Path: "/enabled", Value: "false"}}); err != nil {
		t.Fatal(err)
	}

	status(t, f.get(t, hdr, nil), 403) // disabled key
}

func TestUsagePlanThrottleAndQuota(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{APIKeyRequired: true}, nil)

	key, _ := f.m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", Enabled: true})
	plan, _ := f.m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{
		Name: "p", APIStages: []driver.UsagePlanStage{{RestAPIID: f.api.ID, Stage: "prod"}},
		Throttle: &driver.ThrottleSettings{BurstLimit: 2, RateLimit: 1}, Quota: &driver.QuotaSettings{Limit: 3, Period: "DAY"},
	})
	_, _ = f.m.CreateUsagePlanKey(ctx(), plan.ID, key.ID, "API_KEY")

	hdr := map[string]string{"x-api-key": key.Value}

	status(t, f.get(t, hdr, nil), 200)
	status(t, f.get(t, hdr, nil), 200)

	if resp := f.get(t, hdr, nil); resp.StatusCode != 429 || !strings.Contains(resp.Body, "Too Many Requests") {
		t.Fatalf("burst exhausted: %d %s", resp.StatusCode, resp.Body)
	}

	f.clk.Advance(2 * time.Second) // two tokens refill

	status(t, f.get(t, hdr, nil), 200) // 3rd request of the day

	f.clk.Advance(2 * time.Second)

	if resp := f.get(t, hdr, nil); resp.StatusCode != 429 || !strings.Contains(resp.Body, "Limit Exceeded") {
		t.Fatalf("quota exhausted: %d %s", resp.StatusCode, resp.Body)
	}

	usage, err := f.m.GetUsage(ctx(), &driver.GetUsageInput{UsagePlanID: plan.ID, StartDate: "2025-03-05", EndDate: "2025-03-05"})
	if err != nil || len(usage.Items) != 1 || usage.Items[0].Days[0] != [2]int64{3, 0} {
		t.Fatalf("usage: %v %+v", err, usage)
	}

	f.clk.Advance(24 * time.Hour) // a new quota period

	status(t, f.get(t, hdr, nil), 200)
}

func TestStageMethodThrottling(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{}, nil)

	if _, err := f.m.UpdateStage(ctx(), f.api.ID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/*/*/throttling/burstLimit", Value: "1"},
		{Op: "replace", Path: "/*/*/throttling/rateLimit", Value: "1"},
	}); err != nil {
		t.Fatal(err)
	}

	status(t, f.get(t, nil, nil), 200)
	status(t, f.get(t, nil, nil), 429)
}

func TestLambdaAuthorizerTokenEnforcement(t *testing.T) {
	f := newDP(t)

	az, err := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{
		Name: "tok", Type: driver.AuthorizerToken, AuthorizerURI: strings.Replace(lambdaURI, "hello", "auth", 1),
		IdentityValidationExpression: "^Bearer .+$", AuthorizerResultTTLInSeconds: intPtr(60),
	})
	if err != nil {
		t.Fatal(err)
	}

	f.method(t, driver.PutMethodInput{AuthorizationType: "CUSTOM", AuthorizerID: az.ID}, nil)

	status(t, f.get(t, nil, nil), 401)                                        // no token
	status(t, f.get(t, map[string]string{"Authorization": "nope"}, nil), 401) // fails the validation expression

	policy := func(effect string) []byte {
		return []byte(`{"principalId":"user1","policyDocument":{"Version":"2012-10-17","Statement":[{"Action":"execute-api:Invoke","Effect":"` +
			effect + `","Resource":"arn:aws:execute-api:us-east-1:000000000000:` + f.api.ID + `/prod/GET/items"}]},"context":{"role":"admin"}}`)
	}

	auth := &fakeInvoker{output: policy("Deny")}
	f.m.SetLambdaInvoker(routeInvoker{"auth": auth, "hello": f.inv})

	status(t, f.get(t, map[string]string{"Authorization": "Bearer t1"}, nil), 403)

	auth.output = policy("Allow")

	status(t, f.get(t, map[string]string{"Authorization": "Bearer t2"}, nil), 200)

	var event struct {
		RequestContext struct {
			Authorizer map[string]any `json:"authorizer"`
		} `json:"requestContext"`
	}

	_ = json.Unmarshal(f.inv.lastPayload, &event)
	if event.RequestContext.Authorizer["principalId"] != "user1" || event.RequestContext.Authorizer["role"] != "admin" {
		t.Fatalf("authorizer context must reach the event: %s", f.inv.lastPayload)
	}

	// The decision is cached per token for the TTL: a changed policy is not seen.
	auth.output = policy("Deny")

	status(t, f.get(t, map[string]string{"Authorization": "Bearer t2"}, nil), 200)

	f.clk.Advance(2 * time.Minute)

	status(t, f.get(t, map[string]string{"Authorization": "Bearer t2"}, nil), 403)

	auth.output = []byte(`{"not":"a policy"}`)

	status(t, f.get(t, map[string]string{"Authorization": "Bearer t3"}, nil), 500)

	auth.output, auth.fnErr = []byte(`{"errorMessage":"Unauthorized"}`), "Unhandled"

	status(t, f.get(t, map[string]string{"Authorization": "Bearer t4"}, nil), 401)
}

// routeInvoker routes InvokeSync to a fake per function name.
type routeInvoker map[string]*fakeInvoker

func (r routeInvoker) InvokeSync(c context.Context, target string, payload []byte) ([]byte, string, error) {
	for name, inv := range r {
		if strings.Contains(target, ":function:"+name) || target == name {
			return inv.InvokeSync(c, target, payload)
		}
	}

	return nil, "", nil
}

func jwt(claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)

		return base64.RawURLEncoding.EncodeToString(b)
	}

	return enc(map[string]string{"alg": "none"}) + "." + enc(claims) + ".sig"
}

func TestCognitoAuthorizerEnforcement(t *testing.T) {
	f := newDP(t)

	az, err := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{
		Name: "pool", Type: driver.AuthorizerCognito, ProviderARNs: []string{"arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc"},
	})
	if err != nil {
		t.Fatal(err)
	}

	f.method(t, driver.PutMethodInput{AuthorizationType: "COGNITO_USER_POOLS", AuthorizerID: az.ID, AuthorizationScopes: []string{"read"}}, nil)

	now := f.clk.Now().Unix()
	good := map[string]any{"iss": "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_abc", "exp": now + 3600, "scope": "read write", "sub": "u1"}
	call := func(claims map[string]any) int {
		return f.get(t, map[string]string{"Authorization": jwt(claims)}, nil).StatusCode
	}

	if got := call(good); got != 200 {
		t.Fatalf("valid token: %d", got)
	}

	var event struct {
		RequestContext struct {
			Authorizer struct{ Claims map[string]string } `json:"authorizer"`
		} `json:"requestContext"`
	}

	_ = json.Unmarshal(f.inv.lastPayload, &event)
	if event.RequestContext.Authorizer.Claims["sub"] != "u1" {
		t.Fatalf("claims must reach the event: %s", f.inv.lastPayload)
	}

	for name, claims := range map[string]map[string]any{
		"expired":       {"iss": good["iss"], "exp": now - 1, "scope": "read"},
		"wrong pool":    {"iss": "https://cognito-idp.us-east-1.amazonaws.com/other", "exp": now + 60, "scope": "read"},
		"missing scope": {"iss": good["iss"], "exp": now + 60, "scope": "write"},
	} {
		if got := call(claims); got != 401 {
			t.Fatalf("%s: %d, want 401", name, got)
		}
	}

	status(t, f.get(t, map[string]string{"Authorization": "garbage"}, nil), 401)
	status(t, f.get(t, nil, nil), 401)
}

func TestRequestValidatorParametersAndBody(t *testing.T) {
	f := newDP(t)

	if _, err := f.m.CreateModel(ctx(), f.api.ID, &driver.CreateModelInput{
		Name: "Item", Schema: `{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":2},"qty":{"type":"integer","minimum":1}},"additionalProperties":false}`,
	}); err != nil {
		t.Fatal(err)
	}

	v, _ := f.m.CreateRequestValidator(ctx(), f.api.ID, &driver.CreateRequestValidatorInput{Name: "v", ValidateRequestBody: true, ValidateRequestParameters: true})

	if _, err := f.m.PutMethod(ctx(), f.api.ID, f.resID, "POST", driver.PutMethodInput{
		RequestValidatorID: v.ID, RequestModels: map[string]string{"application/json": "Item"},
		RequestParameters: map[string]bool{"method.request.querystring.page": true, "method.request.header.X-Tenant": true, "method.request.querystring.opt": false},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.PutIntegration(ctx(), f.api.ID, f.resID, "POST", driver.PutIntegrationInput{
		Type: driver.IntegrationAWSProxy, IntegrationHTTPMethod: "POST", URI: lambdaURI,
	}); err != nil {
		t.Fatal(err)
	}

	f.deploy(t, nil)

	post := func(body string, hdr map[string]string, q map[string]string) *driver.ProxyResponse {
		t.Helper()

		resp, err := f.m.InvokeRoute(ctx(), &driver.ProxyRequest{
			RestAPIID: f.api.ID, StageName: "prod", HTTPMethod: "POST", Path: "/items", Body: body, Headers: hdr, Query: q,
		})
		if err != nil {
			t.Fatal(err)
		}

		return resp
	}

	ok := map[string]string{"Content-Type": "application/json", "X-Tenant": "t"}
	page := map[string]string{"page": "1"}

	resp := post(`{"name":"ab"}`, map[string]string{"Content-Type": "application/json"}, nil)
	if resp.StatusCode != 400 || resp.Body != `{"message":"Missing required request parameters: [X-Tenant, page]"}` {
		t.Fatalf("missing params: %d %s", resp.StatusCode, resp.Body)
	}

	for name, body := range map[string]string{
		"missing required": `{}`, "short string": `{"name":"a"}`, "wrong type": `{"name":1}`, "below minimum": `{"name":"ab","qty":0}`,
		"extra property": `{"name":"ab","x":1}`, "not json": `nope`, "empty": ``,
	} {
		if resp = post(body, ok, page); resp.StatusCode != 400 || !strings.Contains(resp.Body, "Invalid request body") {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, resp.Body)
		}
	}

	status(t, post(`{"name":"ab","qty":3}`, ok, page), 200)
}

func TestGatewayResponseOverridesApply(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{APIKeyRequired: true}, nil)

	if _, err := f.m.PutGatewayResponse(ctx(), f.api.ID, "INVALID_API_KEY", &driver.PutGatewayResponseInput{
		StatusCode: "401", ResponseParameters: map[string]string{"gatewayresponse.header.X-Reason": "'no-key'"},
		ResponseTemplates: map[string]string{"application/json": `{"error":"$context.error.responseType","msg":$context.error.messageString}`},
	}); err != nil {
		t.Fatal(err)
	}

	f.deploy(t, nil)

	resp := f.get(t, nil, nil)
	if resp.StatusCode != 401 || resp.Headers["X-Reason"] != "no-key" || resp.Body != `{"error":"INVALID_API_KEY","msg":"Forbidden"}` {
		t.Fatalf("override: %d %v %s", resp.StatusCode, resp.Headers, resp.Body)
	}

	// DEFAULT_4XX covers every 4xx response type without its own override.
	if _, err := f.m.PutGatewayResponse(ctx(), f.api.ID, "DEFAULT_4XX", &driver.PutGatewayResponseInput{
		ResponseParameters: map[string]string{"gatewayresponse.header.Access-Control-Allow-Origin": "'*'"},
	}); err != nil {
		t.Fatal(err)
	}

	f.deploy(t, nil)

	miss, err := f.m.InvokeRoute(ctx(), &driver.ProxyRequest{RestAPIID: f.api.ID, StageName: "prod", HTTPMethod: "GET", Path: "/nope"})
	if err != nil {
		t.Fatal(err)
	}

	if resp = miss; resp.StatusCode != 403 || resp.Headers["Access-Control-Allow-Origin"] != "*" {
		t.Fatalf("DEFAULT_4XX on a route miss: %d %v", resp.StatusCode, resp.Headers)
	}
}

func TestHTTPProxyIntegration(t *testing.T) {
	var got struct {
		method, path, query, header, body string
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query, got.header, got.body = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Custom"), string(b)
		w.Header().Set("X-Backend", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	}))
	defer backend.Close()

	f := newDP(t)
	f.method(t, driver.PutMethodInput{}, &driver.PutIntegrationInput{
		Type: driver.IntegrationHTTPProxy, IntegrationHTTPMethod: "ANY", URI: backend.URL + "/v1/items",
	})

	// A greedy path parameter cannot climb out of the integration's path.
	for _, bad := range []string{"..", "a/../b", "./x"} {
		f2 := newDP(t)
		f2.method(t, driver.PutMethodInput{RequestParameters: map[string]bool{"method.request.querystring.p": false}}, &driver.PutIntegrationInput{
			Type: driver.IntegrationHTTPProxy, IntegrationHTTPMethod: "ANY", URI: backend.URL + "/v1/{p}",
			RequestParameters: map[string]string{"integration.request.path.p": "method.request.querystring.p"},
		})

		if resp := f2.get(t, nil, map[string]string{"p": bad}); resp.StatusCode != 400 {
			t.Fatalf("dot segment %q: %d %s", bad, resp.StatusCode, resp.Body)
		}
	}

	resp := f.get(t, map[string]string{"X-Custom": "abc", "Host": "ignored"}, map[string]string{"a": "1"})
	if resp.StatusCode != http.StatusTeapot || resp.Body != "short and stout" || resp.MultiValueHeaders["X-Backend"][0] != "yes" {
		t.Fatalf("relay: %d %s %v", resp.StatusCode, resp.Body, resp.MultiValueHeaders)
	}

	if got.method != "GET" || got.path != "/v1/items" || got.query != "a=1" || got.header != "abc" {
		t.Fatalf("backend saw %+v", got)
	}
}

func TestHTTPIntegrationMappingAndStageVariables(t *testing.T) {
	var gotPath, gotBody, gotQuery string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody, gotQuery = r.URL.Path, string(b), r.URL.RawQuery
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":7}`))
	}))
	defer backend.Close()

	f := newDP(t)

	if _, err := f.m.PutMethod(ctx(), f.api.ID, f.resID, "GET", driver.PutMethodInput{
		RequestParameters: map[string]bool{"method.request.querystring.q": false},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.PutMethodResponse(ctx(), f.api.ID, f.resID, "GET", "201", driver.PutMethodResponseInput{}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.PutIntegration(ctx(), f.api.ID, f.resID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationHTTP, IntegrationHTTPMethod: "POST", URI: "http://${stageVariables.host}/svc/{tenant}",
		RequestParameters: map[string]string{"integration.request.path.tenant": "'acme'", "integration.request.querystring.term": "method.request.querystring.q"},
		RequestTemplates:  map[string]string{"application/json": `{"wrapped":"$input.path('$.x')"}`},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.PutIntegrationResponse(ctx(), f.api.ID, f.resID, "GET", "201", driver.PutIntegrationResponseInput{
		SelectionPattern: "2\\d\\d", ResponseTemplates: map[string]string{"application/json": `{"created":$input.json('$.id')}`},
	}); err != nil {
		t.Fatal(err)
	}

	host := strings.TrimPrefix(backend.URL, "http://")
	f.deploy(t, map[string]string{"host": host})

	resp, err := f.m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: f.api.ID, StageName: "prod", HTTPMethod: "GET", Path: "/items", Body: `{"x":"hi"}`,
		Headers: map[string]string{"Content-Type": "application/json"}, Query: map[string]string{"q": "cats"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != 201 || resp.Body != `{"created":7}` {
		t.Fatalf("mapped response: %d %s", resp.StatusCode, resp.Body)
	}

	if gotPath != "/svc/acme" || gotBody != `{"wrapped":"hi"}` || gotQuery != "term=cats" {
		t.Fatalf("backend saw %q %q %q", gotPath, gotBody, gotQuery)
	}
}

func TestMissingStageVariableIsA500(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{}, &driver.PutIntegrationInput{
		Type: driver.IntegrationAWSProxy, IntegrationHTTPMethod: "POST",
		URI: strings.Replace(lambdaURI, "function:hello", "function:${stageVariables.fn}", 1),
	})

	resp := f.get(t, nil, nil)
	if resp.StatusCode != 500 || f.inv.lastTarget != "" {
		t.Fatalf("missing variable must fail without invoking: %d (target %q)", resp.StatusCode, f.inv.lastTarget)
	}

	f.deploy(t, map[string]string{"fn": "hello"})

	status(t, f.get(t, nil, nil), 200)

	if !strings.HasSuffix(f.inv.lastTarget, ":function:hello") {
		t.Fatalf("target = %q", f.inv.lastTarget)
	}
}

func TestHTTPIntegrationFailures(t *testing.T) {
	f := newDP(t)

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	f.method(t, driver.PutMethodInput{}, &driver.PutIntegrationInput{Type: driver.IntegrationHTTPProxy, IntegrationHTTPMethod: "GET", URI: closed.URL + "/x"})
	status(t, f.get(t, nil, nil), 504) // connection refused

	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()

	if _, err := f.m.UpdateIntegration(ctx(), f.api.ID, f.resID, "GET", []driver.PatchOperation{
		{Op: "replace", Path: "/uri", Value: slow.URL + "/x"}, {Op: "replace", Path: "/timeoutInMillis", Value: "50"},
	}); err != nil {
		t.Fatal(err)
	}

	f.deploy(t, nil)

	if resp := f.get(t, nil, nil); resp.StatusCode != 504 || !strings.Contains(resp.Body, "timed out") {
		t.Fatalf("timeout: %d %s", resp.StatusCode, resp.Body)
	}
}

func TestZipDeployedLambdaEchoStubIsNotAMalformedResponse(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{}, nil)

	f.inv.output = nil // the stub echoes whatever it was sent

	echo := &echoInvoker{}
	f.m.SetLambdaInvoker(echo)

	resp := f.get(t, nil, nil)
	if resp.StatusCode != 200 || resp.Headers["X-Cloudemu-Lambda-Stub"] != "true" || !strings.Contains(resp.Body, `"httpMethod":"GET"`) {
		t.Fatalf("stub echo: %d %v %s", resp.StatusCode, resp.Headers, resp.Body)
	}
}

type echoInvoker struct{}

func (*echoInvoker) InvokeSync(_ context.Context, _ string, payload []byte) ([]byte, string, error) {
	return payload, "", nil
}

func TestExecutionAccessLogsAndDetailedMetrics(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 3, 5, 12, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-east-1"), config.WithAccountID("000000000000"))
	m := apigateway.New(opts)
	logs := cloudwatchlogs.New(opts)
	cw := cloudwatch.New(opts)
	m.SetLogSink(logs)
	m.SetMonitoring(cw)
	m.SetLambdaInvoker(&fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)})

	apiID, _, _ := deployProxyAPI(t, m, "items", "GET", lambdaURI)

	if _, err := m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "replace", Path: "/cloudwatchRoleArn", Value: "arn:aws:iam::000000000000:role/cw"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.UpdateStage(ctx(), apiID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/*/*/logging/loglevel", Value: "INFO"}, {Op: "replace", Path: "/*/*/metrics/enabled", Value: "true"},
		{Op: "replace", Path: "/accessLogSettings/destinationArn", Value: "arn:aws:logs:us-east-1:000000000000:log-group:access-logs"},
		{Op: "replace", Path: "/accessLogSettings/format", Value: "$context.requestId $context.httpMethod $context.path $context.status"},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{RestAPIID: apiID, StageName: "prod", HTTPMethod: "GET", Path: "/items"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("invoke: %v %+v", err, resp)
	}

	exec := logGroupMessages(t, logs, "API-Gateway-Execution-Logs_"+apiID+"/prod")
	if !strings.Contains(exec, "Method completed with status: 200") || !strings.Contains(exec, "Endpoint request URI") {
		t.Fatalf("execution log: %s", exec)
	}

	access := logGroupMessages(t, logs, "access-logs")
	if !strings.Contains(access, "GET /prod/items 200") {
		t.Fatalf("access log: %s", access)
	}

	got, err := cw.GetMetricData(ctx(), mondriver.GetMetricInput{
		Namespace: "AWS/ApiGateway", MetricName: "Count", Stat: "Sum", Period: 60,
		Dimensions: map[string]string{"ApiName": "petstore", "Resource": "/items", "Method": "GET", "Stage": "prod"},
		StartTime:  clk.Now().Add(-time.Minute), EndTime: clk.Now().Add(time.Minute),
	})
	if err != nil || len(got.Values) != 1 || got.Values[0] != 1 {
		t.Fatalf("per-method Count metric: %v %+v", err, got)
	}
}

func logGroupMessages(t *testing.T, logs *cloudwatchlogs.Mock, group string) string {
	t.Helper()

	streams, err := logs.ListLogStreams(ctx(), group)
	if err != nil || len(streams) == 0 {
		t.Fatalf("log group %s: %v %v", group, err, streams)
	}

	var sb strings.Builder

	for _, s := range streams {
		events, err := logs.GetLogEvents(ctx(), &logdriver.LogQueryInput{LogGroup: group, LogStream: s.Name})
		if err != nil {
			t.Fatal(err)
		}

		for _, e := range events {
			sb.WriteString(e.Message + "\n")
		}
	}

	return sb.String()
}

func TestTestInvokeMethodAndAuthorizer(t *testing.T) {
	f := newDP(t)

	if _, err := f.m.PutMethod(ctx(), f.api.ID, f.resID, "GET", driver.PutMethodInput{}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.PutIntegration(ctx(), f.api.ID, f.resID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationAWSProxy, IntegrationHTTPMethod: "POST", URI: lambdaURI,
	}); err != nil {
		t.Fatal(err)
	}

	// The API is not deployed: TestInvokeMethod runs the live configuration.
	out, err := f.m.TestInvokeMethod(ctx(), &driver.TestInvokeMethodInput{
		RestAPIID: f.api.ID, ResourceID: f.resID, HTTPMethod: "GET", PathWithQuery: "/items?x=1", StageVariables: map[string]string{"a": "b"},
	})
	if err != nil || out.Status != 200 || out.Body != "ok" || out.Log == "" {
		t.Fatalf("test invoke: %v %+v", err, out)
	}

	var event struct {
		QueryStringParameters map[string]string `json:"queryStringParameters"`
		StageVariables        map[string]string `json:"stageVariables"`
	}

	_ = json.Unmarshal(f.inv.lastPayload, &event)
	if event.QueryStringParameters["x"] != "1" || event.StageVariables["a"] != "b" {
		t.Fatalf("event: %s", f.inv.lastPayload)
	}

	if _, err = f.m.TestInvokeMethod(ctx(), &driver.TestInvokeMethodInput{RestAPIID: f.api.ID, ResourceID: "nope", HTTPMethod: "GET"}); err == nil {
		t.Fatal("unknown resource must fail")
	}

	az, _ := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{Name: "t", Type: driver.AuthorizerToken, AuthorizerURI: lambdaURI})
	f.inv.output = []byte(`{"principalId":"p","policyDocument":{"Statement":[{"Action":"execute-api:Invoke","Effect":"Allow","Resource":"*"}]}}`)

	ta, err := f.m.TestInvokeAuthorizer(ctx(), &driver.TestInvokeAuthorizerInput{
		RestAPIID: f.api.ID, AuthorizerID: az.ID, Headers: map[string]string{"Authorization": "tok"},
	})
	if err != nil || ta.PrincipalID != "p" || !strings.Contains(ta.Policy, "execute-api:Invoke") || ta.ClientStatus != 200 {
		t.Fatalf("test authorizer: %v %+v", err, ta)
	}

	if _, err = f.m.TestInvokeAuthorizer(ctx(), &driver.TestInvokeAuthorizerInput{RestAPIID: f.api.ID, AuthorizerID: az.ID}); err == nil {
		t.Fatal("missing identity source must fail")
	}
}

func TestCachedAuthorizerPolicyIsEvaluatedPerMethod(t *testing.T) {
	f := newDP(t)

	az, _ := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{
		Name: "tok", Type: driver.AuthorizerToken, AuthorizerURI: strings.Replace(lambdaURI, "hello", "auth", 1),
		AuthorizerResultTTLInSeconds: intPtr(300),
	})

	// GET /items is guarded; POST /items uses the same authorizer.
	for _, method := range []string{"GET", "POST"} {
		if _, err := f.m.PutMethod(ctx(), f.api.ID, f.resID, method, driver.PutMethodInput{AuthorizationType: "CUSTOM", AuthorizerID: az.ID}); err != nil {
			t.Fatal(err)
		}

		if _, err := f.m.PutIntegration(ctx(), f.api.ID, f.resID, method, driver.PutIntegrationInput{
			Type: driver.IntegrationAWSProxy, IntegrationHTTPMethod: "POST", URI: lambdaURI,
		}); err != nil {
			t.Fatal(err)
		}
	}

	f.deploy(t, nil)

	// The policy allows only GET /items.
	auth := &fakeInvoker{output: []byte(`{"principalId":"u","policyDocument":{"Statement":[{"Action":"execute-api:Invoke","Effect":"Allow","Resource":"arn:aws:execute-api:us-east-1:000000000000:` + f.api.ID + `/prod/GET/items"}]}}`)}
	f.m.SetLambdaInvoker(routeInvoker{"auth": auth, "hello": f.inv})

	hdr := map[string]string{"Authorization": "same-token"}
	status(t, f.get(t, hdr, nil), 200) // GET is allowed and its decision is cached

	resp, err := f.m.InvokeRoute(ctx(), &driver.ProxyRequest{RestAPIID: f.api.ID, StageName: "prod", HTTPMethod: "POST", Path: "/items", Headers: hdr})
	if err != nil {
		t.Fatal(err)
	}

	status(t, resp, 403) // the cached policy does not cover POST /items
}

func TestCognitoTokenWithoutExpiryIsRejected(t *testing.T) {
	f := newDP(t)

	az, _ := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{
		Name: "pool", Type: driver.AuthorizerCognito, ProviderARNs: []string{"arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc"},
	})
	f.method(t, driver.PutMethodInput{AuthorizationType: "COGNITO_USER_POOLS", AuthorizerID: az.ID}, nil)

	noExp := jwt(map[string]any{"iss": "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_abc", "sub": "u"})
	status(t, f.get(t, map[string]string{"Authorization": noExp}, nil), 401)
}

func TestStageMethodThrottleUsesTheTerraformAndNestedPaths(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{}, nil)

	// Terraform's method_path "items/GET" and a nested path both parse from the right.
	if _, err := f.m.UpdateStage(ctx(), f.api.ID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/items/GET/throttling/burstLimit", Value: "1"},
		{Op: "replace", Path: "/items/GET/throttling/rateLimit", Value: "1"},
		{Op: "replace", Path: "/a~1b/GET/metrics/enabled", Value: "true"},
		{Op: "replace", Path: "/pets/child/GET/metrics/enabled", Value: "true"},
	}); err != nil {
		t.Fatal(err)
	}

	status(t, f.get(t, nil, nil), 200)
	status(t, f.get(t, nil, nil), 429)

	st, err := f.m.GetStage(ctx(), f.api.ID, "prod")
	if err != nil || st.MethodSettings["a/b/GET"] == nil || st.MethodSettings["pets/child/GET"] == nil {
		t.Fatalf("both spellings of a nested path store one canonical key: %v %+v", err, st.MethodSettings)
	}
}

func TestPlanThrottleIsOneBucketPerKey(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{APIKeyRequired: true}, nil)

	other, err := f.m.CreateResource(ctx(), f.api.ID, f.api.RootResourceID, "other")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = f.m.PutMethod(ctx(), f.api.ID, other.ID, "GET", driver.PutMethodInput{APIKeyRequired: true}); err != nil {
		t.Fatal(err)
	}

	if _, err = f.m.PutIntegration(ctx(), f.api.ID, other.ID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationAWSProxy, IntegrationHTTPMethod: "POST", URI: lambdaURI,
	}); err != nil {
		t.Fatal(err)
	}

	f.deploy(t, nil)

	key, _ := f.m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", Enabled: true})
	plan, _ := f.m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{
		Name: "p", APIStages: []driver.UsagePlanStage{{RestAPIID: f.api.ID, Stage: "prod"}},
		Throttle: &driver.ThrottleSettings{BurstLimit: 1, RateLimit: 1},
	})
	_, _ = f.m.CreateUsagePlanKey(ctx(), plan.ID, key.ID, "API_KEY")

	hdr := map[string]string{"x-api-key": key.Value}

	status(t, f.get(t, hdr, nil), 200)

	resp, err := f.m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: f.api.ID, StageName: "prod", HTTPMethod: "GET", Path: "/other", Headers: hdr, SourceIP: "1.2.3.4",
	})
	if err != nil {
		t.Fatal(err)
	}

	status(t, resp, 429) // the burst of 1 is shared by every method of the key
}

func TestRequestAuthorizerWithoutIdentitySourceIsInvoked(t *testing.T) {
	f := newDP(t)

	az, err := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{
		Name: "req", Type: driver.AuthorizerRequest, AuthorizerURI: strings.Replace(lambdaURI, "hello", "auth", 1),
		AuthorizerResultTTLInSeconds: intPtr(0),
	})
	if err != nil {
		t.Fatal(err)
	}

	f.method(t, driver.PutMethodInput{AuthorizationType: "CUSTOM", AuthorizerID: az.ID}, nil)

	auth := &fakeInvoker{output: []byte(`{"principalId":"u","policyDocument":{"Version":"2012-10-17","Statement":[{"Action":"execute-api:Invoke","Effect":"Allow","Resource":"*"}]}}`)}
	f.m.SetLambdaInvoker(routeInvoker{"auth": auth, "hello": f.inv})

	status(t, f.get(t, nil, nil), 200)

	if auth.lastPayload == nil {
		t.Fatal("the authorizer Lambda must be invoked")
	}
}
