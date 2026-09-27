package apigatewayv2_test

import (
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigatewayv2"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// addRoute creates an HTTP_PROXY integration and a route targeting it.
func addRoute(t *testing.T, m *apigatewayv2.Mock, apiID, key string) *driver.Route {
	t.Helper()

	ig, err := m.CreateIntegration(ctx(), apiID, &driver.CreateIntegrationInput{
		IntegrationType: driver.IntegrationHTTPProxy, IntegrationURI: "https://example.com", IntegrationMethod: "ANY",
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}

	rt, err := m.CreateRoute(ctx(), apiID, &driver.CreateRouteInput{RouteKey: key, Target: "integrations/" + ig.IntegrationID})
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	return rt
}

func TestDeploymentRequiresRoutesAndStage(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)

	if _, err := m.CreateDeployment(ctx(), api.APIID, &driver.CreateDeploymentInput{}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("deploy with no routes err = %v, want InvalidArgument", err)
	}

	addRoute(t, m, api.APIID, "GET /a")

	if _, err := m.CreateDeployment(ctx(), api.APIID, &driver.CreateDeploymentInput{StageName: "x"}); !cerrors.IsNotFound(err) {
		t.Fatalf("deploy to missing stage err = %v, want NotFound", err)
	}

	d, err := m.CreateDeployment(ctx(), api.APIID, &driver.CreateDeploymentInput{Description: "d"})
	if err != nil || d.DeploymentStatus != driver.DeploymentStatusDeployed || d.AutoDeployed {
		t.Fatalf("CreateDeployment: %v, %+v", err, d)
	}
}

func TestStageDeploymentPointerRules(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)
	addRoute(t, m, api.APIID, "GET /a")

	d, err := m.CreateDeployment(ctx(), api.APIID, &driver.CreateDeploymentInput{})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	if _, err := m.CreateStage(ctx(), api.APIID, &driver.CreateStageInput{StageName: "p", DeploymentID: "nope"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bogus deploymentId err = %v, want InvalidArgument", err)
	}

	st, err := m.CreateStage(ctx(), api.APIID, &driver.CreateStageInput{StageName: "p", DeploymentID: d.DeploymentID})
	if err != nil || st.DeploymentID != d.DeploymentID {
		t.Fatalf("CreateStage: %v, %+v", err, st)
	}

	if err := m.DeleteDeployment(ctx(), api.APIID, d.DeploymentID); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("delete active deployment err = %v, want InvalidArgument", err)
	}

	if err := m.DeleteStage(ctx(), api.APIID, "p"); err != nil {
		t.Fatalf("DeleteStage: %v", err)
	}

	if err := m.DeleteDeployment(ctx(), api.APIID, d.DeploymentID); err != nil {
		t.Fatalf("DeleteDeployment: %v", err)
	}
}

func TestAutoDeployOnStageCreateAndToggle(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)
	addRoute(t, m, api.APIID, "GET /a")

	st, err := m.CreateStage(ctx(), api.APIID, &driver.CreateStageInput{StageName: "auto", AutoDeploy: true})
	if err != nil || st.DeploymentID == "" {
		t.Fatalf("autoDeploy CreateStage: %v, %+v", err, st)
	}

	if _, err := m.CreateStage(ctx(), api.APIID, &driver.CreateStageInput{StageName: "manual"}); err != nil {
		t.Fatalf("CreateStage manual: %v", err)
	}

	on := true

	upd, err := m.UpdateStage(ctx(), api.APIID, "manual", &driver.UpdateStageInput{AutoDeploy: &on})
	if err != nil || upd.DeploymentID == "" {
		t.Fatalf("toggle autoDeploy: %v, %+v", err, upd)
	}

	deps, _, err := m.GetDeployments(ctx(), api.APIID, nil)
	if err != nil || len(deps) != 2 || !deps[0].AutoDeployed {
		t.Fatalf("GetDeployments: %v, %+v", err, deps)
	}
}

