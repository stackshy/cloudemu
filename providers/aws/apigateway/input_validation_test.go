package apigateway_test

import (
	"strings"
	"sync"
	"testing"
	"time"

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

	custom, err := m.CreateAuthorizer(ctx(), api.ID, &driver.CreateAuthorizerInput{
		Name: "lambda-auth", Type: driver.AuthorizerToken, AuthorizerURI: "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:auth/invocations",
	})
	if err != nil {
		t.Fatal(err)
	}

	pool, err := m.CreateAuthorizer(ctx(), api.ID, &driver.CreateAuthorizerInput{
		Name: "pool-auth", Type: driver.AuthorizerCognito, ProviderARNs: []string{"arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc"},
	})
	if err != nil {
		t.Fatal(err)
	}

	authorizers := map[string]string{"CUSTOM": custom.ID, "COGNITO_USER_POOLS": pool.ID}

	for i, good := range []string{"NONE", "AWS_IAM", "CUSTOM", "COGNITO_USER_POOLS"} {
		mth, err := m.PutMethod(ctx(), api.ID, api.RootResourceID, methods[i], driver.PutMethodInput{
			AuthorizationType: good, AuthorizerID: authorizers[good],
		})
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

func TestUpdateIntegrationRevalidatesTheResult(t *testing.T) {
	m := newMock(t)
	api := apiWithGetMethod(t, m)

	if err := putHTTPIntegration(m, api, 0); err != nil {
		t.Fatal(err)
	}

	for name, ops := range map[string][]driver.PatchOperation{
		"empty httpMethod":   {{Op: "replace", Path: "/httpMethod", Value: ""}},
		"unknown httpMethod": {{Op: "replace", Path: "/httpMethod", Value: "BOGUS"}},
		"non-http uri":       {{Op: "replace", Path: "/uri", Value: "ftp://example.com"}},
		"unknown type":       {{Op: "replace", Path: "/type", Value: "SOAP"}},
	} {
		if _, err := m.UpdateIntegration(ctx(), api.ID, api.RootResourceID, "GET", ops); !errors.IsInvalidArgument(err) {
			t.Fatalf("%s: expected InvalidArgument, got %v", name, err)
		}
	}

	got, err := m.GetIntegration(ctx(), api.ID, api.RootResourceID, "GET")
	if err != nil || got.IntegrationHTTPMethod != "GET" || got.Type != driver.IntegrationHTTP {
		t.Fatalf("rejected patches must leave the integration untouched: %v %+v", err, got)
	}
}

func TestStageVariableLengthAndCountLimits(t *testing.T) {
	m := newMock(t)
	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	repeat := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a'
		}

		return string(b)
	}

	many := func(n int) map[string]string {
		out := make(map[string]string, n)
		for i := 0; i < n; i++ {
			out["v"+string(rune('a'+i%26))+string(rune('a'+i/26))] = "x"
		}

		return out
	}

	deps, _ := m.GetDeployments(ctx(), apiID)

	for name, vars := range map[string]map[string]string{
		"65-char name": {repeat(65): "v"}, "513-char value": {"n": repeat(513)}, "101 variables": many(101),
	} {
		_, err := m.CreateStage(ctx(), apiID, driver.CreateStageInput{StageName: "lim", DeploymentID: deps[0].ID, Variables: vars})
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("CreateStage %s: expected InvalidArgument, got %v", name, err)
		}

		if _, err = m.CreateDeployment(ctx(), apiID, driver.CreateDeploymentInput{StageName: "lim2", Variables: vars}); !errors.IsInvalidArgument(err) {
			t.Fatalf("CreateDeployment %s: expected InvalidArgument, got %v", name, err)
		}
	}

	// The boundary values are accepted.
	if _, err := m.CreateStage(ctx(), apiID, driver.CreateStageInput{
		StageName: "edge", DeploymentID: deps[0].ID, Variables: map[string]string{repeat(64): repeat(512)},
	}); err != nil {
		t.Fatalf("64/512 must be accepted: %v", err)
	}

	if _, err := m.CreateStage(ctx(), apiID, driver.CreateStageInput{StageName: "hundred", DeploymentID: deps[0].ID, Variables: many(100)}); err != nil {
		t.Fatalf("100 variables must be accepted: %v", err)
	}

	// An update past the limit is rejected after the patches apply, and an update
	// of a value past 512 characters too; the stage keeps its 100 variables.
	_, err := m.UpdateStage(ctx(), apiID, "hundred", []driver.PatchOperation{{Op: "add", Path: "/variables/extra", Value: "x"}})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("101st variable via update: %v", err)
	}

	_, err = m.UpdateStage(ctx(), apiID, "hundred", []driver.PatchOperation{{Op: "replace", Path: "/variables/vaa", Value: repeat(600)}})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("600-char value via update: %v", err)
	}

	// A deployment's variables merge into the stage: past the limit is rejected.
	if _, err = m.CreateDeployment(ctx(), apiID, driver.CreateDeploymentInput{StageName: "hundred", Variables: map[string]string{"another": "x"}}); !errors.IsInvalidArgument(err) {
		t.Fatalf("deployment merging past the limit: %v", err)
	}

	st, _ := m.GetStage(ctx(), apiID, "hundred")
	if len(st.Variables) != 100 {
		t.Fatalf("rejected updates must leave the stage alone, has %d variables", len(st.Variables))
	}
}

func newAuthorizer(t *testing.T, m *apigateway.Mock, apiID string) *driver.Authorizer {
	t.Helper()

	az, err := m.CreateAuthorizer(ctx(), apiID, &driver.CreateAuthorizerInput{
		Name: "auth", Type: driver.AuthorizerToken, AuthorizerURI: strings.Replace(lambdaURI, "hello", "auth", 1),
	})
	if err != nil {
		t.Fatal(err)
	}

	return az
}

