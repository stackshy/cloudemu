package apigatewayv2_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigatewayv2"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

func ctx() context.Context { return context.Background() }

func newMock(t *testing.T) *apigatewayv2.Mock {
	t.Helper()

	return apigatewayv2.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Unix(1700000000, 0))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	))
}

func createHTTPAPI(t *testing.T, m *apigatewayv2.Mock) *driver.API {
	t.Helper()

	api, err := m.CreateAPI(ctx(), &driver.CreateAPIInput{Name: "http-api", ProtocolType: driver.ProtocolHTTP})
	if err != nil {
		t.Fatalf("CreateAPI: %v", err)
	}

	return api
}

func TestCreateAPIDefaults(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)

	if len(api.APIID) != 10 {
		t.Fatalf("apiId len = %d, want 10 (%q)", len(api.APIID), api.APIID)
	}

	wantEndpoint := "https://" + api.APIID + ".execute-api.us-east-1.amazonaws.com"
	if api.APIEndpoint != wantEndpoint {
		t.Fatalf("apiEndpoint = %q, want %q", api.APIEndpoint, wantEndpoint)
	}

	if api.RouteSelectionExpression != "$request.method $request.path" {
		t.Fatalf("routeSelectionExpression = %q", api.RouteSelectionExpression)
	}

	if api.APIKeySelectionExpression != "$request.header.x-api-key" {
		t.Fatalf("apiKeySelectionExpression = %q", api.APIKeySelectionExpression)
	}

	if api.DisableExecuteAPIEndpoint {
		t.Fatalf("disableExecuteApiEndpoint = true, want false")
	}

	if api.CreatedDate != 1700000000 {
		t.Fatalf("createdDate = %d", api.CreatedDate)
	}
}

func TestCreateAPIValidation(t *testing.T) {
	m := newMock(t)

	if _, err := m.CreateAPI(ctx(), &driver.CreateAPIInput{ProtocolType: driver.ProtocolHTTP}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty name err = %v, want InvalidArgument", err)
	}

	if _, err := m.CreateAPI(ctx(), &driver.CreateAPIInput{Name: "x", ProtocolType: "FTP"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad protocol err = %v, want InvalidArgument", err)
	}
}

func TestAPICRUD(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)

	got, err := m.GetAPI(ctx(), api.APIID)
	if err != nil || got.Name != "http-api" {
		t.Fatalf("GetAPI: %v, %+v", err, got)
	}

	newName := "renamed"
	disable := true
	upd, err := m.UpdateAPI(ctx(), api.APIID, &driver.UpdateAPIInput{Name: &newName, DisableExecuteAPIEndpoint: &disable})
	if err != nil || upd.Name != "renamed" || !upd.DisableExecuteAPIEndpoint {
		t.Fatalf("UpdateAPI: %v, %+v", err, upd)
	}

	// PATCH leaves untouched fields intact.
	if upd.ProtocolType != driver.ProtocolHTTP || upd.RouteSelectionExpression != "$request.method $request.path" {
		t.Fatalf("PATCH clobbered untouched fields: %+v", upd)
	}

	apis, err := m.GetAPIs(ctx())
	if err != nil || len(apis) != 1 {
		t.Fatalf("GetAPIs: %v, len=%d", err, len(apis))
	}

	if err := m.DeleteAPI(ctx(), api.APIID); err != nil {
		t.Fatalf("DeleteAPI: %v", err)
	}

	if _, err := m.GetAPI(ctx(), api.APIID); !cerrors.IsNotFound(err) {
		t.Fatalf("GetAPI after delete err = %v, want NotFound", err)
	}

	if err := m.DeleteAPI(ctx(), api.APIID); !cerrors.IsNotFound(err) {
		t.Fatalf("double DeleteAPI err = %v, want NotFound", err)
	}
}

