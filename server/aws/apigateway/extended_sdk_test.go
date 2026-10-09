package apigateway_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	agtypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSDK(t *testing.T) (*apigateway.Client, *httptest.Server) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := httptest.NewServer(awsserver.NewFromProvider(cloud))
	t.Cleanup(srv.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatal(err)
	}

	return apigateway.NewFromConfig(cfg, func(o *apigateway.Options) { o.BaseEndpoint = aws.String(srv.URL) }), srv
}

// apiErrorCode extracts the exception type of an SDK error.
func apiErrorCode(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}

	return ""
}

func TestSDKAPIKeysAndUsagePlans(t *testing.T) {
	ctx := context.Background()
	c, _ := newSDK(t)

	key, err := c.CreateApiKey(ctx, &apigateway.CreateApiKeyInput{Name: aws.String("k1"), Enabled: true, Tags: map[string]string{"t": "1"}})
	if err != nil || aws.ToString(key.Value) == "" || !key.Enabled {
		t.Fatalf("CreateApiKey: %v %+v", err, key)
	}

	if _, err = c.CreateApiKey(ctx, &apigateway.CreateApiKeyInput{Name: aws.String("dup"), Value: key.Value}); apiErrorCode(err) != "ConflictException" {
		t.Fatalf("duplicate value: %v", err)
	}

	list, err := c.GetApiKeys(ctx, &apigateway.GetApiKeysInput{NameQuery: aws.String("k")})
	if err != nil || len(list.Items) != 1 || aws.ToString(list.Items[0].Value) != "" {
		t.Fatalf("GetApiKeys must hide values: %v %+v", err, list)
	}

	withValues, _ := c.GetApiKeys(ctx, &apigateway.GetApiKeysInput{IncludeValues: aws.Bool(true)})
	if aws.ToString(withValues.Items[0].Value) != aws.ToString(key.Value) {
		t.Fatalf("includeValues: %+v", withValues)
	}

	upd, err := c.UpdateApiKey(ctx, &apigateway.UpdateApiKeyInput{
		ApiKey: key.Id, PatchOperations: []agtypes.PatchOperation{{Op: agtypes.OpReplace, Path: aws.String("/enabled"), Value: aws.String("false")}},
	})
	if err != nil || upd.Enabled {
		t.Fatalf("UpdateApiKey: %v %+v", err, upd)
	}

	plan, err := c.CreateUsagePlan(ctx, &apigateway.CreateUsagePlanInput{
		Name:     aws.String("gold"),
		Throttle: &agtypes.ThrottleSettings{BurstLimit: 10, RateLimit: 5},
		Quota:    &agtypes.QuotaSettings{Limit: 100, Period: agtypes.QuotaPeriodTypeDay},
	})
	if err != nil || plan.Quota.Limit != 100 {
		t.Fatalf("CreateUsagePlan: %v %+v", err, plan)
	}

	pk, err := c.CreateUsagePlanKey(ctx, &apigateway.CreateUsagePlanKeyInput{UsagePlanId: plan.Id, KeyId: key.Id, KeyType: aws.String("API_KEY")})
	if err != nil || aws.ToString(pk.Name) != "k1" {
		t.Fatalf("CreateUsagePlanKey: %v %+v", err, pk)
	}

	keys, err := c.GetUsagePlanKeys(ctx, &apigateway.GetUsagePlanKeysInput{UsagePlanId: plan.Id})
	if err != nil || len(keys.Items) != 1 {
		t.Fatalf("GetUsagePlanKeys: %v %+v", err, keys)
	}

	plans, err := c.GetUsagePlans(ctx, &apigateway.GetUsagePlansInput{KeyId: key.Id})
	if err != nil || len(plans.Items) != 1 {
		t.Fatalf("GetUsagePlans(keyId): %v %+v", err, plans)
	}

	usage, err := c.GetUsage(ctx, &apigateway.GetUsageInput{UsagePlanId: plan.Id, StartDate: aws.String("2025-01-01"), EndDate: aws.String("2025-01-02")})
	if err != nil || len(usage.Items[aws.ToString(key.Id)]) != 2 {
		t.Fatalf("GetUsage: %v %+v", err, usage)
	}

	if _, err = c.UpdateUsagePlan(ctx, &apigateway.UpdateUsagePlanInput{
		UsagePlanId:     plan.Id,
		PatchOperations: []agtypes.PatchOperation{{Op: agtypes.OpReplace, Path: aws.String("/quota/limit"), Value: aws.String("5")}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = c.DeleteUsagePlanKey(ctx, &apigateway.DeleteUsagePlanKeyInput{UsagePlanId: plan.Id, KeyId: key.Id}); err != nil {
		t.Fatal(err)
	}

	if _, err = c.DeleteUsagePlan(ctx, &apigateway.DeleteUsagePlanInput{UsagePlanId: plan.Id}); err != nil {
		t.Fatal(err)
	}

	if _, err = c.DeleteApiKey(ctx, &apigateway.DeleteApiKeyInput{ApiKey: key.Id}); err != nil {
		t.Fatal(err)
	}

	if _, err = c.GetApiKey(ctx, &apigateway.GetApiKeyInput{ApiKey: key.Id}); apiErrorCode(err) != "NotFoundException" {
		t.Fatalf("after delete: %v", err)
	}
}

func TestSDKAuthorizersModelsValidatorsAndGatewayResponses(t *testing.T) {
	ctx := context.Background()
	c, _ := newSDK(t)

	api, err := c.CreateRestApi(ctx, &apigateway.CreateRestApiInput{Name: aws.String("ext")})
	if err != nil {
		t.Fatal(err)
	}

	az, err := c.CreateAuthorizer(ctx, &apigateway.CreateAuthorizerInput{
		RestApiId: api.Id, Name: aws.String("tok"), Type: agtypes.AuthorizerTypeToken,
		AuthorizerUri: aws.String("arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:auth/invocations"),
	})
	if err != nil || aws.ToString(az.IdentitySource) != "method.request.header.Authorization" || aws.ToInt32(az.AuthorizerResultTtlInSeconds) != 300 {
		t.Fatalf("CreateAuthorizer: %v %+v", err, az)
	}

	if _, err = c.CreateAuthorizer(ctx, &apigateway.CreateAuthorizerInput{RestApiId: api.Id, Name: aws.String("bad"), Type: agtypes.AuthorizerTypeCognitoUserPools}); apiErrorCode(err) != "BadRequestException" {
		t.Fatalf("cognito without providers: %v", err)
	}

	azs, err := c.GetAuthorizers(ctx, &apigateway.GetAuthorizersInput{RestApiId: api.Id})
	if err != nil || len(azs.Items) != 1 {
		t.Fatalf("GetAuthorizers: %v %+v", err, azs)
	}

	if _, err = c.UpdateAuthorizer(ctx, &apigateway.UpdateAuthorizerInput{
		RestApiId: api.Id, AuthorizerId: az.Id,
		PatchOperations: []agtypes.PatchOperation{{Op: agtypes.OpReplace, Path: aws.String("/name"), Value: aws.String("renamed")}},
	}); err != nil {
		t.Fatal(err)
	}

	models, err := c.GetModels(ctx, &apigateway.GetModelsInput{RestApiId: api.Id})
	if err != nil || len(models.Items) != 2 {
		t.Fatalf("default models: %v %+v", err, models)
	}

	if _, err = c.CreateModel(ctx, &apigateway.CreateModelInput{RestApiId: api.Id, Name: aws.String("Pet"), ContentType: aws.String("application/json"), Schema: aws.String(`{"type":"object"}`)}); err != nil {
		t.Fatal(err)
	}

	if _, err = c.CreateModel(ctx, &apigateway.CreateModelInput{RestApiId: api.Id, Name: aws.String("Pet"), ContentType: aws.String("application/json")}); apiErrorCode(err) != "ConflictException" {
		t.Fatalf("duplicate model: %v", err)
	}

	v, err := c.CreateRequestValidator(ctx, &apigateway.CreateRequestValidatorInput{RestApiId: api.Id, Name: aws.String("v"), ValidateRequestBody: true})
	if err != nil || !v.ValidateRequestBody {
		t.Fatalf("CreateRequestValidator: %v %+v", err, v)
	}

	root, _ := c.GetResources(ctx, &apigateway.GetResourcesInput{RestApiId: api.Id})
	if _, err = c.PutMethod(ctx, &apigateway.PutMethodInput{
		RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), AuthorizationType: aws.String("CUSTOM"),
		AuthorizerId: az.Id, RequestValidatorId: v.Id, RequestModels: map[string]string{"application/json": "Pet"},
	}); err != nil {
		t.Fatalf("PutMethod with refs: %v", err)
	}

	got, _ := c.GetMethod(ctx, &apigateway.GetMethodInput{RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET")})
	if aws.ToString(got.AuthorizerId) != aws.ToString(az.Id) || aws.ToString(got.RequestValidatorId) != aws.ToString(v.Id) {
		t.Fatalf("method refs round-trip: %+v", got)
	}

	gr, err := c.PutGatewayResponse(ctx, &apigateway.PutGatewayResponseInput{
		RestApiId: api.Id, ResponseType: agtypes.GatewayResponseTypeThrottled, StatusCode: aws.String("503"),
		ResponseTemplates: map[string]string{"application/json": `{"m":$context.error.messageString}`},
	})
	if err != nil || aws.ToString(gr.StatusCode) != "503" || gr.DefaultResponse {
		t.Fatalf("PutGatewayResponse: %v %+v", err, gr)
	}

	all, err := c.GetGatewayResponses(ctx, &apigateway.GetGatewayResponsesInput{RestApiId: api.Id, Limit: aws.Int32(100)})
	if err != nil || len(all.Items) != 21 {
		t.Fatalf("GetGatewayResponses: %v %d", err, len(all.Items))
	}

	if _, err = c.DeleteGatewayResponse(ctx, &apigateway.DeleteGatewayResponseInput{RestApiId: api.Id, ResponseType: agtypes.GatewayResponseTypeThrottled}); err != nil {
		t.Fatal(err)
	}
}

func TestSDKDomainNamesBasePathMappingsAndVpcLinks(t *testing.T) {
	ctx := context.Background()
	c, _ := newSDK(t)

	api, _ := c.CreateRestApi(ctx, &apigateway.CreateRestApiInput{Name: aws.String("d")})
	root, _ := c.GetResources(ctx, &apigateway.GetResourcesInput{RestApiId: api.Id})
	_, _ = c.PutMethod(ctx, &apigateway.PutMethodInput{RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), AuthorizationType: aws.String("NONE")})
	_, _ = c.PutIntegration(ctx, &apigateway.PutIntegrationInput{RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), Type: agtypes.IntegrationTypeMock})
	_, err := c.CreateDeployment(ctx, &apigateway.CreateDeploymentInput{RestApiId: api.Id, StageName: aws.String("prod")})

	if err != nil {
		t.Fatal(err)
	}

	dn, err := c.CreateDomainName(ctx, &apigateway.CreateDomainNameInput{
		DomainName: aws.String("api.example.com"), RegionalCertificateArn: aws.String("arn:aws:acm:us-east-1:000000000000:certificate/x"),
		EndpointConfiguration: &agtypes.EndpointConfiguration{Types: []agtypes.EndpointType{agtypes.EndpointTypeRegional}},
	})
	if err != nil || aws.ToString(dn.RegionalDomainName) == "" {
		t.Fatalf("CreateDomainName: %v %+v", err, dn)
	}

	bpm, err := c.CreateBasePathMapping(ctx, &apigateway.CreateBasePathMappingInput{DomainName: dn.DomainName, RestApiId: api.Id, Stage: aws.String("prod"), BasePath: aws.String("v1")})
	if err != nil || aws.ToString(bpm.BasePath) != "v1" {
		t.Fatalf("CreateBasePathMapping: %v %+v", err, bpm)
	}

	root2, err := c.CreateBasePathMapping(ctx, &apigateway.CreateBasePathMappingInput{DomainName: dn.DomainName, RestApiId: api.Id, Stage: aws.String("prod")})
	if err != nil || aws.ToString(root2.BasePath) != "(none)" {
		t.Fatalf("root mapping: %v %+v", err, root2)
	}

	got, err := c.GetBasePathMapping(ctx, &apigateway.GetBasePathMappingInput{DomainName: dn.DomainName, BasePath: aws.String("(none)")})
	if err != nil || aws.ToString(got.RestApiId) != aws.ToString(api.Id) {
		t.Fatalf("GetBasePathMapping((none)): %v %+v", err, got)
	}

	list, err := c.GetBasePathMappings(ctx, &apigateway.GetBasePathMappingsInput{DomainName: dn.DomainName})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("GetBasePathMappings: %v %+v", err, list)
	}

	if _, err = c.DeleteBasePathMapping(ctx, &apigateway.DeleteBasePathMappingInput{DomainName: dn.DomainName, BasePath: aws.String("v1")}); err != nil {
		t.Fatal(err)
	}

	link, err := c.CreateVpcLink(ctx, &apigateway.CreateVpcLinkInput{
		Name: aws.String("vl"), TargetArns: []string{"arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/net/n/abc123"},
	})
	if err != nil || link.Status != agtypes.VpcLinkStatusAvailable {
		t.Fatalf("CreateVpcLink: %v %+v", err, link)
	}

	if _, err = c.CreateVpcLink(ctx, &apigateway.CreateVpcLinkInput{Name: aws.String("bad"), TargetArns: []string{"nope"}}); apiErrorCode(err) != "BadRequestException" {
		t.Fatalf("bad target: %v", err)
	}

	links, err := c.GetVpcLinks(ctx, &apigateway.GetVpcLinksInput{})
	if err != nil || len(links.Items) != 1 {
		t.Fatalf("GetVpcLinks: %v %+v", err, links)
	}

	if _, err = c.DeleteVpcLink(ctx, &apigateway.DeleteVpcLinkInput{VpcLinkId: link.Id}); err != nil {
		t.Fatal(err)
	}

	if _, err = c.DeleteDomainName(ctx, &apigateway.DeleteDomainNameInput{DomainName: dn.DomainName}); err != nil {
		t.Fatal(err)
	}
}

func TestSDKTagsOnStagesKeysAndPlans(t *testing.T) {
	ctx := context.Background()
	c, _ := newSDK(t)

	key, _ := c.CreateApiKey(ctx, &apigateway.CreateApiKeyInput{Name: aws.String("k")})
	arn := "arn:aws:apigateway:us-east-1::/apikeys/" + aws.ToString(key.Id)

	if _, err := c.TagResource(ctx, &apigateway.TagResourceInput{ResourceArn: aws.String(arn), Tags: map[string]string{"a": "b"}}); err != nil {
		t.Fatal(err)
	}

	tags, err := c.GetTags(ctx, &apigateway.GetTagsInput{ResourceArn: aws.String(arn)})
	if err != nil || tags.Tags["a"] != "b" {
		t.Fatalf("GetTags: %v %+v", err, tags)
	}

	if _, err = c.UntagResource(ctx, &apigateway.UntagResourceInput{ResourceArn: aws.String(arn), TagKeys: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
}

func TestSDKStageSettingsAndTags(t *testing.T) {
	ctx := context.Background()
	c, _ := newSDK(t)

	api, _ := c.CreateRestApi(ctx, &apigateway.CreateRestApiInput{Name: aws.String("s")})
	root, _ := c.GetResources(ctx, &apigateway.GetResourcesInput{RestApiId: api.Id})
	_, _ = c.PutMethod(ctx, &apigateway.PutMethodInput{RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), AuthorizationType: aws.String("NONE")})
	_, _ = c.PutIntegration(ctx, &apigateway.PutIntegrationInput{RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), Type: agtypes.IntegrationTypeMock})
	dep, _ := c.CreateDeployment(ctx, &apigateway.CreateDeploymentInput{RestApiId: api.Id})

	st, err := c.CreateStage(ctx, &apigateway.CreateStageInput{
		RestApiId: api.Id, StageName: aws.String("dev"), DeploymentId: dep.Id, Tags: map[string]string{"env": "dev"}, TracingEnabled: true,
	})
	if err != nil || st.Tags["env"] != "dev" || !st.TracingEnabled {
		t.Fatalf("CreateStage: %v %+v", err, st)
	}

	_, err = c.UpdateStage(ctx, &apigateway.UpdateStageInput{RestApiId: api.Id, StageName: aws.String("dev"), PatchOperations: []agtypes.PatchOperation{
		{Op: agtypes.OpReplace, Path: aws.String("/*/*/logging/loglevel"), Value: aws.String("INFO")},
	}})
	if apiErrorCode(err) != "BadRequestException" || !strings.Contains(err.Error(), "CloudWatch Logs role ARN") {
		t.Fatalf("logging without the account role: %v", err)
	}

	if _, err = c.UpdateAccount(ctx, &apigateway.UpdateAccountInput{PatchOperations: []agtypes.PatchOperation{
		{Op: agtypes.OpReplace, Path: aws.String("/cloudwatchRoleArn"), Value: aws.String("arn:aws:iam::000000000000:role/cw")},
	}}); err != nil {
		t.Fatal(err)
	}

	upd, err := c.UpdateStage(ctx, &apigateway.UpdateStageInput{RestApiId: api.Id, StageName: aws.String("dev"), PatchOperations: []agtypes.PatchOperation{
		{Op: agtypes.OpReplace, Path: aws.String("/*/*/logging/loglevel"), Value: aws.String("INFO")},
		{Op: agtypes.OpReplace, Path: aws.String("/*/*/metrics/enabled"), Value: aws.String("true")},
		{Op: agtypes.OpReplace, Path: aws.String("/~1pets/GET/throttling/rateLimit"), Value: aws.String("7")},
	}})
	if err != nil || aws.ToString(upd.MethodSettings["*/*"].LoggingLevel) != "INFO" ||
		upd.MethodSettings["pets/GET"].ThrottlingRateLimit != 7 {
		t.Fatalf("method settings: %v %+v", err, upd.MethodSettings)
	}

	arn := "arn:aws:apigateway:us-east-1::/restapis/" + aws.ToString(api.Id) + "/stages/dev"
	if _, err = c.TagResource(ctx, &apigateway.TagResourceInput{ResourceArn: aws.String(arn), Tags: map[string]string{"x": "y"}}); err != nil {
		t.Fatal(err)
	}

	got, _ := c.GetStage(ctx, &apigateway.GetStageInput{RestApiId: api.Id, StageName: aws.String("dev")})
	if got.Tags["x"] != "y" || got.Tags["env"] != "dev" {
		t.Fatalf("stage tags: %+v", got.Tags)
	}
}

func TestSDKOpenAPIImportPutExport(t *testing.T) {
	ctx := context.Background()
	c, _ := newSDK(t)

	const def = `{"swagger":"2.0","info":{"title":"imp","version":"1"},"paths":{"/hi":{"get":{"responses":{"200":{"description":"ok"}},
	"x-amazon-apigateway-integration":{"type":"mock","requestTemplates":{"application/json":"{\"statusCode\":200}"},
	"responses":{"default":{"statusCode":"200","responseTemplates":{"application/json":"{\"hello\":true}"}}}}}}}}`

	api, err := c.ImportRestApi(ctx, &apigateway.ImportRestApiInput{Body: []byte(def)})
	if err != nil || aws.ToString(api.Name) != "imp" {
		t.Fatalf("ImportRestApi: %v %+v", err, api)
	}

	if _, err = c.ImportRestApi(ctx, &apigateway.ImportRestApiInput{Body: []byte("not a definition")}); apiErrorCode(err) != "BadRequestException" {
		t.Fatalf("bad definition: %v", err)
	}

	if _, err = c.CreateDeployment(ctx, &apigateway.CreateDeploymentInput{RestApiId: api.Id, StageName: aws.String("prod")}); err != nil {
		t.Fatal(err)
	}

	exp, err := c.GetExport(ctx, &apigateway.GetExportInput{RestApiId: api.Id, StageName: aws.String("prod"), ExportType: aws.String("oas30"), Accepts: aws.String("application/json")})
	if err != nil || !strings.Contains(string(exp.Body), `"openapi": "3.0.1"`) || aws.ToString(exp.ContentType) != "application/json" {
		t.Fatalf("GetExport: %v %s", err, exp.Body)
	}

	put, err := c.PutRestApi(ctx, &apigateway.PutRestApiInput{RestApiId: api.Id, Mode: agtypes.PutModeOverwrite, Body: []byte(strings.Replace(def, "/hi", "/yo", 1))})
	if err != nil || aws.ToString(put.Id) != aws.ToString(api.Id) {
		t.Fatalf("PutRestApi: %v %+v", err, put)
	}

	res, _ := c.GetResources(ctx, &apigateway.GetResourcesInput{RestApiId: api.Id})
	paths := map[string]bool{}

	for _, r := range res.Items {
		paths[aws.ToString(r.Path)] = true
	}

	if paths["/hi"] || !paths["/yo"] {
		t.Fatalf("overwrite: %v", paths)
	}
}

func TestSDKTestInvokeMethodAndPathParams(t *testing.T) {
	ctx := context.Background()
	c, _ := newSDK(t)

	api, _ := c.CreateRestApi(ctx, &apigateway.CreateRestApiInput{Name: aws.String("ti")})
	root, _ := c.GetResources(ctx, &apigateway.GetResourcesInput{RestApiId: api.Id})
	_, _ = c.PutMethod(ctx, &apigateway.PutMethodInput{RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), AuthorizationType: aws.String("NONE")})
	_, _ = c.PutMethodResponse(ctx, &apigateway.PutMethodResponseInput{RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), StatusCode: aws.String("200")})
	_, _ = c.PutIntegration(ctx, &apigateway.PutIntegrationInput{
		RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), Type: agtypes.IntegrationTypeMock,
		RequestTemplates: map[string]string{"application/json": `{"statusCode":200}`},
	})
	_, _ = c.PutIntegrationResponse(ctx, &apigateway.PutIntegrationResponseInput{
		RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET"), StatusCode: aws.String("200"),
		ResponseTemplates: map[string]string{"application/json": `{"ok":true}`},
	})

	out, err := c.TestInvokeMethod(ctx, &apigateway.TestInvokeMethodInput{RestApiId: api.Id, ResourceId: root.Items[0].Id, HttpMethod: aws.String("GET")})
	if err != nil || out.Status != 200 || out.Body == nil || *out.Body != `{"ok":true}` {
		t.Fatalf("TestInvokeMethod: %v %+v", err, out)
	}
}

