package apigateway_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// mockMethod builds /m with a MOCK GET using the given request templates and
// passthrough behaviour, a 200 method response declaring X-A, and a default
// integration response with responseTemplate. It deploys stage "s" with
// stage variable v=sv and returns the API and resource ids.
func mockMethod(
	t *testing.T, m *apigateway.Mock, reqTemplates map[string]string, passthrough, responseTemplate string,
) (apiID, resID string) {
	t.Helper()

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "mock"})
	if err != nil {
		t.Fatalf("CreateRestAPI: %v", err)
	}

	res, err := m.CreateResource(ctx(), api.ID, api.RootResourceID, "m")
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}

	if _, err := m.PutMethod(ctx(), api.ID, res.ID, "GET", driver.PutMethodInput{}); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}

	if _, err := m.PutIntegration(ctx(), api.ID, res.ID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationMock, RequestTemplates: reqTemplates, PassthroughBehavior: passthrough,
	}); err != nil {
		t.Fatalf("PutIntegration: %v", err)
	}

	if _, err := m.PutMethodResponse(ctx(), api.ID, res.ID, "GET", "200", driver.PutMethodResponseInput{
		ResponseParameters: map[string]bool{"method.response.header.X-A": false},
	}); err != nil {
		t.Fatalf("PutMethodResponse: %v", err)
	}

	in := driver.PutIntegrationResponseInput{
		ResponseParameters: map[string]string{"method.response.header.X-A": "stageVariables.v"},
	}
	if responseTemplate != "" {
		in.ResponseTemplates = map[string]string{"application/json": responseTemplate}
	}

	if _, err := m.PutIntegrationResponse(ctx(), api.ID, res.ID, "GET", "200", in); err != nil {
		t.Fatalf("PutIntegrationResponse: %v", err)
	}

	if _, err := m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{
		StageName: "s", Variables: map[string]string{"v": "sv"},
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	return api.ID, res.ID
}

func invokeMock(t *testing.T, m *apigateway.Mock, apiID string, req driver.ProxyRequest) *driver.ProxyResponse {
	t.Helper()

	req.RestAPIID, req.StageName, req.HTTPMethod, req.Path = apiID, "s", "GET", "/m"

	resp, err := m.InvokeRoute(ctx(), &req)
	if err != nil {
		t.Fatalf("InvokeRoute: %v", err)
	}

	return resp
}

func TestMockResponseTemplateContext(t *testing.T) {
	m := newMock(t)
	tmpl := `{"q":"$input.params('q')","h":"$input.params('x-h')","path":"$context.resourcePath",` +
		`"esc":"$util.escapeJavaScript('a"b/é')","b64":"$util.base64Encode('hi')",` +
		`"url":"$util.urlEncode('a b')","json":$input.json('$')}`
	apiID, _ := mockMethod(t, m, map[string]string{"application/json": `{"statusCode": 200}`}, "", tmpl)

	resp := invokeMock(t, m, apiID, driver.ProxyRequest{
		Query: map[string]string{"q": "1"}, Headers: map[string]string{"X-H": "hv"},
	})

	want := `{"q":"1","h":"hv","path":"/m","esc":"a\"b\/\u00E9","b64":"aGk=","url":"a+b","json":{}}`
	if resp.StatusCode != 200 || resp.Body != want || resp.Headers["X-A"] != "sv" {
		t.Fatalf("got %d %s %v\nwant %s", resp.StatusCode, resp.Body, resp.Headers, want)
	}
}

func TestMockResponseOverride(t *testing.T) {
	m := newMock(t)
	tmpl := `#set($context.responseOverride.status = 201)#set($context.responseOverride.header.X-O = "o")created`
	apiID, _ := mockMethod(t, m, map[string]string{"application/json": `{"statusCode": 200}`}, "", tmpl)

	resp := invokeMock(t, m, apiID, driver.ProxyRequest{})
	if resp.StatusCode != 201 || resp.Body != "created" || resp.Headers["X-O"] != "o" {
		t.Fatalf("got %d %s %v", resp.StatusCode, resp.Body, resp.Headers)
	}
}