func TestDeleteAuthorizerAndModelAreRefusedWhileInUse(t *testing.T) {
	m := newMock(t)

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}

	az := newAuthorizer(t, m, api.ID)

	if _, err = m.CreateModel(ctx(), api.ID, &driver.CreateModelInput{Name: "Pet", Schema: `{"type":"object"}`}); err != nil {
		t.Fatal(err)
	}

	if _, err = m.PutMethod(ctx(), api.ID, api.RootResourceID, "GET", driver.PutMethodInput{
		AuthorizationType: "CUSTOM", AuthorizerID: az.ID, RequestModels: map[string]string{"application/json": "Pet"},
	}); err != nil {
		t.Fatal(err)
	}

	if err = m.DeleteAuthorizer(ctx(), api.ID, az.ID); !errors.IsInvalidArgument(err) {
		t.Fatalf("delete of a used authorizer: want InvalidArgument, got %v", err)
	}

	if err = m.DeleteModel(ctx(), api.ID, "Pet"); !errors.IsInvalidArgument(err) {
		t.Fatalf("delete of a used model: want InvalidArgument, got %v", err)
	}

	// Once the method is gone both deletes succeed.
	if err = m.DeleteMethod(ctx(), api.ID, api.RootResourceID, "GET"); err != nil {
		t.Fatal(err)
	}

	if err = m.DeleteAuthorizer(ctx(), api.ID, az.ID); err != nil {
		t.Fatalf("delete of an unused authorizer: %v", err)
	}

	if err = m.DeleteModel(ctx(), api.ID, "Pet"); err != nil {
		t.Fatalf("delete of an unused model: %v", err)
	}
}

func TestUpdateMethodChecksReferences(t *testing.T) {
	m := newMock(t)
	api := apiWithGetMethod(t, m)

	for _, ops := range [][]driver.PatchOperation{
		{{Op: "replace", Path: "/authorizationType", Value: "CUSTOM"}, {Op: "replace", Path: "/authorizerId", Value: "nope"}},
		{{Op: "replace", Path: "/requestValidatorId", Value: "nope"}},
		{{Op: "add", Path: "/requestModels/application~1json", Value: "Missing"}},
	} {
		if _, err := m.UpdateMethod(ctx(), api.ID, api.RootResourceID, "GET", ops); err == nil {
			t.Fatalf("patch %+v must be rejected", ops)
		}
	}
}

func TestUpdateIntegrationChecksTheConnection(t *testing.T) {
	m := newMock(t)
	api := apiWithGetMethod(t, m)

	if err := putHTTPIntegration(m, api, 0); err != nil {
		t.Fatal(err)
	}

	_, err := m.UpdateIntegration(ctx(), api.ID, api.RootResourceID, "GET", []driver.PatchOperation{
		{Op: "replace", Path: "/connectionType", Value: "VPC_LINK"}, {Op: "replace", Path: "/connectionId", Value: "nope"},
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("unknown VPC link: want InvalidArgument, got %v", err)
	}
}

// TestPutIntegrationVpcLinkDoesNotDeadlock loops PutIntegration(VPC_LINK) against
// link create/delete, which lock regionMu before the API lock.
func TestPutIntegrationVpcLinkDoesNotDeadlock(t *testing.T) {
	m := newMock(t)
	api := apiWithGetMethod(t, m)

	done := make(chan struct{})

	go func() {
		defer close(done)

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			for i := 0; i < 200; i++ {
				link, err := m.CreateVpcLink(ctx(), &driver.CreateVpcLinkInput{Name: "l", TargetARNs: []string{"arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/net/n/1"}})
				if err == nil {
					_ = m.DeleteVpcLink(ctx(), link.ID)
				}
			}
		}()

		go func() {
			defer wg.Done()

			for i := 0; i < 200; i++ {
				_, _ = m.PutIntegration(ctx(), api.ID, api.RootResourceID, "GET", driver.PutIntegrationInput{
					Type: driver.IntegrationHTTP, IntegrationHTTPMethod: "GET", URI: "https://example.com",
					ConnectionType: "VPC_LINK", ConnectionID: "missing",
				})
			}
		}()

		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("PutIntegration against VPC link create/delete deadlocked")
	}
}

func TestPutRestAPILeavesTheAPIUnchangedOnFailure(t *testing.T) {
	m := newMock(t)

	res, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(`{"swagger":"2.0","info":{"title":"t","version":"1"},
"paths":{"/pets":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}

	// The new document declares an authorizer scheme with a bad extension, which only warns, so
	// FailOnWarnings fails the call after everything was applied.
	_, err = m.PutRestAPI(ctx(), res.API.ID, &driver.PutRestAPIInput{Mode: driver.ImportOverwrite, FailOnWarnings: true, Body: []byte(`{
"swagger":"2.0","info":{"title":"t","version":"2"},
"paths":{"/other":{"get":{"responses":{"200":{"description":"ok"}}}}},
"definitions":{"bad name":{"type":"object"}}}`)})
	if err == nil {
		t.Fatal("failOnWarnings with a warning must fail")
	}

	resources, err := m.GetResources(ctx(), res.API.ID)
	if err != nil {
		t.Fatal(err)
	}

	paths := map[string]bool{}
	for i := range resources {
		paths[resources[i].Path] = true
	}

	if !paths["/pets"] || paths["/other"] {
		t.Fatalf("a failed overwrite must leave the API as it was, got %v", paths)
	}
}
