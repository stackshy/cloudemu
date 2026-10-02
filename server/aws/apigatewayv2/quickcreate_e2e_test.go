package apigatewayv2_test

import (
	"net/http"
	"testing"
)

const lambdaARN = "arn:aws:lambda:us-east-1:000000000000:function:quick"

// TestE2E_QuickCreateLambda proves CreateApi with Target builds the managed
// integration, route and auto-deployed $default stage.
func TestE2E_QuickCreateLambda(t *testing.T) {
	ts := newE2E(t)
	apiID := newAPI(t, ts.URL, `{"name":"q","protocolType":"HTTP","target":"`+lambdaARN+`","routeKey":"GET /pets",`+
		`"credentialsArn":"arn:aws:iam::000000000000:role/apigw"}`)
	apiBase := ts.URL + "/v2/apis/" + apiID

	igs := items(mustDo(t, http.MethodGet, apiBase+"/integrations", "", http.StatusOK))
	if len(igs) != 1 {
		t.Fatalf("integrations = %v, want 1", igs)
	}

	ig, _ := igs[0].(map[string]any)
	if ig["integrationType"] != "AWS_PROXY" || ig["integrationUri"] != lambdaARN || ig["payloadFormatVersion"] != "2.0" ||
		ig["apiGatewayManaged"] != true || ig["credentialsArn"] != "arn:aws:iam::000000000000:role/apigw" {
		t.Fatalf("quick-create integration = %v", ig)
	}

	routes := items(mustDo(t, http.MethodGet, apiBase+"/routes", "", http.StatusOK))
	if len(routes) != 1 {
		t.Fatalf("routes = %v, want 1", routes)
	}

	rt, _ := routes[0].(map[string]any)
	if rt["routeKey"] != "GET /pets" || rt["target"] != "integrations/"+ig["integrationId"].(string) || rt["apiGatewayManaged"] != true {
		t.Fatalf("quick-create route = %v", rt)
	}

	stage := mustDo(t, http.MethodGet, apiBase+"/stages/$default", "", http.StatusOK)
	if stage["autoDeploy"] != true || stage["apiGatewayManaged"] != true || stage["deploymentId"] == nil {
		t.Fatalf("quick-create stage = %v", stage)
	}

	wantErr(t, http.MethodDelete, apiBase+"/integrations/"+ig["integrationId"].(string), "",
		http.StatusBadRequest, "BadRequestException", "Cannot delete an integration managed by API Gateway")

	wantErr(t, http.MethodDelete, apiBase+"/stages/$default", "",
		http.StatusBadRequest, "BadRequestException", "Cannot modify or delete a stage managed by API Gateway")

	// UpdateApi with a URL target switches the managed integration to HTTP_PROXY.
	mustDo(t, http.MethodPatch, apiBase, `{"target":"https://example.com/pets"}`, http.StatusOK)

	upd := mustDo(t, http.MethodGet, apiBase+"/integrations/"+ig["integrationId"].(string), "", http.StatusOK)
	if upd["integrationType"] != "HTTP_PROXY" || upd["integrationUri"] != "https://example.com/pets" || upd["payloadFormatVersion"] != "1.0" {
		t.Fatalf("UpdateApi target integration = %v", upd)
	}
}

// TestE2E_QuickCreateDefaultsAndErrors covers the $default route key default
// and the quick-create input rules.
func TestE2E_QuickCreateDefaultsAndErrors(t *testing.T) {
	ts := newE2E(t)
	apiID := newAPI(t, ts.URL, `{"name":"q","protocolType":"HTTP","target":"https://example.com"}`)

	routes := items(mustDo(t, http.MethodGet, ts.URL+"/v2/apis/"+apiID+"/routes", "", http.StatusOK))
	rt, _ := routes[0].(map[string]any)

	if rt["routeKey"] != "$default" {
		t.Fatalf("quick-create default route key = %v", rt["routeKey"])
	}

	igs := items(mustDo(t, http.MethodGet, ts.URL+"/v2/apis/"+apiID+"/integrations", "", http.StatusOK))
	ig, _ := igs[0].(map[string]any)

	if ig["integrationType"] != "HTTP_PROXY" || ig["integrationMethod"] != "ANY" || ig["payloadFormatVersion"] != "1.0" {
		t.Fatalf("quick-create HTTP integration = %v", ig)
	}

	wantErr(t, http.MethodPost, ts.URL+"/v2/apis", `{"name":"q","protocolType":"HTTP","routeKey":"GET /x"}`,
		http.StatusBadRequest, "BadRequestException", "RouteKey and CredentialsArn can only be specified with Target")

	wantErr(t, http.MethodPost, ts.URL+"/v2/apis",
		`{"name":"q","protocolType":"WEBSOCKET","routeSelectionExpression":"$request.body.action","target":"`+lambdaARN+`"}`,
		http.StatusBadRequest, "BadRequestException", "Quick create is only supported for HTTP APIs")

	wantErr(t, http.MethodPost, ts.URL+"/v2/apis", `{"name":"q","protocolType":"HTTP","target":"not-a-target"}`,
		http.StatusBadRequest, "BadRequestException", "Target must be a Lambda function ARN or an HTTP(S) URL")
}
