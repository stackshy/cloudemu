package apigateway_test

import (
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const helloTarget = "arn:aws:lambda:us-east-1:000000000000:function:hello"

// invokeStatus routes one GET through the given stage and returns the status.
func invokeStatus(t *testing.T, m *apigateway.Mock, apiID, stage, path string) int {
	t.Helper()

	resp, err := m.InvokeRoute(ctx(), &driver.ProxyRequest{
		RestAPIID: apiID, StageName: stage, HTTPMethod: "GET", Path: path,
	})
	if err != nil {
		t.Fatalf("InvokeRoute: %v", err)
	}

	return resp.StatusCode
}

// addLambdaMethod creates pathPart under the root with a GET AWS_PROXY method.
func addLambdaMethod(t *testing.T, m *apigateway.Mock, apiID, rootID, pathPart, uri string) string {
	t.Helper()

	res, err := m.CreateResource(ctx(), apiID, rootID, pathPart)
	if err != nil {
		t.Fatalf("CreateResource(%s): %v", pathPart, err)
	}

	if _, err := m.PutMethod(ctx(), apiID, res.ID, "GET", driver.PutMethodInput{}); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}

	if _, err := m.PutIntegration(ctx(), apiID, res.ID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationAWSProxy, IntegrationHTTPMethod: "POST", URI: uri,
	}); err != nil {
		t.Fatalf("PutIntegration: %v", err)
	}

	return res.ID
}

func assertMessage(t *testing.T, err error, isCode func(error) bool, want string) {
	t.Helper()

	if !isCode(err) {
		t.Fatalf("got %v, want a %q error of the expected code", err, want)
	}

	if got := errors.Message(err); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

// TestDeploymentSnapshotGatesLiveEdits proves a stage serves the tree captured
// at CreateDeployment time: edits to the live API are invisible until the API
// is redeployed.
func TestDeploymentSnapshotGatesLiveEdits(t *testing.T) {
	m := newMock(t)
	inv := &fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)}
	m.SetLambdaInvoker(inv)

	apiID, rootID, helloID := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	// A new resource added after the deploy is not live yet.
	addLambdaMethod(t, m, apiID, rootID, "fresh", lambdaURI)

	if got := invokeStatus(t, m, apiID, "prod", "/fresh"); got != 403 {
		t.Fatalf("undeployed /fresh = %d, want 403", got)
	}

	// Re-pointing the live integration does not change the deployed target.
	const otherURI = "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/" +
		"arn:aws:lambda:us-east-1:000000000000:function:other/invocations"

	if _, err := m.UpdateIntegration(ctx(), apiID, helloID, "GET", []driver.PatchOperation{
		{Op: "replace", Path: "/uri", Value: otherURI},
	}); err != nil {
		t.Fatalf("UpdateIntegration: %v", err)
	}

	if got := invokeStatus(t, m, apiID, "prod", "/hello"); got != 200 || inv.lastTarget != helloTarget {
		t.Fatalf("deployed /hello = %d via %q, want 200 via the deployed target", got, inv.lastTarget)
	}

	// Deleting the live method leaves the deployed one serving.
	if err := m.DeleteMethod(ctx(), apiID, helloID, "GET"); err != nil {
		t.Fatalf("DeleteMethod: %v", err)
	}

	if got := invokeStatus(t, m, apiID, "prod", "/hello"); got != 200 {
		t.Fatalf("deployed /hello after live delete = %d, want 200", got)
	}

	// Redeploying publishes the live tree.
	if _, err := m.CreateDeployment(ctx(), apiID, driver.CreateDeploymentInput{StageName: "prod"}); err != nil {
		t.Fatalf("redeploy: %v", err)
	}

	if got := invokeStatus(t, m, apiID, "prod", "/fresh"); got != 200 {
		t.Fatalf("redeployed /fresh = %d, want 200", got)
	}

	if got := invokeStatus(t, m, apiID, "prod", "/hello"); got != 403 {
		t.Fatalf("redeployed /hello (method deleted) = %d, want 403", got)
	}
}

