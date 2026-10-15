package apigateway_test

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const swaggerDoc = `
swagger: "2.0"
info: {title: Pets, version: "1.0", description: pet store}
x-amazon-apigateway-binary-media-types: [image/png]
x-amazon-apigateway-request-validators:
  all: {validateRequestBody: true, validateRequestParameters: true}
securityDefinitions:
  api_key: {type: apiKey, name: x-api-key, in: header}
  tok:
    type: apiKey
    name: Authorization
    in: header
    x-amazon-apigateway-authtype: custom
    x-amazon-apigateway-authorizer:
      type: token
      authorizerUri: arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:auth/invocations
      authorizerResultTtlInSeconds: 120
definitions:
  Pet: {type: object, required: [name], properties: {name: {type: string}}}
x-amazon-apigateway-gateway-responses:
  DEFAULT_4XX:
    responseParameters: {gatewayresponse.header.Access-Control-Allow-Origin: "'*'"}
paths:
  /pets:
    get:
      operationId: listPets
      parameters:
        - {name: page, in: query, required: true, type: string}
      security: [{tok: []}]
      x-amazon-apigateway-request-validator: all
      responses: {"200": {description: ok}}
      x-amazon-apigateway-integration:
        type: aws_proxy
        httpMethod: POST
        uri: arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:hello/invocations
    post:
      security: [{api_key: []}]
      responses: {"201": {description: created}}
      x-amazon-apigateway-integration:
        type: http
        httpMethod: POST
        uri: http://example.com/pets
        passthroughBehavior: when_no_match
        responses:
          default: {statusCode: "201"}
  /pets/{petId}:
    x-amazon-apigateway-any-method:
      parameters: [{name: petId, in: path, required: true, type: string}]
      responses: {"200": {description: ok}}
      x-amazon-apigateway-integration:
        type: http_proxy
        httpMethod: ANY
        uri: http://example.com/pets/{petId}
        requestParameters: {integration.request.path.petId: method.request.path.petId}
`

func TestImportRestAPIBuildsTheWholeDefinition(t *testing.T) {
	m := newMock(t)

	res, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(swaggerDoc)})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	api := res.API
	if api.Name != "Pets" || api.Version != "1.0" || len(api.BinaryMediaTypes) != 1 {
		t.Fatalf("api: %+v", api)
	}

	resources, _ := m.GetResources(ctx(), api.ID)
	byPath := map[string]driver.Resource{}

	for _, r := range resources {
		byPath[r.Path] = r
	}

	pets := byPath["/pets"]
	get := pets.Methods["GET"]

	if get == nil || get.OperationName != "listPets" || get.AuthorizationType != "CUSTOM" || get.AuthorizerID == "" ||
		get.RequestValidatorID == "" || !get.RequestParameters["method.request.querystring.page"] ||
		get.Integration == nil || get.Integration.Type != driver.IntegrationAWSProxy {
		t.Fatalf("GET /pets: %+v", get)
	}

	post := pets.Methods["POST"]
	if post == nil || !post.APIKeyRequired || post.Integration.Type != driver.IntegrationHTTP ||
		post.Integration.IntegrationResponses["201"] == nil || post.MethodResponses["201"] == nil {
		t.Fatalf("POST /pets: %+v", post)
	}

	any := byPath["/pets/{petId}"].Methods["ANY"]
	if any == nil || any.Integration.Type != driver.IntegrationHTTPProxy {
		t.Fatalf("ANY /pets/{petId}: %+v", any)
	}

	if _, err = m.GetModel(ctx(), api.ID, "Pet"); err != nil {
		t.Fatalf("definitions become models: %v", err)
	}

	az, _ := m.GetAuthorizer(ctx(), api.ID, get.AuthorizerID)
	if az.Name != "tok" || az.Type != driver.AuthorizerToken || *az.AuthorizerResultTTLInSeconds != 120 || az.IdentitySource != "method.request.header.Authorization" {
		t.Fatalf("authorizer: %+v", az)
	}

	if gr, _ := m.GetGatewayResponse(ctx(), api.ID, "DEFAULT_4XX"); gr.DefaultResponse {
		t.Fatalf("gateway response override: %+v", gr)
	}

	// The imported API deploys.
	if _, err = m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{StageName: "prod"}); err != nil {
		t.Fatalf("deploy: %v", err)
	}
}