func TestRouteCRUD(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)

	rt, err := m.CreateRoute(ctx(), api.APIID, &driver.CreateRouteInput{RouteKey: "GET /items"})
	if err != nil || rt.AuthorizationType != "NONE" {
		t.Fatalf("CreateRoute: %v, %+v", err, rt)
	}

	target := "integrations/abc"
	upd, err := m.UpdateRoute(ctx(), api.APIID, rt.RouteID, &driver.UpdateRouteInput{Target: &target})
	if err != nil || upd.Target != target || upd.RouteKey != "GET /items" {
		t.Fatalf("UpdateRoute: %v, %+v", err, upd)
	}

	routes, err := m.GetRoutes(ctx(), api.APIID)
	if err != nil || len(routes) != 1 {
		t.Fatalf("GetRoutes: %v, len=%d", err, len(routes))
	}

	if err := m.DeleteRoute(ctx(), api.APIID, rt.RouteID); err != nil {
		t.Fatalf("DeleteRoute: %v", err)
	}

	if _, err := m.GetRoute(ctx(), api.APIID, rt.RouteID); !cerrors.IsNotFound(err) {
		t.Fatalf("GetRoute after delete err = %v, want NotFound", err)
	}

	if _, err := m.CreateRoute(ctx(), api.APIID, &driver.CreateRouteInput{}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty routeKey err = %v, want InvalidArgument", err)
	}
}

func TestIntegrationCRUDAndTimeoutDefault(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)

	ig, err := m.CreateIntegration(ctx(), api.APIID, &driver.CreateIntegrationInput{
		IntegrationType: driver.IntegrationAWSProxy, IntegrationURI: "arn:aws:lambda:...",
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}

	if ig.ConnectionType != "INTERNET" || ig.PayloadFormatVersion != "1.0" || ig.TimeoutInMillis != 30000 {
		t.Fatalf("integration defaults wrong: %+v", ig)
	}

	// WebSocket API defaults the integration timeout to 29000.
	wsAPI, err := m.CreateAPI(ctx(), &driver.CreateAPIInput{Name: "ws", ProtocolType: driver.ProtocolWebSocket})
	if err != nil {
		t.Fatalf("CreateAPI ws: %v", err)
	}

	wsIg, err := m.CreateIntegration(ctx(), wsAPI.APIID, &driver.CreateIntegrationInput{IntegrationType: driver.IntegrationAWSProxy})
	if err != nil || wsIg.TimeoutInMillis != 29000 {
		t.Fatalf("ws integration timeout: %v, %+v", err, wsIg)
	}

	if err := m.DeleteIntegration(ctx(), api.APIID, ig.IntegrationID); err != nil {
		t.Fatalf("DeleteIntegration: %v", err)
	}

	if _, err := m.CreateIntegration(ctx(), api.APIID, &driver.CreateIntegrationInput{}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty integrationType err = %v, want InvalidArgument", err)
	}
}

func TestStageCRUDAndConflict(t *testing.T) {
	m := newMock(t)
	api := createHTTPAPI(t, m)

	st, err := m.CreateStage(ctx(), api.APIID, &driver.CreateStageInput{StageName: "$default", AutoDeploy: true})
	if err != nil || !st.AutoDeploy || st.CreatedDate != 1700000000 {
		t.Fatalf("CreateStage: %v, %+v", err, st)
	}

	if _, err := m.CreateStage(ctx(), api.APIID, &driver.CreateStageInput{StageName: "$default"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate stage err = %v, want AlreadyExists", err)
	}

	desc := "prod stage"
	upd, err := m.UpdateStage(ctx(), api.APIID, "$default", &driver.UpdateStageInput{Description: &desc})
	if err != nil || upd.Description != desc || !upd.AutoDeploy {
		t.Fatalf("UpdateStage: %v, %+v", err, upd)
	}

	stages, err := m.GetStages(ctx(), api.APIID)
	if err != nil || len(stages) != 1 {
		t.Fatalf("GetStages: %v, len=%d", err, len(stages))
	}

	if err := m.DeleteStage(ctx(), api.APIID, "$default"); err != nil {
		t.Fatalf("DeleteStage: %v", err)
	}

	if _, err := m.GetStage(ctx(), api.APIID, "$default"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetStage after delete err = %v, want NotFound", err)
	}
}

func TestSubResourcesOnMissingAPI(t *testing.T) {
	m := newMock(t)

	if _, err := m.GetRoutes(ctx(), "nope"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetRoutes missing api err = %v, want NotFound", err)
	}

	if _, err := m.CreateStage(ctx(), "nope", &driver.CreateStageInput{StageName: "x"}); !cerrors.IsNotFound(err) {
		t.Fatalf("CreateStage missing api err = %v, want NotFound", err)
	}
}