// TestUpdateStageRepointsDeploymentSnapshot proves UpdateStage /deploymentId
// switches the stage back to an older deployment's tree.
func TestUpdateStageRepointsDeploymentSnapshot(t *testing.T) {
	m := newMock(t)
	m.SetLambdaInvoker(&fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)})

	apiID, rootID, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	first, err := m.GetStage(ctx(), apiID, "prod")
	if err != nil {
		t.Fatalf("GetStage: %v", err)
	}

	addLambdaMethod(t, m, apiID, rootID, "fresh", lambdaURI)

	if _, err := m.CreateDeployment(ctx(), apiID, driver.CreateDeploymentInput{StageName: "prod"}); err != nil {
		t.Fatalf("redeploy: %v", err)
	}

	if got := invokeStatus(t, m, apiID, "prod", "/fresh"); got != 200 {
		t.Fatalf("/fresh on second deployment = %d, want 200", got)
	}

	if _, err := m.UpdateStage(ctx(), apiID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/deploymentId", Value: first.DeploymentID},
	}); err != nil {
		t.Fatalf("UpdateStage: %v", err)
	}

	if got := invokeStatus(t, m, apiID, "prod", "/fresh"); got != 403 {
		t.Fatalf("/fresh after rollback = %d, want 403", got)
	}
}

// TestStageVariablesResolvedOnInvoke proves ${stageVariables.x} in a Lambda
// integration URI resolves per stage, the proxy event carries stageVariables,
// and a variable change applies without a redeploy.
func TestStageVariablesResolvedOnInvoke(t *testing.T) {
	m := newMock(t)
	inv := &fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)}
	m.SetLambdaInvoker(inv)

	const varURI = "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/" +
		"arn:aws:lambda:us-east-1:000000000000:function:${stageVariables.fn}/invocations"

	api, _ := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "vars"})
	addLambdaMethod(t, m, api.ID, api.RootResourceID, "hello", varURI)

	dep, err := m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{
		StageName: "prod", Variables: map[string]string{"fn": "hello-prod"},
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	if _, err := m.CreateStage(ctx(), api.ID, driver.CreateStageInput{
		StageName: "dev", DeploymentID: dep.ID, Variables: map[string]string{"fn": "hello-dev"},
	}); err != nil {
		t.Fatalf("CreateStage: %v", err)
	}

	invokeStatus(t, m, api.ID, "prod", "/hello")

	if inv.lastTarget != "arn:aws:lambda:us-east-1:000000000000:function:hello-prod" {
		t.Fatalf("prod target = %q", inv.lastTarget)
	}

	var event struct {
		StageVariables map[string]string `json:"stageVariables"`
	}
	_ = json.Unmarshal(inv.lastPayload, &event)

	if event.StageVariables["fn"] != "hello-prod" {
		t.Fatalf("event stageVariables = %v", event.StageVariables)
	}

	invokeStatus(t, m, api.ID, "dev", "/hello")

	if inv.lastTarget != "arn:aws:lambda:us-east-1:000000000000:function:hello-dev" {
		t.Fatalf("dev target = %q", inv.lastTarget)
	}

	if _, err := m.UpdateStage(ctx(), api.ID, "dev", []driver.PatchOperation{
		{Op: "replace", Path: "/variables/fn", Value: "hello-v2"},
	}); err != nil {
		t.Fatalf("UpdateStage: %v", err)
	}

	invokeStatus(t, m, api.ID, "dev", "/hello")

	if inv.lastTarget != "arn:aws:lambda:us-east-1:000000000000:function:hello-v2" {
		t.Fatalf("dev target after variable update = %q", inv.lastTarget)
	}
}

// TestCreateDeploymentRepointsExistingStage proves redeploying to an existing
// stage keeps its variables and description and merges new variables.
func TestCreateDeploymentRepointsExistingStage(t *testing.T) {
	m := newMock(t)
	api, _ := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "x"})
	addLambdaMethod(t, m, api.ID, api.RootResourceID, "hello", lambdaURI)

	first, err := m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{
		StageName: "prod", StageDescription: "production", Variables: map[string]string{"a": "1"},
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	second, err := m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{
		StageName: "prod", Variables: map[string]string{"b": "2"},
	})
	if err != nil {
		t.Fatalf("second CreateDeployment: %v", err)
	}

	st, _ := m.GetStage(ctx(), api.ID, "prod")
	if st.DeploymentID != second.ID || st.DeploymentID == first.ID {
		t.Fatalf("stage deploymentId = %q, want %q", st.DeploymentID, second.ID)
	}

	if st.Description != "production" || st.Variables["a"] != "1" || st.Variables["b"] != "2" {
		t.Fatalf("stage lost its settings on redeploy: %+v", st)
	}
}