func TestQuickCreateManagedRules(t *testing.T) {
	m := newMock(t)

	api, err := m.CreateAPI(ctx(), &driver.CreateAPIInput{
		Name: "q", ProtocolType: driver.ProtocolHTTP,
		Target: "arn:aws:lambda:us-east-1:000000000000:function:f", RouteKey: "GET /pets",
	})
	if err != nil {
		t.Fatalf("CreateAPI quick: %v", err)
	}

	routes, _, _ := m.GetRoutes(ctx(), api.APIID, nil)
	if len(routes) != 1 || !routes[0].APIGatewayManaged {
		t.Fatalf("routes = %+v", routes)
	}

	key := "GET /cats"
	if _, err := m.UpdateRoute(ctx(), api.APIID, routes[0].RouteID, &driver.UpdateRouteInput{RouteKey: &key}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("managed route key change err = %v, want InvalidArgument", err)
	}

	desc := "x"
	if _, err := m.UpdateStage(ctx(), api.APIID, "$default", &driver.UpdateStageInput{Description: &desc}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("managed stage update err = %v, want InvalidArgument", err)
	}

	// UpdateApi's RouteKey is the supported way to rename the managed route.
	if _, err := m.UpdateAPI(ctx(), api.APIID, &driver.UpdateAPIInput{RouteKey: &key}); err != nil {
		t.Fatalf("UpdateAPI routeKey: %v", err)
	}

	got, _ := m.GetRoute(ctx(), api.APIID, routes[0].RouteID)
	if got.RouteKey != key {
		t.Fatalf("managed route key = %q, want %q", got.RouteKey, key)
	}

	bad := "nope"
	if _, err := m.UpdateAPI(ctx(), api.APIID, &driver.UpdateAPIInput{Target: &bad}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad target err = %v, want InvalidArgument", err)
	}
}

func TestWebSocketEndpointAndRoutes(t *testing.T) {
	m := newMock(t)

	api, err := m.CreateAPI(ctx(), &driver.CreateAPIInput{
		Name: "ws", ProtocolType: driver.ProtocolWebSocket, RouteSelectionExpression: "$request.body.action",
	})
	if err != nil {
		t.Fatalf("CreateAPI ws: %v", err)
	}

	if api.APIEndpoint != "wss://"+api.APIID+".execute-api.us-east-1.amazonaws.com" {
		t.Fatalf("ws apiEndpoint = %q", api.APIEndpoint)
	}

	if _, err := m.CreateRoute(ctx(), api.APIID, &driver.CreateRouteInput{RouteKey: "$connect", AuthorizationType: "JWT"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("ws JWT route err = %v, want InvalidArgument", err)
	}
}

func TestTagsOnStageAndLimits(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)
	arn := "arn:aws:apigateway:us-east-1::/apis/" + api.APIID

	if _, err := m.CreateStage(ctx(), api.APIID, &driver.CreateStageInput{StageName: "s", Tags: map[string]string{"a": "1"}}); err != nil {
		t.Fatalf("CreateStage: %v", err)
	}

	if err := m.TagResource(ctx(), arn+"/stages/s", map[string]string{"b": "2"}); err != nil {
		t.Fatalf("TagResource stage: %v", err)
	}

	tags, err := m.GetTags(ctx(), arn+"/stages/s")
	if err != nil || tags["a"] != "1" || tags["b"] != "2" {
		t.Fatalf("GetTags stage: %v, %v", err, tags)
	}

	if _, err := m.GetTags(ctx(), arn+"/stages/missing"); !cerrors.IsNotFound(err) {
		t.Fatalf("missing stage tags err = %v, want NotFound", err)
	}

	if _, err := m.GetTags(ctx(), "arn:aws:apigateway:eu-west-1::/apis/"+api.APIID); !cerrors.IsNotFound(err) {
		t.Fatalf("other region err = %v, want NotFound", err)
	}

	many := map[string]string{}
	for i := range 51 {
		many[string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}

	if err := m.TagResource(ctx(), arn, many); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("51 tags err = %v, want InvalidArgument", err)
	}

	if err := m.UntagResource(ctx(), arn, nil); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("untag without keys err = %v, want InvalidArgument", err)
	}
}
