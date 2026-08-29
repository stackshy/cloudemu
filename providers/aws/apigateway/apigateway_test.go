package apigateway_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// fakeInvoker records the last payload it was invoked with and returns a
// canned proxy-shaped response, so tests can assert both the event shape and the
// response mapping.
type fakeInvoker struct {
	lastTarget  string
	lastPayload []byte
	output      []byte
	fnErr       string
	err         error
}

func (f *fakeInvoker) InvokeSync(_ context.Context, target string, payload []byte) ([]byte, string, error) {
	f.lastTarget = target
	f.lastPayload = payload

	return f.output, f.fnErr, f.err
}

func newMock(t *testing.T) *apigateway.Mock {
	t.Helper()

	return apigateway.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Unix(0, 0))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	))
}

func ctx() context.Context { return context.Background() }

// deployProxyAPI builds a REST API with a single resource+method+AWS_PROXY
// integration and deploys it to "prod". It returns the api id and root id.
func deployProxyAPI(t *testing.T, m *apigateway.Mock, pathPart, method, uri string) (apiID, rootID, resID string) {
	t.Helper()

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "petstore"})
	if err != nil {
		t.Fatalf("CreateRestAPI: %v", err)
	}

	res, err := m.CreateResource(ctx(), api.ID, api.RootResourceID, pathPart)
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}

	if _, err = m.PutMethod(ctx(), api.ID, res.ID, method, driver.PutMethodInput{}); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}

	_, err = m.PutIntegration(ctx(), api.ID, res.ID, method, driver.PutIntegrationInput{
		Type: driver.IntegrationAWSProxy, IntegrationHTTPMethod: "POST", URI: uri,
	})
	if err != nil {
		t.Fatalf("PutIntegration: %v", err)
	}

	if _, err = m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{StageName: "prod"}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	return api.ID, api.RootResourceID, res.ID
}

const lambdaURI = "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/" +
	"arn:aws:lambda:us-east-1:000000000000:function:hello/invocations"

func TestCreateRestAPICreatesRoot(t *testing.T) {
	m := newMock(t)

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "petstore"})
	if err != nil {
		t.Fatalf("CreateRestAPI: %v", err)
	}

	if api.ID == "" || api.RootResourceID == "" {
		t.Fatalf("expected id + rootResourceId, got %+v", api)
	}

	resources, err := m.GetResources(ctx(), api.ID)
	if err != nil {
		t.Fatalf("GetResources: %v", err)
	}

	if len(resources) != 1 || resources[0].Path != "/" {
		t.Fatalf("expected a single root resource '/', got %+v", resources)
	}
}

func TestCreateRestAPIRequiresName(t *testing.T) {
	m := newMock(t)

	if _, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("empty name should be InvalidArgument, got %v", err)
	}
}

func TestDeleteRestAPI(t *testing.T) {
	m := newMock(t)
	api, _ := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "x"})

	if err := m.DeleteRestAPI(ctx(), api.ID); err != nil {
		t.Fatalf("DeleteRestAPI: %v", err)
	}

	if _, err := m.GetRestAPI(ctx(), api.ID); !errors.IsNotFound(err) {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}
}

func TestCreateResourceDuplicateRejected(t *testing.T) {
	m := newMock(t)
	api, _ := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "x"})

	if _, err := m.CreateResource(ctx(), api.ID, api.RootResourceID, "pets"); err != nil {
		t.Fatalf("CreateResource: %v", err)
	}

	if _, err := m.CreateResource(ctx(), api.ID, api.RootResourceID, "pets"); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate pathPart should be AlreadyExists, got %v", err)
	}
}