// TestExecuteAPIFormPostReachesTheIntegration drives a form-encoded POST through
// the execute-api path form: it must reach the API, not an AWS query handler.
func TestExecuteAPIFormPostReachesTheIntegration(t *testing.T) {
	ctx := context.Background()
	c, srv := newSDK(t)

	api, _ := c.CreateRestApi(ctx, &apigateway.CreateRestApiInput{Name: aws.String("form")})
	root, _ := c.GetResources(ctx, &apigateway.GetResourcesInput{RestApiId: api.Id})
	rid := root.Items[0].Id
	_, _ = c.PutMethod(ctx, &apigateway.PutMethodInput{RestApiId: api.Id, ResourceId: rid, HttpMethod: aws.String("POST"), AuthorizationType: aws.String("NONE")})
	_, _ = c.PutMethodResponse(ctx, &apigateway.PutMethodResponseInput{RestApiId: api.Id, ResourceId: rid, HttpMethod: aws.String("POST"), StatusCode: aws.String("200")})
	_, _ = c.PutIntegration(ctx, &apigateway.PutIntegrationInput{
		RestApiId: api.Id, ResourceId: rid, HttpMethod: aws.String("POST"), Type: agtypes.IntegrationTypeMock,
		RequestTemplates: map[string]string{"application/x-www-form-urlencoded": `{"statusCode":200}`},
	})
	_, _ = c.PutIntegrationResponse(ctx, &apigateway.PutIntegrationResponseInput{
		RestApiId: api.Id, ResourceId: rid, HttpMethod: aws.String("POST"), StatusCode: aws.String("200"),
		ResponseTemplates: map[string]string{"application/json": `{"form":"reached"}`},
	})

	if _, err := c.CreateDeployment(ctx, &apigateway.CreateDeploymentInput{RestApiId: api.Id, StageName: aws.String("prod")}); err != nil {
		t.Fatal(err)
	}

	for name, mk := range map[string]func() *http.Request{
		"path form": func() *http.Request {
			r, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/restapis/"+aws.ToString(api.Id)+"/prod/_user_request_/", strings.NewReader("a=1&Action=Foo"))

			return r
		},
		"host form": func() *http.Request {
			r, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/prod/", strings.NewReader("a=1&Action=Foo"))
			r.Host = aws.ToString(api.Id) + ".execute-api.us-east-1.amazonaws.com"

			return r
		},
	} {
		req := mk()
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != 200 || !strings.Contains(string(body), "reached") {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
	}
}

// TestCustomDomainServesTheMappedStage invokes an API by its custom domain name:
// the Host header selects the domain, the first path segment the base path.
func TestCustomDomainServesTheMappedStage(t *testing.T) {
	ctx := context.Background()
	c, srv := newSDK(t)

	api, _ := c.CreateRestApi(ctx, &apigateway.CreateRestApiInput{Name: aws.String("cd")})
	root, _ := c.GetResources(ctx, &apigateway.GetResourcesInput{RestApiId: api.Id})
	rid := root.Items[0].Id
	pets, _ := c.CreateResource(ctx, &apigateway.CreateResourceInput{RestApiId: api.Id, ParentId: rid, PathPart: aws.String("pets")})
	_, _ = c.PutMethod(ctx, &apigateway.PutMethodInput{RestApiId: api.Id, ResourceId: pets.Id, HttpMethod: aws.String("GET"), AuthorizationType: aws.String("NONE")})
	_, _ = c.PutMethodResponse(ctx, &apigateway.PutMethodResponseInput{RestApiId: api.Id, ResourceId: pets.Id, HttpMethod: aws.String("GET"), StatusCode: aws.String("200")})
	_, _ = c.PutIntegration(ctx, &apigateway.PutIntegrationInput{
		RestApiId: api.Id, ResourceId: pets.Id, HttpMethod: aws.String("GET"), Type: agtypes.IntegrationTypeMock,
		RequestTemplates: map[string]string{"application/json": `{"statusCode":200}`},
	})
	_, _ = c.PutIntegrationResponse(ctx, &apigateway.PutIntegrationResponseInput{
		RestApiId: api.Id, ResourceId: pets.Id, HttpMethod: aws.String("GET"), StatusCode: aws.String("200"),
		ResponseTemplates: map[string]string{"application/json": `{"via":"domain"}`},
	})

	if _, err := c.CreateDeployment(ctx, &apigateway.CreateDeploymentInput{RestApiId: api.Id, StageName: aws.String("prod")}); err != nil {
		t.Fatal(err)
	}

	_, err := c.CreateDomainName(ctx, &apigateway.CreateDomainNameInput{
		DomainName: aws.String("api.example.com"), CertificateArn: aws.String("arn:aws:acm:us-east-1:000000000000:certificate/x"),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, bp := range []string{"v1", ""} {
		in := &apigateway.CreateBasePathMappingInput{DomainName: aws.String("api.example.com"), RestApiId: api.Id, Stage: aws.String("prod")}
		if bp != "" {
			in.BasePath = aws.String(bp)
		}

		if _, err = c.CreateBasePathMapping(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	for path, want := range map[string]int{"/v1/pets": 200, "/pets": 200, "/v1/nope": 403} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
		req.Host = "api.example.com"

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != want || (want == 200 && !strings.Contains(string(body), "domain")) {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, body)
		}
	}
}