func TestCreateDeploymentValidation(t *testing.T) {
	m := newMock(t)
	api, _ := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "x"})

	_, err := m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{StageName: "prod"})
	assertMessage(t, err, errors.IsInvalidArgument, "The REST API doesn't contain any methods")

	res, _ := m.CreateResource(ctx(), api.ID, api.RootResourceID, "pets")
	if _, err := m.PutMethod(ctx(), api.ID, res.ID, "GET", driver.PutMethodInput{}); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}

	_, err = m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{StageName: "prod"})
	assertMessage(t, err, errors.IsInvalidArgument, "No integration defined for method")

	if stages, _ := m.GetStages(ctx(), api.ID); len(stages) != 0 {
		t.Fatalf("a rejected deployment created stages: %+v", stages)
	}

	if _, err := m.PutIntegration(ctx(), api.ID, res.ID, "GET", driver.PutIntegrationInput{Type: "MOCK"}); err != nil {
		t.Fatalf("PutIntegration: %v", err)
	}

	_, err = m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{StageName: "bad.name"})
	assertMessage(t, err, errors.IsInvalidArgument, "Stage name only allows a-zA-Z0-9_-")

	if deps, _ := m.GetDeployments(ctx(), api.ID); len(deps) != 0 {
		t.Fatalf("a rejected deployment was stored: %+v", deps)
	}
}

func TestPutMethodValidation(t *testing.T) {
	m := newMock(t)
	api, _ := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "x"})
	res, _ := m.CreateResource(ctx(), api.ID, api.RootResourceID, "pets")

	_, err := m.PutMethod(ctx(), api.ID, res.ID, "FOO", driver.PutMethodInput{})
	assertMessage(t, err, errors.IsInvalidArgument, "Invalid HTTP method specified")

	_, err = m.PutMethod(ctx(), api.ID, "nope", "GET", driver.PutMethodInput{})
	assertMessage(t, err, errors.IsNotFound, "Invalid Resource identifier specified")

	for _, verb := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "ANY"} {
		if _, err := m.PutMethod(ctx(), api.ID, res.ID, verb, driver.PutMethodInput{}); err != nil {
			t.Fatalf("PutMethod(%s): %v", verb, err)
		}
	}

	_, err = m.PutMethod(ctx(), api.ID, res.ID, "GET", driver.PutMethodInput{})
	assertMessage(t, err, errors.IsAlreadyExists, "Method already exists for this resource")
}

func TestPutIntegrationValidation(t *testing.T) {
	m := newMock(t)
	api, _ := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "x"})
	res, _ := m.CreateResource(ctx(), api.ID, api.RootResourceID, "pets")

	if _, err := m.PutMethod(ctx(), api.ID, res.ID, "GET", driver.PutMethodInput{}); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}

	cases := []struct {
		name string
		in   driver.PutIntegrationInput
		want string
	}{
		{"bad type", driver.PutIntegrationInput{Type: "FOO"},
			"1 validation error detected: Value 'FOO' at 'putIntegrationInput.type' failed to satisfy " +
				"constraint: Member must satisfy enum value set: [HTTP, AWS, MOCK, HTTP_PROXY, AWS_PROXY]"},
		{"http no method", driver.PutIntegrationInput{Type: "HTTP", URI: "https://example.com"},
			"Enumeration value for HttpMethod must be non-empty"},
		{"http bad uri", driver.PutIntegrationInput{Type: "HTTP_PROXY", IntegrationHTTPMethod: "GET", URI: "not a url"},
			"Invalid HTTP endpoint specified for URI"},
		{"http no uri", driver.PutIntegrationInput{Type: "HTTP", IntegrationHTTPMethod: "GET"},
			"Invalid HTTP endpoint specified for URI"},
		{"aws bad arn", driver.PutIntegrationInput{Type: "AWS_PROXY", IntegrationHTTPMethod: "POST", URI: "hello"},
			"Invalid ARN specified in the request"},
		{"aws no path", driver.PutIntegrationInput{
			Type: "AWS", IntegrationHTTPMethod: "POST", URI: "arn:aws:apigateway:us-east-1:sqs:foo",
		}, "AWS ARN for integration must contain path or action"},
		{"aws_proxy not lambda", driver.PutIntegrationInput{
			Type: "AWS_PROXY", IntegrationHTTPMethod: "POST", URI: "arn:aws:apigateway:us-east-1:sqs:path/123/q",
		}, "Integrations of type 'AWS_PROXY' currently only supports Lambda function and Firehose stream invocations."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.PutIntegration(ctx(), api.ID, res.ID, "GET", tc.in)
			assertMessage(t, err, errors.IsInvalidArgument, tc.want)
		})
	}

	_, err := m.PutIntegration(ctx(), api.ID, res.ID, "POST", driver.PutIntegrationInput{Type: "MOCK"})
	assertMessage(t, err, errors.IsNotFound, "Invalid Method identifier specified")

	// A stage variable in the host is accepted, and MOCK needs no uri.
	for _, in := range []driver.PutIntegrationInput{
		{Type: "HTTP", IntegrationHTTPMethod: "GET", URI: "http://${stageVariables.host}/pets"},
		{Type: "MOCK"},
	} {
		if _, err := m.PutIntegration(ctx(), api.ID, res.ID, "GET", in); err != nil {
			t.Fatalf("PutIntegration(%+v): %v", in, err)
		}
	}
}