func TestImportRejectsBadDefinitions(t *testing.T) {
	m := newMock(t)

	for name, body := range map[string]string{
		"empty": "", "not a document": "just text: [", "no version": "info: {title: x}", "no title": "swagger: '2.0'\ninfo: {version: '1'}",
		"bad integration": "swagger: '2.0'\ninfo: {title: x}\npaths:\n  /a:\n    get:\n      x-amazon-apigateway-integration: {type: bogus, httpMethod: GET, uri: http://x}",
	} {
		if _, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(body)}); !errors.IsInvalidArgument(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	// A failed import leaves no half-built API behind.
	if apis, _ := m.GetRestAPIs(ctx()); len(apis) != 0 {
		t.Fatalf("failed imports must not leave APIs: %+v", apis)
	}

	warn := "swagger: '2.0'\ninfo: {title: w}\npaths:\n  /a:\n    get:\n      responses: {'200': {description: ok}}"

	res, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(warn)})
	if err != nil || len(res.Warnings) == 0 {
		t.Fatalf("a method with no integration is a warning: %v %+v", err, res)
	}

	if _, err = m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(warn), FailOnWarnings: true}); !errors.IsInvalidArgument(err) {
		t.Fatalf("failOnWarnings: %v", err)
	}
}

func TestPutRestAPIMergeAndOverwrite(t *testing.T) {
	m := newMock(t)

	res, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(swaggerDoc)})
	if err != nil {
		t.Fatal(err)
	}

	id := res.API.ID

	extra := `
swagger: "2.0"
info: {title: Pets, version: "2"}
paths:
  /owners:
    get:
      responses: {"200": {description: ok}}
      x-amazon-apigateway-integration: {type: mock, requestTemplates: {application/json: '{"statusCode":200}'}}
`

	if _, err = m.PutRestAPI(ctx(), id, &driver.PutRestAPIInput{Mode: "bogus", Body: []byte(extra)}); !errors.IsInvalidArgument(err) {
		t.Fatalf("mode: %v", err)
	}

	if _, err = m.PutRestAPI(ctx(), id, &driver.PutRestAPIInput{Mode: driver.ImportMerge, Body: []byte(extra)}); err != nil {
		t.Fatal(err)
	}

	paths := func() map[string]bool {
		rs, _ := m.GetResources(ctx(), id)
		out := map[string]bool{}

		for _, r := range rs {
			out[r.Path] = true
		}

		return out
	}

	if p := paths(); !p["/pets"] || !p["/owners"] {
		t.Fatalf("merge keeps existing and adds: %v", p)
	}

	if _, err = m.PutRestAPI(ctx(), id, &driver.PutRestAPIInput{Mode: driver.ImportOverwrite, Body: []byte(extra)}); err != nil {
		t.Fatal(err)
	}

	if p := paths(); p["/pets"] || !p["/owners"] {
		t.Fatalf("overwrite replaces: %v", p)
	}

	if _, err = m.PutRestAPI(ctx(), "missing", &driver.PutRestAPIInput{Body: []byte(extra)}); !errors.IsNotFound(err) {
		t.Fatalf("unknown api: %v", err)
	}
}