func TestMockPassthroughBehavior(t *testing.T) {
	cases := []struct {
		name        string
		templates   map[string]string
		passthrough string
		contentType string
		wantStatus  int
	}{
		{"no match passes through", map[string]string{"application/json": `{"statusCode":200}`}, "", "text/plain", 200},
		{"never rejects", map[string]string{"application/json": `{"statusCode":200}`}, driver.PassthroughNever, "text/plain", 415},
		{"no templates passes", nil, driver.PassthroughWhenNoTemplates, "text/plain", 200},
		{"templates defined rejects", map[string]string{"application/json": "{}"}, driver.PassthroughWhenNoTemplates, "text/xml", 415},
		{"missing content type is json", map[string]string{"application/json": `{"statusCode":200}`}, driver.PassthroughNever, "", 200},
		{"bad statusCode", map[string]string{"application/json": `{"statusCode":"x"}`}, "", "", 500},
		{"template not json", map[string]string{"application/json": `not json`}, "", "", 500},
		{"template error", map[string]string{"application/json": `$util.parseJson("{")`}, "", "", 500},
		{"unmatched status", map[string]string{"application/json": `{"statusCode":404}`}, "", "", 200},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newMock(t)
			apiID, _ := mockMethod(t, m, c.templates, c.passthrough, "")

			resp := invokeMock(t, m, apiID, driver.ProxyRequest{
				Headers: map[string]string{"Content-Type": c.contentType}, Body: "plain",
			})
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, c.wantStatus, resp.Body)
			}
		})
	}
}