func TestCreateStageValidation(t *testing.T) {
	m := newMock(t)
	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)
	st, _ := m.GetStage(ctx(), apiID, "prod")

	_, err := m.CreateStage(ctx(), apiID, driver.CreateStageInput{StageName: "prod", DeploymentID: st.DeploymentID})
	assertMessage(t, err, errors.IsAlreadyExists, "Stage already exists")

	_, err = m.CreateStage(ctx(), apiID, driver.CreateStageInput{StageName: "bad.name", DeploymentID: st.DeploymentID})
	assertMessage(t, err, errors.IsInvalidArgument, "Stage name only allows a-zA-Z0-9_-")

	_, err = m.CreateStage(ctx(), apiID, driver.CreateStageInput{StageName: "qa", DeploymentID: "nope"})
	assertMessage(t, err, errors.IsNotFound, "Invalid Deployment identifier specified")

	if _, err := m.CreateStage(ctx(), apiID, driver.CreateStageInput{
		StageName: "qa-1_x", DeploymentID: st.DeploymentID,
	}); err != nil {
		t.Fatalf("CreateStage(qa-1_x): %v", err)
	}
}

// TestGetDeploymentAPISummary proves a deployment reports the method summary it
// captured, not the live tree.
func TestGetDeploymentAPISummary(t *testing.T) {
	m := newMock(t)
	apiID, rootID, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)
	st, _ := m.GetStage(ctx(), apiID, "prod")

	addLambdaMethod(t, m, apiID, rootID, "fresh", lambdaURI)

	dep, err := m.GetDeployment(ctx(), apiID, st.DeploymentID)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}

	got, ok := dep.APISummary["/hello"]["GET"]
	if !ok || got.AuthorizationType != "NONE" {
		t.Fatalf("apiSummary = %+v, want /hello GET NONE", dep.APISummary)
	}

	if _, leaked := dep.APISummary["/fresh"]; leaked {
		t.Fatalf("apiSummary includes an undeployed path: %+v", dep.APISummary)
	}
}

// TestSnapshotRestorePreservesDeploymentTrees proves persist keeps each
// deployment's captured tree, so a restored stage still serves what was
// deployed rather than the live edits.
func TestSnapshotRestorePreservesDeploymentTrees(t *testing.T) {
	src := newMock(t)
	apiID, rootID, _ := deployProxyAPI(t, src, "hello", "GET", lambdaURI)
	addLambdaMethod(t, src, apiID, rootID, "fresh", lambdaURI)

	data, err := src.Snapshot(ctx(), false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newMock(t)
	dst.SetLambdaInvoker(&fakeInvoker{output: []byte(`{"statusCode":200,"body":"ok"}`)})

	if err := dst.Restore(ctx(), data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := invokeStatus(t, dst, apiID, "prod", "/hello"); got != 200 {
		t.Fatalf("restored /hello = %d, want 200", got)
	}

	if got := invokeStatus(t, dst, apiID, "prod", "/fresh"); got != 403 {
		t.Fatalf("restored /fresh (never deployed) = %d, want 403", got)
	}
}