func TestPutGetMethodAndIntegration(t *testing.T) {
	m := newMock(t)
	apiID, _, resID := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	mth, err := m.GetMethod(ctx(), apiID, resID, "GET")
	if err != nil {
		t.Fatalf("GetMethod: %v", err)
	}

	if mth.HTTPMethod != "GET" || mth.Integration == nil || mth.Integration.Type != driver.IntegrationAWSProxy {
		t.Fatalf("unexpected method/integration: %+v", mth)
	}

	ig, err := m.GetIntegration(ctx(), apiID, resID, "GET")
	if err != nil {
		t.Fatalf("GetIntegration: %v", err)
	}

	if ig.URI != lambdaURI {
		t.Fatalf("unexpected integration uri: %s", ig.URI)
	}
}

func TestCreateDeploymentAutoCreatesStage(t *testing.T) {
	m := newMock(t)
	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	st, err := m.GetStage(ctx(), apiID, "prod")
	if err != nil {
		t.Fatalf("GetStage: %v", err)
	}

	if st.StageName != "prod" || st.DeploymentID == "" {
		t.Fatalf("expected deployed prod stage, got %+v", st)
	}
}

func TestInvokeRouteInvokesLambdaAndMapsResponse(t *testing.T) {
	m := newMock(t)
	inv := &fakeInvoker{output: []byte(`{"statusCode":201,"headers":{"X-Test":"1"},"body":"hi there"}`)}
	m.SetLambdaInvoker(inv)

	apiID, _, resID := deployProxyAPI(t, m, "hello", "GET", lambdaURI)
	_ = resID

	resp, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: apiID, StageName: "prod", HTTPMethod: "GET", Path: "/hello",
	})
	if err != nil {
		t.Fatalf("InvokeRoute: %v", err)
	}

	if resp.StatusCode != 201 || resp.Body != "hi there" || resp.Headers["X-Test"] != "1" {
		t.Fatalf("unexpected mapped response: %+v", resp)
	}

	// The Lambda target ARN was extracted from the integration URI.
	if inv.lastTarget != "arn:aws:lambda:us-east-1:000000000000:function:hello" {
		t.Fatalf("unexpected lambda target: %s", inv.lastTarget)
	}

	// The proxy event carried the request method+path.
	var event map[string]any
	if err := json.Unmarshal(inv.lastPayload, &event); err != nil {
		t.Fatalf("event not JSON: %v", err)
	}

	if event["httpMethod"] != "GET" || event["path"] != "/hello" || event["resource"] != "/hello" {
		t.Fatalf("unexpected proxy event: %v", event)
	}
}

func TestInvokeRouteGreedyProxyCapturesPath(t *testing.T) {
	m := newMock(t)
	inv := &fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)}
	m.SetLambdaInvoker(inv)

	apiID, _, _ := deployProxyAPI(t, m, "{proxy+}", "ANY", lambdaURI)

	resp, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: apiID, StageName: "prod", HTTPMethod: "DELETE", Path: "/a/b/c",
	})
	if err != nil {
		t.Fatalf("InvokeRoute: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("greedy route should have matched, got %d", resp.StatusCode)
	}

	var event struct {
		PathParameters map[string]string `json:"pathParameters"`
		Resource       string            `json:"resource"`
	}

	if err := json.Unmarshal(inv.lastPayload, &event); err != nil {
		t.Fatalf("event not JSON: %v", err)
	}

	if event.PathParameters["proxy"] != "a/b/c" || event.Resource != "/{proxy+}" {
		t.Fatalf("greedy path not captured: %+v", event)
	}
}

