package apigatewayv2_test

import (
	"net/http"
	"testing"
)

// TestE2E_ManualDeploymentLifecycle covers CreateDeployment, GetDeployment(s),
// UpdateDeployment and DeleteDeployment, and the stage that points at it.
func TestE2E_ManualDeploymentLifecycle(t *testing.T) {
	ts := newE2E(t)
	apiID := newHTTPAPI(t, ts.URL)
	apiBase := ts.URL + "/v2/apis/" + apiID

	wantErr(t, http.MethodPost, apiBase+"/deployments", `{}`,
		http.StatusBadRequest, "BadRequestException", "Unable to deploy API because no routes exist in this API")

	igID := newLambdaIntegration(t, apiBase)
	mustDo(t, http.MethodPost, apiBase+"/routes", `{"routeKey":"GET /items","target":"integrations/`+igID+`"}`, http.StatusCreated)
	mustDo(t, http.MethodPost, apiBase+"/stages", `{"stageName":"prod"}`, http.StatusCreated)

	wantErr(t, http.MethodPost, apiBase+"/deployments", `{"stageName":"nope"}`,
		http.StatusNotFound, "NotFoundException", "Invalid stage identifier specified")

	dep := mustDo(t, http.MethodPost, apiBase+"/deployments", `{"stageName":"prod","description":"v1"}`, http.StatusCreated)

	depID, _ := dep["deploymentId"].(string)
	if depID == "" || dep["deploymentStatus"] != "DEPLOYED" || dep["autoDeployed"] != false || dep["description"] != "v1" {
		t.Fatalf("CreateDeployment body = %v", dep)
	}

	stage := mustDo(t, http.MethodGet, apiBase+"/stages/prod", "", http.StatusOK)
	if stage["deploymentId"] != depID {
		t.Fatalf("stage deploymentId = %v, want %s", stage["deploymentId"], depID)
	}

	if stage["lastDeploymentStatusMessage"] != "Successfully deployed stage with deployment ID '"+depID+"'" {
		t.Fatalf("lastDeploymentStatusMessage = %v", stage["lastDeploymentStatusMessage"])
	}

	got := mustDo(t, http.MethodGet, apiBase+"/deployments/"+depID, "", http.StatusOK)
	if got["deploymentId"] != depID || got["createdDate"] == nil {
		t.Fatalf("GetDeployment = %v", got)
	}

	upd := mustDo(t, http.MethodPatch, apiBase+"/deployments/"+depID, `{"description":"v1b"}`, http.StatusOK)
	if upd["description"] != "v1b" {
		t.Fatalf("UpdateDeployment = %v", upd)
	}

	if n := len(items(mustDo(t, http.MethodGet, apiBase+"/deployments", "", http.StatusOK))); n != 1 {
		t.Fatalf("GetDeployments items = %d, want 1", n)
	}

	wantErr(t, http.MethodDelete, apiBase+"/deployments/"+depID, "",
		http.StatusBadRequest, "BadRequestException", "Active stages pointing to this deployment must be moved or deleted")

	mustDo(t, http.MethodDelete, apiBase+"/stages/prod", "", http.StatusNoContent)
	mustDo(t, http.MethodDelete, apiBase+"/deployments/"+depID, "", http.StatusNoContent)

	wantErr(t, http.MethodGet, apiBase+"/deployments/"+depID, "",
		http.StatusNotFound, "NotFoundException", "Invalid deployment identifier specified "+depID)
}

// TestE2E_StageRejectsUnknownDeploymentID proves a stage cannot point at a
// deployment that does not exist, on create or update.
func TestE2E_StageRejectsUnknownDeploymentID(t *testing.T) {
	ts := newE2E(t)
	apiID := newHTTPAPI(t, ts.URL)
	apiBase := ts.URL + "/v2/apis/" + apiID

	wantErr(t, http.MethodPost, apiBase+"/stages", `{"stageName":"prod","deploymentId":"bogus12345"}`,
		http.StatusBadRequest, "BadRequestException", "Invalid deployment identifier specified bogus12345")

	mustDo(t, http.MethodPost, apiBase+"/stages", `{"stageName":"prod"}`, http.StatusCreated)

	wantErr(t, http.MethodPatch, apiBase+"/stages/prod", `{"deploymentId":"bogus12345"}`,
		http.StatusBadRequest, "BadRequestException", "Invalid deployment identifier specified bogus12345")
}

// TestE2E_AutoDeployStageRedeploysOnChange proves an autoDeploy stage gets a
// new automatic deployment whenever a route or integration changes.
func TestE2E_AutoDeployStageRedeploysOnChange(t *testing.T) {
	ts := newE2E(t)
	apiID := newHTTPAPI(t, ts.URL)
	apiBase := ts.URL + "/v2/apis/" + apiID

	stage := mustDo(t, http.MethodPost, apiBase+"/stages", `{"stageName":"$default","autoDeploy":true}`, http.StatusCreated)
	if _, ok := stage["deploymentId"]; ok {
		t.Fatalf("autoDeploy stage on an API with no routes has a deployment: %v", stage)
	}

	igID := newLambdaIntegration(t, apiBase)
	mustDo(t, http.MethodPost, apiBase+"/routes", `{"routeKey":"GET /a","target":"integrations/`+igID+`"}`, http.StatusCreated)

	stage = mustDo(t, http.MethodGet, apiBase+"/stages/$default", "", http.StatusOK)

	first, _ := stage["deploymentId"].(string)
	if first == "" {
		t.Fatalf("autoDeploy stage not deployed after CreateRoute: %v", stage)
	}

	dep := mustDo(t, http.MethodGet, apiBase+"/deployments/"+first, "", http.StatusOK)
	if dep["autoDeployed"] != true || dep["deploymentStatus"] != "DEPLOYED" ||
		dep["description"] != "Automatic deployment triggered by changes to the Api configuration" {
		t.Fatalf("auto deployment = %v", dep)
	}

	mustDo(t, http.MethodPost, apiBase+"/routes", `{"routeKey":"GET /b","target":"integrations/`+igID+`"}`, http.StatusCreated)

	stage = mustDo(t, http.MethodGet, apiBase+"/stages/$default", "", http.StatusOK)
	if second, _ := stage["deploymentId"].(string); second == "" || second == first {
		t.Fatalf("autoDeploy stage not redeployed: first=%s now=%v", first, stage["deploymentId"])
	}

	wantErr(t, http.MethodPatch, apiBase+"/stages/$default", `{"deploymentId":"`+first+`"}`,
		http.StatusBadRequest, "BadRequestException", "DeploymentId can't be updated if autoDeploy is enabled")

	// Switching autoDeploy off in the same call lets the stage pin a deployment.
	pinned := mustDo(t, http.MethodPatch, apiBase+"/stages/$default", `{"autoDeploy":false,"deploymentId":"`+first+`"}`, http.StatusOK)
	if pinned["deploymentId"] != first || pinned["autoDeploy"] != false {
		t.Fatalf("pin deployment = %v", pinned)
	}
}
