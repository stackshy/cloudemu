package apigateway_test

import (
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

func newAPI(t *testing.T, m *apigateway.Mock) *driver.RestAPI {
	t.Helper()

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "ext"})
	if err != nil {
		t.Fatal(err)
	}

	return api
}

func TestAPIKeysLifecycle(t *testing.T) {
	m := newMock(t)

	if _, err := m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "short", Value: "tooshort"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("short value must be rejected: %v", err)
	}

	key, err := m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k1", Enabled: true, CustomerID: "c1", Tags: map[string]string{"a": "b"}})
	if err != nil || key.Value == "" || len(key.Value) < 20 {
		t.Fatalf("create: %v %+v", err, key)
	}

	if _, err = m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "dup", Value: key.Value}); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate value must conflict: %v", err)
	}

	got, err := m.GetAPIKey(ctx(), key.ID, false)
	if err != nil || got.Value != "" {
		t.Fatalf("value must be hidden by default: %v %+v", err, got)
	}

	got, _ = m.GetAPIKey(ctx(), key.ID, true)
	if got.Value != key.Value {
		t.Fatal("includeValue must return the value")
	}

	page, err := m.GetAPIKeys(ctx(), &driver.GetAPIKeysInput{NameQuery: "k", CustomerID: "c1"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Value != "" {
		t.Fatalf("list: %v %+v", err, page)
	}

	upd, err := m.UpdateAPIKey(ctx(), key.ID, []driver.PatchOperation{
		{Op: "replace", Path: "/enabled", Value: "false"}, {Op: "replace", Path: "/name", Value: "renamed"},
	})
	if err != nil || upd.Enabled || upd.Name != "renamed" {
		t.Fatalf("update: %v %+v", err, upd)
	}

	if _, err = m.UpdateAPIKey(ctx(), key.ID, []driver.PatchOperation{{Op: "replace", Path: "/bogus", Value: "x"}}); !errors.IsInvalidArgument(err) {
		t.Fatalf("unknown patch path: %v", err)
	}

	if err = m.DeleteAPIKey(ctx(), key.ID); err != nil {
		t.Fatal(err)
	}

	if _, err = m.GetAPIKey(ctx(), key.ID, false); !errors.IsNotFound(err) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestAPIKeyStageKeysMustExist(t *testing.T) {
	m := newMock(t)
	api := newAPI(t, m)

	_, err := m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", StageKeys: []driver.StageKey{{RestAPIID: api.ID, StageName: "nope"}}})
	if !errors.IsNotFound(err) {
		t.Fatalf("unknown stage: %v", err)
	}
}

func TestUsagePlanLifecycleAndKeys(t *testing.T) {
	m := newMock(t)

	if _, err := m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("name required: %v", err)
	}

	if _, err := m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{Name: "p", Quota: &driver.QuotaSettings{Limit: 1, Period: "YEAR"}}); !errors.IsInvalidArgument(err) {
		t.Fatalf("bad period: %v", err)
	}

	plan, err := m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{
		Name: "gold", Throttle: &driver.ThrottleSettings{BurstLimit: 5, RateLimit: 10}, Quota: &driver.QuotaSettings{Limit: 100, Period: "DAY"},
	})
	if err != nil {
		t.Fatal(err)
	}

	key, _ := m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", Enabled: true})

	if _, err = m.CreateUsagePlanKey(ctx(), plan.ID, key.ID, "BEARER"); !errors.IsInvalidArgument(err) {
		t.Fatalf("key type: %v", err)
	}

	if _, err = m.CreateUsagePlanKey(ctx(), plan.ID, key.ID, "API_KEY"); err != nil {
		t.Fatal(err)
	}

	if _, err = m.CreateUsagePlanKey(ctx(), plan.ID, key.ID, "API_KEY"); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate attach: %v", err)
	}

	keys, err := m.GetUsagePlanKeys(ctx(), plan.ID, "", driver.PageInput{})
	if err != nil || len(keys.Items) != 1 || keys.Items[0].Value != key.Value {
		t.Fatalf("plan keys: %v %+v", err, keys)
	}

	plans, _ := m.GetUsagePlans(ctx(), key.ID, driver.PageInput{})
	if len(plans.Items) != 1 {
		t.Fatalf("plans containing key: %+v", plans)
	}

	upd, err := m.UpdateUsagePlan(ctx(), plan.ID, []driver.PatchOperation{
		{Op: "replace", Path: "/throttle/rateLimit", Value: "20"}, {Op: "replace", Path: "/quota/limit", Value: "7"},
	})
	if err != nil || upd.Throttle.RateLimit != 20 || upd.Quota.Limit != 7 {
		t.Fatalf("update: %v %+v", err, upd)
	}

	if err = m.DeleteUsagePlanKey(ctx(), plan.ID, key.ID); err != nil {
		t.Fatal(err)
	}

	if err = m.DeleteUsagePlan(ctx(), plan.ID); err != nil {
		t.Fatal(err)
	}

	if _, err = m.GetUsagePlan(ctx(), plan.ID); !errors.IsNotFound(err) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestAuthorizerValidation(t *testing.T) {
	m := newMock(t)
	api := newAPI(t, m)

	bad := []driver.CreateAuthorizerInput{
		{Type: driver.AuthorizerToken, AuthorizerURI: "x"},
		{Name: "n", Type: "BOGUS"},
		{Name: "n", Type: driver.AuthorizerToken},
		{Name: "n", Type: driver.AuthorizerCognito},
		{Name: "n", Type: driver.AuthorizerToken, AuthorizerURI: "x", AuthorizerResultTTLInSeconds: intPtr(4000)},
		{Name: "n", Type: driver.AuthorizerRequest, AuthorizerURI: "x", AuthorizerResultTTLInSeconds: intPtr(60)},
	}

	for i := range bad {
		if _, err := m.CreateAuthorizer(ctx(), api.ID, &bad[i]); !errors.IsInvalidArgument(err) {
			t.Fatalf("case %d must be rejected: %v", i, err)
		}
	}

	az, err := m.CreateAuthorizer(ctx(), api.ID, &driver.CreateAuthorizerInput{Name: "tok", Type: driver.AuthorizerToken, AuthorizerURI: "uri"})
	if err != nil || az.IdentitySource != "method.request.header.Authorization" || *az.AuthorizerResultTTLInSeconds != 300 {
		t.Fatalf("defaults: %v %+v", err, az)
	}

	upd, err := m.UpdateAuthorizer(ctx(), api.ID, az.ID, []driver.PatchOperation{{Op: "replace", Path: "/authorizerResultTtlInSeconds", Value: "0"}})
	if err != nil || *upd.AuthorizerResultTTLInSeconds != 0 {
		t.Fatalf("update: %v %+v", err, upd)
	}

	list, err := m.GetAuthorizers(ctx(), api.ID, driver.PageInput{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	if err = m.DeleteAuthorizer(ctx(), api.ID, az.ID); err != nil {
		t.Fatal(err)
	}

	if _, err = m.GetAuthorizer(ctx(), api.ID, az.ID); !errors.IsNotFound(err) {
		t.Fatalf("after delete: %v", err)
	}
}

func intPtr(n int) *int { return &n }

func TestMethodReferencesAreValidated(t *testing.T) {
	m := newMock(t)
	api := newAPI(t, m)

	if _, err := m.PutMethod(ctx(), api.ID, api.RootResourceID, "GET", driver.PutMethodInput{AuthorizationType: "CUSTOM"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("CUSTOM without authorizer: %v", err)
	}

	if _, err := m.PutMethod(ctx(), api.ID, api.RootResourceID, "GET", driver.PutMethodInput{RequestValidatorID: "nope"}); !errors.IsNotFound(err) {
		t.Fatalf("unknown validator: %v", err)
	}

	if _, err := m.PutMethod(ctx(), api.ID, api.RootResourceID, "GET", driver.PutMethodInput{RequestModels: map[string]string{"application/json": "Missing"}}); !errors.IsInvalidArgument(err) {
		t.Fatalf("unknown model: %v", err)
	}

	if _, err := m.PutMethod(ctx(), api.ID, api.RootResourceID, "GET", driver.PutMethodInput{RequestModels: map[string]string{"application/json": "Empty"}}); err != nil {
		t.Fatalf("default Empty model must exist: %v", err)
	}
}

func TestModelsAndValidators(t *testing.T) {
	m := newMock(t)
	api := newAPI(t, m)

	models, err := m.GetModels(ctx(), api.ID, driver.PageInput{})
	if err != nil || len(models.Items) != 2 || models.Items[0].Name != "Empty" || models.Items[1].Name != "Error" {
		t.Fatalf("default models: %v %+v", err, models)
	}

	if _, err = m.CreateModel(ctx(), api.ID, &driver.CreateModelInput{Name: "bad name", Schema: "{}"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("name pattern: %v", err)
	}

	if _, err = m.CreateModel(ctx(), api.ID, &driver.CreateModelInput{Name: "Pet", Schema: "not json"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("schema must be JSON: %v", err)
	}

	mod, err := m.CreateModel(ctx(), api.ID, &driver.CreateModelInput{Name: "Pet", Schema: `{"type":"object"}`})
	if err != nil || mod.ContentType != "application/json" {
		t.Fatalf("create: %v %+v", err, mod)
	}

	if _, err = m.CreateModel(ctx(), api.ID, &driver.CreateModelInput{Name: "Pet", Schema: "{}"}); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate: %v", err)
	}

	if _, err = m.UpdateModel(ctx(), api.ID, "Empty", []driver.PatchOperation{{Op: "replace", Path: "/description", Value: "x"}}); !errors.IsInvalidArgument(err) {
		t.Fatalf("default models are read-only: %v", err)
	}

	if _, err = m.UpdateModel(ctx(), api.ID, "Pet", []driver.PatchOperation{{Op: "replace", Path: "/description", Value: "pets"}}); err != nil {
		t.Fatal(err)
	}

	if err = m.DeleteModel(ctx(), api.ID, "Pet"); err != nil {
		t.Fatal(err)
	}

	v, err := m.CreateRequestValidator(ctx(), api.ID, &driver.CreateRequestValidatorInput{Name: "both", ValidateRequestBody: true, ValidateRequestParameters: true})
	if err != nil || !v.ValidateRequestBody {
		t.Fatalf("validator: %v %+v", err, v)
	}

	upd, err := m.UpdateRequestValidator(ctx(), api.ID, v.ID, []driver.PatchOperation{{Op: "replace", Path: "/validateRequestBody", Value: "false"}})
	if err != nil || upd.ValidateRequestBody {
		t.Fatalf("update: %v %+v", err, upd)
	}

	if _, err = m.CreateRequestValidator(ctx(), api.ID, &driver.CreateRequestValidatorInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("name required: %v", err)
	}

	if err = m.DeleteRequestValidator(ctx(), api.ID, v.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayResponses(t *testing.T) {
	m := newMock(t)
	api := newAPI(t, m)

	list, err := m.GetGatewayResponses(ctx(), api.ID, driver.PageInput{Limit: 100})
	if err != nil || len(list.Items) != 21 {
		t.Fatalf("every response type is listed: %v %d", err, len(list.Items))
	}

	gr, err := m.GetGatewayResponse(ctx(), api.ID, "THROTTLED")
	if err != nil || !gr.DefaultResponse || gr.StatusCode != "429" {
		t.Fatalf("default: %v %+v", err, gr)
	}

	if _, err = m.PutGatewayResponse(ctx(), api.ID, "NOPE", &driver.PutGatewayResponseInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("unknown type: %v", err)
	}

	if _, err = m.PutGatewayResponse(ctx(), api.ID, "THROTTLED", &driver.PutGatewayResponseInput{StatusCode: "99"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("bad status: %v", err)
	}

	put, err := m.PutGatewayResponse(ctx(), api.ID, "THROTTLED", &driver.PutGatewayResponseInput{StatusCode: "503", ResponseTemplates: map[string]string{"application/json": `{"slow":true}`}})
	if err != nil || put.DefaultResponse || put.StatusCode != "503" {
		t.Fatalf("put: %v %+v", err, put)
	}

	upd, err := m.UpdateGatewayResponse(ctx(), api.ID, "UNAUTHORIZED", []driver.PatchOperation{{Op: "replace", Path: "/statusCode", Value: "418"}})
	if err != nil || upd.StatusCode != "418" {
		t.Fatalf("update from default: %v %+v", err, upd)
	}

	if err = m.DeleteGatewayResponse(ctx(), api.ID, "THROTTLED"); err != nil {
		t.Fatal(err)
	}

	if gr, _ = m.GetGatewayResponse(ctx(), api.ID, "THROTTLED"); !gr.DefaultResponse {
		t.Fatal("delete must restore the default")
	}

	if err = m.DeleteGatewayResponse(ctx(), api.ID, "THROTTLED"); !errors.IsNotFound(err) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDomainNamesAndBasePathMappings(t *testing.T) {
	m := newMock(t)
	api, stage := deployedStage(t, m)

	bad := []driver.CreateDomainNameInput{
		{DomainName: "NotLower.example.com", CertificateARN: "arn"}, {DomainName: "nodot", CertificateARN: "arn"},
		{DomainName: "a.example.com"}, {DomainName: "a.example.com", CertificateARN: "arn", SecurityPolicy: "TLS_9"},
		{DomainName: "a.example.com", RegionalCertificateARN: "arn", EndpointConfigurationType: []string{"PRIVATE"}},
	}

	for i := range bad {
		if _, err := m.CreateDomainName(ctx(), &bad[i]); !errors.IsInvalidArgument(err) {
			t.Fatalf("case %d: %v", i, err)
		}
	}

	dn, err := m.CreateDomainName(ctx(), &driver.CreateDomainNameInput{
		DomainName: "api.example.com", RegionalCertificateARN: "arn:aws:acm:us-east-1:000000000000:certificate/x",
		EndpointConfigurationType: []string{"REGIONAL"},
	})
	if err != nil || dn.RegionalDomainName == "" || dn.DomainNameStatus != "AVAILABLE" {
		t.Fatalf("create: %v %+v", err, dn)
	}

	if _, err = m.CreateDomainName(ctx(), &driver.CreateDomainNameInput{DomainName: "api.example.com", CertificateARN: "arn"}); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate: %v", err)
	}

	if _, err = m.CreateBasePathMapping(ctx(), "api.example.com", driver.BasePathMapping{RestAPIID: api, Stage: "missing"}); !errors.IsNotFound(err) {
		t.Fatalf("unknown stage: %v", err)
	}

	if _, err = m.CreateBasePathMapping(ctx(), "api.example.com", driver.BasePathMapping{BasePath: "a/b", RestAPIID: api, Stage: stage}); !errors.IsInvalidArgument(err) {
		t.Fatalf("slash in base path: %v", err)
	}

	bpm, err := m.CreateBasePathMapping(ctx(), "api.example.com", driver.BasePathMapping{RestAPIID: api, Stage: stage})
	if err != nil || bpm.BasePath != "(none)" {
		t.Fatalf("root mapping: %v %+v", err, bpm)
	}

	if _, err = m.CreateBasePathMapping(ctx(), "api.example.com", driver.BasePathMapping{RestAPIID: api, Stage: stage}); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate mapping: %v", err)
	}

	if _, err = m.CreateBasePathMapping(ctx(), "api.example.com", driver.BasePathMapping{BasePath: "v1", RestAPIID: api, Stage: stage}); err != nil {
		t.Fatal(err)
	}

	api2, stg, rest, ok := m.ResolveDomain("api.example.com", "/v1/pets")
	if !ok || api2 != api || stg != stage || rest != "/pets" {
		t.Fatalf("resolve v1: %v %s %s %s", ok, api2, stg, rest)
	}

	if _, _, rest, ok = m.ResolveDomain("api.example.com", "/pets"); !ok || rest != "/pets" {
		t.Fatalf("resolve (none): %v %s", ok, rest)
	}

	if _, _, _, ok = m.ResolveDomain("other.example.com", "/pets"); ok {
		t.Fatal("unregistered host must not resolve")
	}

	if _, err = m.UpdateBasePathMapping(ctx(), "api.example.com", "v1", []driver.PatchOperation{{Op: "replace", Path: "/basePath", Value: "v2"}}); err != nil {
		t.Fatal(err)
	}

	if err = m.DeleteDomainName(ctx(), "api.example.com"); err != nil {
		t.Fatal(err)
	}

	if _, err = m.GetBasePathMappings(ctx(), "api.example.com", driver.PageInput{}); !errors.IsNotFound(err) {
		t.Fatalf("mappings must go with the domain: %v", err)
	}
}

// deployedStage deploys a one-method API to "prod" and returns its id and stage.
func deployedStage(t *testing.T, m *apigateway.Mock) (apiID, stage string) {
	t.Helper()

	id, _, _ := deployProxyAPI(t, m, "pets", "GET", lambdaURI)

	return id, "prod"
}

func TestVpcLinks(t *testing.T) {
	m := newMock(t)

	const nlb = "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/net/my-nlb/50dc6c495c0c9188"

	for _, bad := range []driver.CreateVpcLinkInput{{TargetARNs: []string{nlb}}, {Name: "n"}, {Name: "n", TargetARNs: []string{"not-an-arn"}}} {
		if _, err := m.CreateVpcLink(ctx(), &bad); !errors.IsInvalidArgument(err) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}

	link, err := m.CreateVpcLink(ctx(), &driver.CreateVpcLinkInput{Name: "link", TargetARNs: []string{nlb}})
	if err != nil || link.Status != "AVAILABLE" {
		t.Fatalf("create: %v %+v", err, link)
	}

	upd, err := m.UpdateVpcLink(ctx(), link.ID, []driver.PatchOperation{{Op: "replace", Path: "/name", Value: "renamed"}})
	if err != nil || upd.Name != "renamed" {
		t.Fatalf("update: %v %+v", err, upd)
	}

	// An integration that goes through the link blocks its deletion.
	api := newAPI(t, m)
	if _, err = m.PutMethod(ctx(), api.ID, api.RootResourceID, "GET", driver.PutMethodInput{}); err != nil {
		t.Fatal(err)
	}

	if _, err = m.PutIntegration(ctx(), api.ID, api.RootResourceID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationHTTP, IntegrationHTTPMethod: "GET", URI: "http://nlb.internal/x", ConnectionType: "VPC_LINK", ConnectionID: "missing",
	}); !errors.IsInvalidArgument(err) {
		t.Fatalf("unknown link: %v", err)
	}

	if _, err = m.PutIntegration(ctx(), api.ID, api.RootResourceID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationHTTP, IntegrationHTTPMethod: "GET", URI: "http://nlb.internal/x", ConnectionType: "VPC_LINK", ConnectionID: link.ID,
	}); err != nil {
		t.Fatal(err)
	}

	if err = m.DeleteVpcLink(ctx(), link.ID); !errors.IsFailedPrecondition(err) {
		t.Fatalf("in-use link: %v", err)
	}

	if err = m.DeleteIntegration(ctx(), api.ID, api.RootResourceID, "GET"); err != nil {
		t.Fatal(err)
	}

	if err = m.DeleteVpcLink(ctx(), link.ID); err != nil {
		t.Fatal(err)
	}
}

func TestTagsOnNewResources(t *testing.T) {
	m := newMock(t)
	api, stage := deployedStage(t, m)

	key, _ := m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k"})
	plan, _ := m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{Name: "p"})

	arns := []string{
		"arn:aws:apigateway:us-east-1::/restapis/" + api + "/stages/" + stage,
		"arn:aws:apigateway:us-east-1::/apikeys/" + key.ID,
		"arn:aws:apigateway:us-east-1::/usageplans/" + plan.ID,
	}

	for _, arn := range arns {
		if err := m.TagResource(ctx(), arn, map[string]string{"team": "x"}); err != nil {
			t.Fatalf("%s: %v", arn, err)
		}

		tags, err := m.GetTags(ctx(), arn)
		if err != nil || tags["team"] != "x" {
			t.Fatalf("%s: %v %v", arn, err, tags)
		}

		if err = m.UntagResource(ctx(), arn, []string{"team"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.TagResource(ctx(), "arn:aws:apigateway:us-east-1::/apikeys/missing", map[string]string{"a": "b"}); !errors.IsNotFound(err) {
		t.Fatalf("missing key: %v", err)
	}

	if err := m.TagResource(ctx(), "arn:aws:apigateway:us-east-1::/restapis/"+api+"/stages/nope", map[string]string{"a": "b"}); !errors.IsNotFound(err) {
		t.Fatalf("missing stage: %v", err)
	}
}

func TestStageLoggingSettingsValidation(t *testing.T) {
	m := newMock(t)
	api, stage := deployedStage(t, m)

	enable := []driver.PatchOperation{{Op: "replace", Path: "/*/*/logging/loglevel", Value: "INFO"}}

	if _, err := m.UpdateStage(ctx(), api, stage, enable); !errors.IsInvalidArgument(err) || !strings.Contains(err.Error(), "CloudWatch Logs role") {
		t.Fatalf("logging needs the account role: %v", err)
	}

	if _, err := m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "replace", Path: "/cloudwatchRoleArn", Value: "arn:aws:iam::000000000000:role/cw"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.UpdateStage(ctx(), api, stage, []driver.PatchOperation{{Op: "replace", Path: "/*/*/logging/loglevel", Value: "LOUD"}}); !errors.IsInvalidArgument(err) {
		t.Fatalf("log level enum: %v", err)
	}

	st, err := m.UpdateStage(ctx(), api, stage, append(enable,
		driver.PatchOperation{Op: "replace", Path: "/*/*/metrics/enabled", Value: "true"},
		driver.PatchOperation{Op: "replace", Path: "/~1pets/GET/throttling/rateLimit", Value: "5"},
		driver.PatchOperation{Op: "replace", Path: "/tracingEnabled", Value: "true"},
	))
	if err != nil || st.MethodSettings["*/*"].LoggingLevel != "INFO" || !st.MethodSettings["*/*"].MetricsEnabled ||
		st.MethodSettings["pets/GET"].ThrottlingRateLimit != 5 || !st.TracingEnabled {
		t.Fatalf("settings: %v %+v", err, st)
	}

	badLog := []driver.PatchOperation{
		{Op: "replace", Path: "/accessLogSettings/destinationArn", Value: "not-a-log-group"},
		{Op: "replace", Path: "/accessLogSettings/format", Value: "$context.requestId"},
	}
	if _, err = m.UpdateStage(ctx(), api, stage, badLog); !errors.IsInvalidArgument(err) {
		t.Fatalf("destination must be a log group ARN: %v", err)
	}

	noID := []driver.PatchOperation{
		{Op: "replace", Path: "/accessLogSettings/destinationArn", Value: "arn:aws:logs:us-east-1:000000000000:log-group:access"},
		{Op: "replace", Path: "/accessLogSettings/format", Value: "$context.status"},
	}
	if _, err = m.UpdateStage(ctx(), api, stage, noID); !errors.IsInvalidArgument(err) {
		t.Fatalf("format needs a request id: %v", err)
	}
}

func TestSnapshotRestoreKeepsNewResources(t *testing.T) {
	m := newMock(t)
	api := newAPI(t, m)

	az, _ := m.CreateAuthorizer(ctx(), api.ID, &driver.CreateAuthorizerInput{Name: "a", Type: driver.AuthorizerToken, AuthorizerURI: "u"})
	_, _ = m.CreateModel(ctx(), api.ID, &driver.CreateModelInput{Name: "Pet", Schema: "{}"})
	_, _ = m.PutGatewayResponse(ctx(), api.ID, "THROTTLED", &driver.PutGatewayResponseInput{StatusCode: "503"})
	key, _ := m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", Enabled: true})
	plan, _ := m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{Name: "p"})
	_, _ = m.CreateUsagePlanKey(ctx(), plan.ID, key.ID, "API_KEY")
	_, _ = m.CreateDomainName(ctx(), &driver.CreateDomainNameInput{DomainName: "x.example.com", CertificateARN: "arn"})

	data, err := m.Snapshot(ctx(), false)
	if err != nil {
		t.Fatal(err)
	}

	m2 := newMock(t)
	if err = m2.Restore(ctx(), data); err != nil {
		t.Fatal(err)
	}

	if got, err := m2.GetAuthorizer(ctx(), api.ID, az.ID); err != nil || got.Name != "a" {
		t.Fatalf("authorizer: %v %+v", err, got)
	}

	if _, err = m2.GetModel(ctx(), api.ID, "Pet"); err != nil {
		t.Fatalf("model: %v", err)
	}

	if gr, _ := m2.GetGatewayResponse(ctx(), api.ID, "THROTTLED"); gr.StatusCode != "503" || gr.DefaultResponse {
		t.Fatalf("gateway response override: %+v", gr)
	}

	if keys, _ := m2.GetUsagePlanKeys(ctx(), plan.ID, "", driver.PageInput{}); len(keys.Items) != 1 {
		t.Fatalf("plan keys: %+v", keys)
	}

	if _, err = m2.GetDomainName(ctx(), "x.example.com"); err != nil {
		t.Fatalf("domain: %v", err)
	}
}
