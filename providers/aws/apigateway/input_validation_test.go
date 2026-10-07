package apigateway_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// apiWithGetMethod builds an API whose root resource has a GET method.
func apiWithGetMethod(t *testing.T, m *apigateway.Mock) *driver.RestAPI {
	t.Helper()

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = m.PutMethod(ctx(), api.ID, api.RootResourceID, "GET", driver.PutMethodInput{}); err != nil {
		t.Fatal(err)
	}

	return api
}

func putHTTPIntegration(m *apigateway.Mock, api *driver.RestAPI, timeout int) error {
	_, err := m.PutIntegration(ctx(), api.ID, api.RootResourceID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationHTTP, IntegrationHTTPMethod: "GET", URI: "https://example.com", TimeoutInMillis: timeout,
	})

	return err
}

func TestPutMethodRejectsUnknownAuthorizationType(t *testing.T) {
	m := newMock(t)

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"BASIC", "none", "IAM", "JWT"} {
		_, err = m.PutMethod(ctx(), api.ID, api.RootResourceID, "GET", driver.PutMethodInput{AuthorizationType: bad})
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("authorizationType %q: expected InvalidArgument, got %v", bad, err)
		}
	}

	// The rejected calls stored nothing, and every real type is accepted.
	methods := []string{"GET", "POST", "PUT", "DELETE"}

	for i, good := range []string{"NONE", "AWS_IAM", "CUSTOM", "COGNITO_USER_POOLS"} {
		mth, err := m.PutMethod(ctx(), api.ID, api.RootResourceID, methods[i], driver.PutMethodInput{AuthorizationType: good})
		if err != nil || mth.AuthorizationType != good {
			t.Fatalf("authorizationType %q: %v %+v", good, err, mth)
		}
	}
}

func TestUpdateMethodRejectsUnknownAuthorizationType(t *testing.T) {
	m := newMock(t)
	api := apiWithGetMethod(t, m)

	_, err := m.UpdateMethod(ctx(), api.ID, api.RootResourceID, "GET", []driver.PatchOperation{
		{Op: "replace", Path: "/authorizationType", Value: "BASIC"},
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}

	got, err := m.GetMethod(ctx(), api.ID, api.RootResourceID, "GET")
	if err != nil || got.AuthorizationType != "NONE" {
		t.Fatalf("rejected patch must leave the method untouched: %v %+v", err, got)
	}

	if _, err = m.UpdateMethod(ctx(), api.ID, api.RootResourceID, "GET", []driver.PatchOperation{
		{Op: "replace", Path: "/authorizationType", Value: "AWS_IAM"},
	}); err != nil {
		t.Fatalf("AWS_IAM patch: %v", err)
	}
}

func TestIntegrationTimeoutRange(t *testing.T) {
	m := newMock(t)
	api := apiWithGetMethod(t, m)

	for _, bad := range []int{-1, 1, 49, 29001, 100000} {
		if err := putHTTPIntegration(m, api, bad); !errors.IsInvalidArgument(err) {
			t.Fatalf("timeout %d: expected InvalidArgument, got %v", bad, err)
		}
	}

	if _, err := m.GetIntegration(ctx(), api.ID, api.RootResourceID, "GET"); !errors.IsNotFound(err) {
		t.Fatalf("rejected puts must store no integration, got %v", err)
	}

	for _, good := range []int{50, 5000, 29000} {
		if err := putHTTPIntegration(m, api, good); err != nil {
			t.Fatalf("timeout %d: %v", good, err)
		}
	}

	// Omitted keeps the 29000ms default.
	if err := putHTTPIntegration(m, api, 0); err != nil {
		t.Fatal(err)
	}

	ig, _ := m.GetIntegration(ctx(), api.ID, api.RootResourceID, "GET")
	if ig.TimeoutInMillis != 29000 {
		t.Fatalf("default timeout = %d, want 29000", ig.TimeoutInMillis)
	}
}

func TestUpdateIntegrationTimeoutAndHTTPMethodPatch(t *testing.T) {
	m := newMock(t)
	api := apiWithGetMethod(t, m)

	if err := putHTTPIntegration(m, api, 0); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"10", "30000", "abc", ""} {
		_, err := m.UpdateIntegration(ctx(), api.ID, api.RootResourceID, "GET", []driver.PatchOperation{
			{Op: "replace", Path: "/timeoutInMillis", Value: bad},
		})
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("timeout patch %q: expected InvalidArgument, got %v", bad, err)
		}
	}

	// /httpMethod is the Integration resource's member; /integrationHttpMethod
	// (PutIntegration's parameter name) keeps working.
	for _, path := range []string{"/httpMethod", "/integrationHttpMethod"} {
		want := map[string]string{"/httpMethod": "POST", "/integrationHttpMethod": "PUT"}[path]

		ig, err := m.UpdateIntegration(ctx(), api.ID, api.RootResourceID, "GET", []driver.PatchOperation{
			{Op: "replace", Path: path, Value: want}, {Op: "replace", Path: "/timeoutInMillis", Value: "5000"},
		})
		if err != nil || ig.IntegrationHTTPMethod != want || ig.TimeoutInMillis != 5000 {
			t.Fatalf("patch %s: %v %+v", path, err, ig)
		}
	}
}