func TestInvokeRouteLiteralBeatsGreedy(t *testing.T) {
	m := newMock(t)
	inv := &fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)}
	m.SetLambdaInvoker(inv)

	// Two resources under root: a literal /health and a greedy /{proxy+}.
	api, _ := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "x"})

	health, _ := m.CreateResource(ctx(), api.ID, api.RootResourceID, "health")
	_, _ = m.PutMethod(ctx(), api.ID, health.ID, "GET", driver.PutMethodInput{})
	_, _ = m.PutIntegration(ctx(), api.ID, health.ID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationAWSProxy, URI: lambdaURI,
	})

	proxy, _ := m.CreateResource(ctx(), api.ID, api.RootResourceID, "{proxy+}")
	_, _ = m.PutMethod(ctx(), api.ID, proxy.ID, "ANY", driver.PutMethodInput{})
	_, _ = m.PutIntegration(ctx(), api.ID, proxy.ID, "ANY", driver.PutIntegrationInput{
		Type: driver.IntegrationAWSProxy, URI: lambdaURI,
	})

	_, _ = m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{StageName: "prod"})

	if _, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: api.ID, StageName: "prod", HTTPMethod: "GET", Path: "/health",
	}); err != nil {
		t.Fatalf("InvokeRoute: %v", err)
	}

	var event struct {
		Resource string `json:"resource"`
	}
	_ = json.Unmarshal(inv.lastPayload, &event)

	if event.Resource != "/health" {
		t.Fatalf("literal /health should win over /{proxy+}, matched %q", event.Resource)
	}
}

func TestInvokeRouteUndefinedRouteForbidden(t *testing.T) {
	m := newMock(t)
	m.SetLambdaInvoker(&fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)})

	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	resp, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: apiID, StageName: "prod", HTTPMethod: "GET", Path: "/nope",
	})
	if err != nil {
		t.Fatalf("InvokeRoute: %v", err)
	}

	if resp.StatusCode != 403 {
		t.Fatalf("undefined route should be 403, got %d", resp.StatusCode)
	}
}

func TestInvokeRouteUnknownStageForbidden(t *testing.T) {
	m := newMock(t)
	m.SetLambdaInvoker(&fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)})

	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	resp, _ := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: apiID, StageName: "missing", HTTPMethod: "GET", Path: "/hello",
	})

	if resp.StatusCode != 403 {
		t.Fatalf("unknown stage should be 403, got %d", resp.StatusCode)
	}
}

func TestInvokeRouteNilLambdaIsBadGateway(t *testing.T) {
	m := newMock(t) // no SetLambdaInvoker — nil-safe fallback

	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	resp, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: apiID, StageName: "prod", HTTPMethod: "GET", Path: "/hello",
	})
	if err != nil {
		t.Fatalf("InvokeRoute must not error when no lambda wired: %v", err)
	}

	if resp.StatusCode != 502 {
		t.Fatalf("nil lambda should yield 502, got %d", resp.StatusCode)
	}
}

func TestInvokeRouteMalformedLambdaResponse(t *testing.T) {
	m := newMock(t)
	m.SetLambdaInvoker(&fakeInvoker{output: []byte(`not json`)})

	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	resp, _ := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: apiID, StageName: "prod", HTTPMethod: "GET", Path: "/hello",
	})

	if resp.StatusCode != 502 {
		t.Fatalf("malformed lambda response should be 502, got %d", resp.StatusCode)
	}
}

// TestCreateResourceRejectsSecondVariableSibling proves a parent may have at
// most one variable (path-parameter) child: creating {name} after {id} under the
// same parent is a ConflictException (AlreadyExists), since two variable siblings
// would make route matching depend on map iteration order.
func TestCreateResourceRejectsSecondVariableSibling(t *testing.T) {
	m := newMock(t)

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "petstore"})
	if err != nil {
		t.Fatalf("CreateRestAPI: %v", err)
	}

	if _, err = m.CreateResource(ctx(), api.ID, api.RootResourceID, "{id}"); err != nil {
		t.Fatalf("CreateResource({id}): %v", err)
	}

	// A second variable sibling with a different name is rejected.
	if _, err = m.CreateResource(ctx(), api.ID, api.RootResourceID, "{name}"); !errors.IsAlreadyExists(err) {
		t.Fatalf("CreateResource({name}) error = %v, want AlreadyExists (ConflictException)", err)
	}

	// A literal sibling alongside the variable child is still allowed.
	if _, err = m.CreateResource(ctx(), api.ID, api.RootResourceID, "pets"); err != nil {
		t.Fatalf("CreateResource(pets) alongside {id}: %v", err)
	}
}