func TestGetExportSwaggerOAS30AndYAMLRoundTrip(t *testing.T) {
	m := newMock(t)

	res, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(swaggerDoc)})
	if err != nil {
		t.Fatal(err)
	}

	id := res.API.ID

	if _, err = m.CreateDeployment(ctx(), id, driver.CreateDeploymentInput{StageName: "prod"}); err != nil {
		t.Fatal(err)
	}

	if _, err = m.GetExport(ctx(), &driver.GetExportInput{RestAPIID: id, StageName: "prod", ExportType: "raml"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("export type: %v", err)
	}

	if _, err = m.GetExport(ctx(), &driver.GetExportInput{RestAPIID: id, StageName: "nope", ExportType: "swagger"}); !errors.IsNotFound(err) {
		t.Fatalf("stage: %v", err)
	}

	exp, err := m.GetExport(ctx(), &driver.GetExportInput{
		RestAPIID: id, StageName: "prod", ExportType: driver.ExportSwagger, Extensions: []string{"apigateway"},
	})
	if err != nil || exp.ContentType != "application/json" {
		t.Fatalf("swagger: %v %+v", err, exp)
	}

	var doc map[string]any
	if err = json.Unmarshal(exp.Body, &doc); err != nil || doc["swagger"] != "2.0" || doc["basePath"] != "/prod" {
		t.Fatalf("swagger doc: %v %s", err, exp.Body)
	}

	if !strings.Contains(string(exp.Body), "x-amazon-apigateway-integration") || !strings.Contains(string(exp.Body), "x-amazon-apigateway-authorizer") {
		t.Fatalf("extensions missing: %s", exp.Body)
	}

	plain, _ := m.GetExport(ctx(), &driver.GetExportInput{RestAPIID: id, StageName: "prod", ExportType: driver.ExportSwagger})
	if strings.Contains(string(plain.Body), "x-amazon-apigateway-integration") {
		t.Fatal("no extensions requested: no integration extension")
	}

	oas, err := m.GetExport(ctx(), &driver.GetExportInput{
		RestAPIID: id, StageName: "prod", ExportType: driver.ExportOAS30, Accept: "application/yaml", Extensions: []string{"integrations", "authorizers"},
	})
	if err != nil || oas.ContentType != "application/yaml" {
		t.Fatalf("oas30: %v %+v", err, oas)
	}

	var ydoc map[string]any
	if err = yaml.Unmarshal(oas.Body, &ydoc); err != nil || ydoc["openapi"] != "3.0.1" {
		t.Fatalf("oas30 yaml: %v %s", err, oas.Body)
	}

	// An exported definition imports again with the same shape.
	again, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: oas.Body})
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}

	rs, _ := m.GetResources(ctx(), again.API.ID)

	methods := 0

	for _, r := range rs {
		methods += len(r.Methods)
	}

	if methods != 3 {
		t.Fatalf("re-imported methods = %d, want 3", methods)
	}
}

