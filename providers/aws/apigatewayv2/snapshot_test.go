package apigatewayv2_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	src := newMock(t)
	api := createHTTPAPI(t, src)

	ig, err := src.CreateIntegration(ctx(), api.APIID, &driver.CreateIntegrationInput{
		IntegrationType: driver.IntegrationAWSProxy, IntegrationURI: "arn:aws:lambda:x",
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}

	rt, err := src.CreateRoute(ctx(), api.APIID, &driver.CreateRouteInput{
		RouteKey: "GET /items", Target: "integrations/" + ig.IntegrationID,
	})
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	if _, err := src.CreateStage(ctx(), api.APIID, &driver.CreateStageInput{StageName: "$default", AutoDeploy: true}); err != nil {
		t.Fatalf("CreateStage: %v", err)
	}

	data, err := src.Snapshot(ctx(), false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx(), data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	gotAPI, err := dst.GetAPI(ctx(), api.APIID)
	if err != nil || gotAPI.APIEndpoint != api.APIEndpoint {
		t.Fatalf("restored GetAPI: %v, %+v", err, gotAPI)
	}

	gotRt, err := dst.GetRoute(ctx(), api.APIID, rt.RouteID)
	if err != nil || gotRt.RouteKey != "GET /items" || gotRt.Target != "integrations/"+ig.IntegrationID {
		t.Fatalf("restored GetRoute: %v, %+v", err, gotRt)
	}

	gotIg, err := dst.GetIntegration(ctx(), api.APIID, ig.IntegrationID)
	if err != nil || gotIg.IntegrationType != driver.IntegrationAWSProxy {
		t.Fatalf("restored GetIntegration: %v, %+v", err, gotIg)
	}

	gotSt, err := dst.GetStage(ctx(), api.APIID, "$default")
	if err != nil || !gotSt.AutoDeploy {
		t.Fatalf("restored GetStage: %v, %+v", err, gotSt)
	}
}