func TestMethodResponseLifecycleAndSnapshot(t *testing.T) {
	m := newMock(t)
	apiID, resID := mockMethod(t, m, map[string]string{"application/json": `{"statusCode":200}`}, "", "{}")

	_, err := m.PutMethodResponse(ctx(), apiID, resID, "GET", "200", driver.PutMethodResponseInput{})
	assertMessage(t, err, errors.IsAlreadyExists, "Response already exists for this resource")

	_, err = m.PutMethodResponse(ctx(), apiID, resID, "GET", "200", driver.PutMethodResponseInput{
		ResponseParameters: map[string]bool{"bad.key": true},
	})
	if !errors.IsAlreadyExists(err) && !errors.IsInvalidArgument(err) {
		t.Fatalf("bad key: %v", err)
	}

	mr, err := m.UpdateMethodResponse(ctx(), apiID, resID, "GET", "200", []driver.PatchOperation{
		{Op: "remove", Path: "/responseParameters/method.response.header.X-A"},
		{Op: "add", Path: "/responseModels/application~1json", Value: "Empty"},
	})
	if err != nil || len(mr.ResponseParameters) != 0 || mr.ResponseModels["application/json"] != "Empty" {
		t.Fatalf("UpdateMethodResponse = %+v %v", mr, err)
	}

	ir, err := m.UpdateIntegrationResponse(ctx(), apiID, resID, "GET", "200", []driver.PatchOperation{
		{Op: "replace", Path: "/selectionPattern", Value: "2\\d\\d"},
		{Op: "remove", Path: "/responseParameters/method.response.header.X-A"},
		{Op: "replace", Path: "/contentHandling", Value: "CONVERT_TO_TEXT"},
	})
	if err != nil || ir.SelectionPattern != `2\d\d` || ir.ContentHandling != "CONVERT_TO_TEXT" {
		t.Fatalf("UpdateIntegrationResponse = %+v %v", ir, err)
	}

	if _, err := m.UpdateIntegrationResponse(ctx(), apiID, resID, "GET", "200", []driver.PatchOperation{
		{Op: "replace", Path: "/contentHandling", Value: "BOGUS"},
	}); !errors.IsInvalidArgument(err) {
		t.Fatalf("bad contentHandling: %v", err)
	}

	mth, err := m.UpdateMethod(ctx(), apiID, resID, "GET", []driver.PatchOperation{
		{Op: "add", Path: "/requestParameters/method.request.querystring.q", Value: "true"},
		{Op: "add", Path: "/requestModels/application~1json", Value: "Empty"},
		{Op: "replace", Path: "/operationName", Value: "GetM"},
	})
	if err != nil || !mth.RequestParameters["method.request.querystring.q"] || mth.OperationName != "GetM" {
		t.Fatalf("UpdateMethod = %+v %v", mth, err)
	}

	ig, err := m.UpdateIntegration(ctx(), apiID, resID, "GET", []driver.PatchOperation{
		{Op: "add", Path: "/requestParameters/integration.request.header.X", Value: "method.request.querystring.q"},
		{Op: "add", Path: "/cacheKeyParameters/method.request.querystring.q"},
		{Op: "replace", Path: "/credentials", Value: "arn:aws:iam::000000000000:role/r"},
	})
	if err != nil || ig.RequestParameters["integration.request.header.X"] == "" || len(ig.CacheKeyParameters) != 1 ||
		len(ig.IntegrationResponses) != 1 {
		t.Fatalf("UpdateIntegration = %+v %v", ig, err)
	}

	data, err := m.Snapshot(ctx(), false)
	if err != nil {
		t.Fatal(err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx(), data); err != nil {
		t.Fatal(err)
	}

	got, err := dst.GetIntegrationResponse(ctx(), apiID, resID, "GET", "200")
	if err != nil || got.SelectionPattern != `2\d\d` {
		t.Fatalf("restored integration response = %+v %v", got, err)
	}

	if mr, err := dst.GetMethodResponse(ctx(), apiID, resID, "GET", "200"); err != nil || mr.ResponseModels["application/json"] != "Empty" {
		t.Fatalf("restored method response = %+v %v", mr, err)
	}

	// The deployed tree survives too: the restored stage still answers.
	if resp := invokeMock(t, dst, apiID, driver.ProxyRequest{}); resp.StatusCode != 200 {
		t.Fatalf("restored invoke = %d", resp.StatusCode)
	}

	if err := m.DeleteIntegrationResponse(ctx(), apiID, resID, "GET", "200"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetIntegrationResponse(ctx(), apiID, resID, "GET", "200"); !errors.IsNotFound(err) {
		t.Fatalf("after delete: %v", err)
	}

	if err := m.DeleteMethodResponse(ctx(), apiID, resID, "GET", "200"); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteMethodResponse(ctx(), apiID, resID, "GET", "200"); !errors.IsNotFound(err) {
		t.Fatalf("double delete: %v", err)
	}
}

func TestMockSelectionPatternPicksResponse(t *testing.T) {
	m := newMock(t)
	apiID, resID := mockMethod(t, m, map[string]string{"application/json": `{"statusCode": $input.params('c')}`}, "", "ok")

	if _, err := m.PutMethodResponse(ctx(), apiID, resID, "GET", "400", driver.PutMethodResponseInput{}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PutIntegrationResponse(ctx(), apiID, resID, "GET", "400", driver.PutIntegrationResponseInput{
		SelectionPattern:  `4\d\d`,
		ResponseTemplates: map[string]string{"application/json": "bad", "text/plain": "bad-text"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateDeployment(ctx(), apiID, driver.CreateDeploymentInput{StageName: "s"}); err != nil {
		t.Fatal(err)
	}

	resp := invokeMock(t, m, apiID, driver.ProxyRequest{
		Query: map[string]string{"c": "404"}, Headers: map[string]string{"Accept": "text/plain"},
	})
	if resp.StatusCode != 400 || resp.Body != "bad-text" || resp.Headers["Content-Type"] != "text/plain" {
		t.Fatalf("4xx = %d %s %v", resp.StatusCode, resp.Body, resp.Headers)
	}

	if resp := invokeMock(t, m, apiID, driver.ProxyRequest{Query: map[string]string{"c": "200"}}); resp.Body != "ok" {
		t.Fatalf("default = %d %s", resp.StatusCode, resp.Body)
	}
}