func TestImportRecognisesAnyXAPIKeyScheme(t *testing.T) {
	m := newMock(t)

	res, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(`{
"openapi":"3.0.1","info":{"title":"k","version":"1"},
"components":{"securitySchemes":{"ApiKeyAuth":{"type":"apiKey","name":"x-api-key","in":"header"}}},
"paths":{"/k":{"get":{"security":[{"ApiKeyAuth":[]}],"responses":{"200":{"description":"ok"}}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}

	resources, _ := m.GetResources(ctx(), res.API.ID)
	for i := range resources {
		if resources[i].Path == "/k" && !resources[i].Methods["GET"].APIKeyRequired {
			t.Fatal("a scheme of type apiKey on x-api-key must require the key whatever its name")
		}
	}
}

func TestExportRoundTripsSecuredMethods(t *testing.T) {
	m := newMock(t)

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "sec"})
	if err != nil {
		t.Fatal(err)
	}

	res, _ := m.CreateResource(ctx(), api.ID, api.RootResourceID, "r")
	az, err := m.CreateAuthorizer(ctx(), api.ID, &driver.CreateAuthorizerInput{
		Name: "reqauth", Type: driver.AuthorizerRequest, AuthorizerURI: lambdaURI, IdentitySource: "method.request.header.X-Tenant",
		AuthorizerResultTTLInSeconds: intPtr(30),
	})
	if err != nil {
		t.Fatal(err)
	}

	val, err := m.CreateRequestValidator(ctx(), api.ID, &driver.CreateRequestValidatorInput{Name: "all", ValidateRequestBody: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = m.PutMethod(ctx(), api.ID, res.ID, "GET", driver.PutMethodInput{
		AuthorizationType: "CUSTOM", AuthorizerID: az.ID, RequestValidatorID: val.ID, APIKeyRequired: true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = m.PutIntegration(ctx(), api.ID, res.ID, "GET", driver.PutIntegrationInput{
		Type: driver.IntegrationHTTP, IntegrationHTTPMethod: "GET", URI: "https://example.com",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = m.CreateDeployment(ctx(), api.ID, driver.CreateDeploymentInput{StageName: "prod"}); err != nil {
		t.Fatal(err)
	}

	exp, err := m.GetExport(ctx(), &driver.GetExportInput{
		RestAPIID: api.ID, StageName: "prod", ExportType: "swagger", Extensions: []string{"apigateway"},
	})
	if err != nil {
		t.Fatal(err)
	}

	imp, err := m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: exp.Body})
	if err != nil {
		t.Fatalf("re-import: %v\n%s", err, exp.Body)
	}

	resources, _ := m.GetResources(ctx(), imp.API.ID)
	for i := range resources {
		if resources[i].Path != "/r" {
			continue
		}

		got := resources[i].Methods["GET"]
		if got.AuthorizationType != "CUSTOM" || got.AuthorizerID == "" || got.RequestValidatorID == "" || !got.APIKeyRequired {
			t.Fatalf("the secured method must survive an export and import: %+v\n%s", got, exp.Body)
		}
	}

	azs, _ := m.GetAuthorizers(ctx(), imp.API.ID, driver.PageInput{})
	if len(azs.Items) != 1 || azs.Items[0].IdentitySource != "method.request.header.X-Tenant" {
		t.Fatalf("the REQUEST authorizer keeps its identitySource: %+v", azs)
	}
}

// roundTrip exports the fixture's deployed API and imports the document into a new API.
func (f *dpFixture) roundTrip(t *testing.T, exportType string) (body, apiID string) {
	t.Helper()

	exp, err := f.m.GetExport(ctx(), &driver.GetExportInput{
		RestAPIID: f.api.ID, StageName: "prod", ExportType: exportType, Extensions: []string{"apigateway"},
	})
	if err != nil {
		t.Fatal(err)
	}

	imp, err := f.m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: exp.Body})
	if err != nil {
		t.Fatalf("re-import: %v\n%s", err, exp.Body)
	}

	return string(exp.Body), imp.API.ID
}

func (f *dpFixture) importedGet(t *testing.T, apiID string) *driver.Method {
	t.Helper()

	resources, err := f.m.GetResources(ctx(), apiID)
	if err != nil {
		t.Fatal(err)
	}

	for i := range resources {
		if resources[i].Path == "/items" {
			return resources[i].Methods["GET"]
		}
	}

	t.Fatal("the imported API has no /items")

	return nil
}

func TestExportRoundTripsAWSIAMMethods(t *testing.T) {
	for _, exportType := range []string{"swagger", "oas30"} {
		t.Run(exportType, func(t *testing.T) {
			f := newDP(t)
			f.method(t, driver.PutMethodInput{AuthorizationType: "AWS_IAM", APIKeyRequired: true}, nil)

			body, apiID := f.roundTrip(t, exportType)

			got := f.importedGet(t, apiID)
			if got.AuthorizationType != "AWS_IAM" || !got.APIKeyRequired {
				t.Fatalf("AWS_IAM and the API key must survive an export and import: %+v\n%s", got, body)
			}

			if !strings.Contains(body, "awsSigv4") {
				t.Fatalf("the sigv4 scheme must be declared: %s", body)
			}
		})
	}
}

func TestImportRecognisesTheDocumentedSigv4Scheme(t *testing.T) {
	body := `{"swagger":"2.0","info":{"title":"iam","version":"1"},
"securityDefinitions":{"sigv4":{"type":"apiKey","name":"Authorization","in":"header","x-amazon-apigateway-authtype":"awsSigv4"}},
"paths":{"/items":{"get":{"security":[{"sigv4":[]}],"responses":{"200":{"description":"ok"}},
"x-amazon-apigateway-integration":{"type":"http","httpMethod":"GET","uri":"http://example.com"}}}}}`

	f := newDP(t)

	imp, err := f.m.ImportRestAPI(ctx(), &driver.ImportRestAPIInput{Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}

	if got := f.importedGet(t, imp.API.ID); got.AuthorizationType != "AWS_IAM" || got.AuthorizerID != "" {
		t.Fatalf("a scheme with authtype awsSigv4 must mean AWS_IAM: %+v", got)
	}
}

func TestAuthorizerAuthTypeRoundTrips(t *testing.T) {
	cases := []struct {
		name, authType string
		wantField      bool
	}{
		{"set", "custom", true},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDP(t)

			az, err := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{
				Name: "tok", Type: driver.AuthorizerToken, AuthorizerURI: lambdaURI, AuthType: tc.authType,
			})
			if err != nil {
				t.Fatal(err)
			}

			f.method(t, driver.PutMethodInput{AuthorizationType: "CUSTOM", AuthorizerID: az.ID}, nil)

			body, apiID := f.roundTrip(t, "swagger")

			if has := strings.Contains(body, "x-amazon-apigateway-authtype"); has != tc.wantField {
				t.Fatalf("authtype present = %v, want %v\n%s", has, tc.wantField, body)
			}

			azs, _ := f.m.GetAuthorizers(ctx(), apiID, driver.PageInput{})
			if len(azs.Items) != 1 || azs.Items[0].AuthType != tc.authType {
				t.Fatalf("AuthType %q must survive: %+v", tc.authType, azs)
			}
		})
	}
}

func TestRequestAuthorizerWithoutIdentitySourceRoundTrips(t *testing.T) {
	f := newDP(t)

	az, err := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{
		Name: "req", Type: driver.AuthorizerRequest, AuthorizerURI: strings.Replace(lambdaURI, "hello", "auth", 1),
		AuthorizerResultTTLInSeconds: intPtr(0),
	})
	if err != nil {
		t.Fatal(err)
	}

	f.method(t, driver.PutMethodInput{AuthorizationType: "CUSTOM", AuthorizerID: az.ID}, nil)

	body, apiID := f.roundTrip(t, "swagger")
	if !strings.Contains(body, `"name": "Unused"`) && !strings.Contains(body, `"name":"Unused"`) {
		t.Fatalf("a REQUEST authorizer with no identity source exports name Unused: %s", body)
	}

	azs, _ := f.m.GetAuthorizers(ctx(), apiID, driver.PageInput{})
	if len(azs.Items) != 1 || azs.Items[0].IdentitySource != "" {
		t.Fatalf("the identity source must stay empty: %+v", azs)
	}

	auth := &fakeInvoker{output: []byte(`{"principalId":"u","policyDocument":{"Version":"2012-10-17","Statement":[{"Action":"execute-api:Invoke","Effect":"Allow","Resource":"*"}]}}`)}
	f.m.SetLambdaInvoker(routeInvoker{"auth": auth, "hello": f.inv})

	if _, err = f.m.CreateDeployment(ctx(), apiID, driver.CreateDeploymentInput{StageName: "prod"}); err != nil {
		t.Fatal(err)
	}

	resp, err := f.m.InvokeRoute(ctx(), &driver.ProxyRequest{RestAPIID: apiID, StageName: "prod", HTTPMethod: "GET", Path: "/items"})
	if err != nil {
		t.Fatal(err)
	}

	status(t, resp, 200)
}

func TestMultiSourceRequestAuthorizerRoundTrips(t *testing.T) {
	const sources = "method.request.header.X-A,method.request.querystring.q"

	f := newDP(t)

	az, err := f.m.CreateAuthorizer(ctx(), f.api.ID, &driver.CreateAuthorizerInput{
		Name: "req", Type: driver.AuthorizerRequest, AuthorizerURI: lambdaURI, IdentitySource: sources,
		AuthorizerResultTTLInSeconds: intPtr(30),
	})
	if err != nil {
		t.Fatal(err)
	}

	f.method(t, driver.PutMethodInput{AuthorizationType: "CUSTOM", AuthorizerID: az.ID}, nil)

	body, apiID := f.roundTrip(t, "swagger")
	if !strings.Contains(body, "Unused") || !strings.Contains(body, sources) {
		t.Fatalf("a multi-source REQUEST authorizer exports name Unused and keeps its identitySource: %s", body)
	}

	azs, _ := f.m.GetAuthorizers(ctx(), apiID, driver.PageInput{})
	if len(azs.Items) != 1 || azs.Items[0].IdentitySource != sources {
		t.Fatalf("the multi-source identity source must survive: %+v", azs)
	}
}