func TestStageVariableRules(t *testing.T) {
	m := newMock(t)
	api := apiWithGetMethod(t, m)

	if _, err := m.PutIntegration(ctx(), api.ID, api.RootResourceID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationMock,
	}); err != nil {
		t.Fatal(err)
	}

	dep, err := m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{})
	if err != nil {
		t.Fatal(err)
	}

	badVars := []map[string]string{
		{"bad-name": "v"}, {"has space": "v"}, {"": "v"},
		{"name": "has space"}, {"name": "semi;colon"}, {"name": ""}, {"name": "quote\""},
	}

	for _, vars := range badVars {
		_, err = m.CreateStage(ctx(), api.ID, driver.CreateStageInput{StageName: "s", DeploymentID: dep.ID, Variables: vars})
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("CreateStage %v: expected InvalidArgument, got %v", vars, err)
		}

		_, err = m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{StageName: "d", Variables: vars})
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("CreateDeployment %v: expected InvalidArgument, got %v", vars, err)
		}
	}

	// Neither rejected call left a stage behind.
	stages, _ := m.GetStages(ctx(), api.ID)
	if len(stages) != 0 {
		t.Fatalf("rejected creates left %d stages", len(stages))
	}

	good := map[string]string{"Env_1": "prod-1.x/a?b=c&d#e,f:g~h"}

	st, err := m.CreateStage(ctx(), api.ID, driver.CreateStageInput{StageName: "s", DeploymentID: dep.ID, Variables: good})
	if err != nil || st.Variables["Env_1"] != good["Env_1"] {
		t.Fatalf("valid variables: %v %+v", err, st)
	}

	_, err = m.UpdateStage(ctx(), api.ID, "s", []driver.PatchOperation{{Op: "add", Path: "/variables/bad-name", Value: "v"}})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("patch bad name: expected InvalidArgument, got %v", err)
	}

	_, err = m.UpdateStage(ctx(), api.ID, "s", []driver.PatchOperation{{Op: "replace", Path: "/variables/Env_1", Value: "bad value"}})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("patch bad value: expected InvalidArgument, got %v", err)
	}

	got, _ := m.GetStage(ctx(), api.ID, "s")
	if got.Variables["Env_1"] != good["Env_1"] || len(got.Variables) != 1 {
		t.Fatalf("rejected patches must leave variables untouched, got %v", got.Variables)
	}

	// Removing a variable never validates its (absent) value.
	st, err = m.UpdateStage(ctx(), api.ID, "s", []driver.PatchOperation{{Op: "remove", Path: "/variables/Env_1"}})
	if err != nil || len(st.Variables) != 0 {
		t.Fatalf("remove: %v %+v", err, st)
	}
}
