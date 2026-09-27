package apigatewayv2_test

import (
	"net/http"
	"testing"
)

const badHTTPRouteKey = `The provided route key is not formatted properly for HTTP protocol. ` +
	`Format should be "<HTTP METHOD> /<RESOURCE PATH>" or "$default"`

// TestE2E_RouteValidation covers the RouteKey format, duplicate keys and the
// integration a route targets.
func TestE2E_RouteValidation(t *testing.T) {
	ts := newE2E(t)
	apiID := newHTTPAPI(t, ts.URL)
	apiBase := ts.URL + "/v2/apis/" + apiID
	igID := newLambdaIntegration(t, apiBase)

	for _, key := range []string{"GET", "/items", "FETCH /items", "GET items", "$connect"} {
		wantErr(t, http.MethodPost, apiBase+"/routes", `{"routeKey":"`+key+`"}`,
			http.StatusBadRequest, "BadRequestException", badHTTPRouteKey)
	}

	for _, key := range []string{"$default", "ANY /{proxy+}", "GET /items/{id}"} {
		mustDo(t, http.MethodPost, apiBase+"/routes", `{"routeKey":"`+key+`"}`, http.StatusCreated)
	}

	wantErr(t, http.MethodPost, apiBase+"/routes", `{"routeKey":"GET /items/{id}"}`,
		http.StatusConflict, "ConflictException", "Route with key GET /items/{id} already exists for this API")

	wantErr(t, http.MethodPost, apiBase+"/routes", `{"routeKey":"POST /x","target":"integrations/missing123"}`,
		http.StatusBadRequest, "BadRequestException", "Invalid Integration identifier specified")

	rt := mustDo(t, http.MethodPost, apiBase+"/routes",
		`{"routeKey":"POST /x","target":"integrations/`+igID+`"}`, http.StatusCreated)
	rtID, _ := rt["routeId"].(string)

	wantErr(t, http.MethodPatch, apiBase+"/routes/"+rtID, `{"routeKey":"$default"}`,
		http.StatusConflict, "ConflictException", "Route with key $default already exists for this API")

	wantErr(t, http.MethodPatch, apiBase+"/routes/"+rtID, `{"target":"integrations/missing123"}`,
		http.StatusBadRequest, "BadRequestException", "Invalid Integration identifier specified")

	upd := mustDo(t, http.MethodPatch, apiBase+"/routes/"+rtID,
		`{"authorizationType":"JWT","authorizationScopes":["read","write"]}`, http.StatusOK)

	scopes, _ := upd["authorizationScopes"].([]any)
	if upd["authorizationType"] != "JWT" || len(scopes) != 2 {
		t.Fatalf("UpdateRoute scopes = %v", upd)
	}
}

// TestE2E_IntegrationValidation covers the integration types and payload
// format versions each protocol accepts.
func TestE2E_IntegrationValidation(t *testing.T) {
	ts := newE2E(t)
	httpBase := ts.URL + "/v2/apis/" + newHTTPAPI(t, ts.URL)
	wsBase := ts.URL + "/v2/apis/" + newAPI(t, ts.URL,
		`{"name":"ws","protocolType":"WEBSOCKET","routeSelectionExpression":"$request.body.action"}`)

	wantErr(t, http.MethodPost, httpBase+"/integrations", `{"integrationType":"MOCK"}`,
		http.StatusBadRequest, "BadRequestException", "Integration type MOCK is not supported for HTTP APIs")

	wantErr(t, http.MethodPost, httpBase+"/integrations", `{"integrationType":"LAMBDA"}`,
		http.StatusBadRequest, "BadRequestException", "Invalid integration type specified: LAMBDA")

	wantErr(t, http.MethodPost, httpBase+"/integrations",
		`{"integrationType":"HTTP_PROXY","integrationUri":"https://example.com","integrationMethod":"GET","payloadFormatVersion":"2.0"}`,
		http.StatusBadRequest, "BadRequestException", "Payload format version 2.0 is only supported for AWS_PROXY integrations")

	wantErr(t, http.MethodPost, httpBase+"/integrations",
		`{"integrationType":"AWS_PROXY","integrationUri":"arn:aws:lambda:us-east-1:000000000000:function:f","payloadFormatVersion":"3.0"}`,
		http.StatusBadRequest, "BadRequestException", "Invalid payload format version specified: 3.0")

	wantErr(t, http.MethodPost, httpBase+"/integrations",
		`{"integrationType":"AWS_PROXY","integrationUri":"arn:aws:lambda:us-east-1:000000000000:function:f","timeoutInMillis":40000}`,
		http.StatusBadRequest, "BadRequestException", "TimeoutInMillis must be between 50 and 30000")

	mustDo(t, http.MethodPost, wsBase+"/integrations", `{"integrationType":"MOCK"}`, http.StatusCreated)

	wantErr(t, http.MethodPost, wsBase+"/integrations", `{"integrationType":"AWS_PROXY","payloadFormatVersion":"2.0"}`,
		http.StatusBadRequest, "BadRequestException", "Payload format version 2.0 is not supported for WEBSOCKET APIs")
}

// TestE2E_APIAndStageValidation covers the WebSocket route selection
// expression and the stage name rules.
func TestE2E_APIAndStageValidation(t *testing.T) {
	ts := newE2E(t)

	wantErr(t, http.MethodPost, ts.URL+"/v2/apis", `{"name":"ws","protocolType":"WEBSOCKET"}`,
		http.StatusBadRequest, "BadRequestException", "RouteSelectionExpression is required for WEBSOCKET protocol")

	wantErr(t, http.MethodPost, ts.URL+"/v2/apis",
		`{"name":"h","protocolType":"HTTP","routeSelectionExpression":"$request.body.action"}`,
		http.StatusBadRequest, "BadRequestException", "Only $request.method $request.path is supported for HTTP APIs")

	wsID := newAPI(t, ts.URL, `{"name":"ws","protocolType":"WEBSOCKET","routeSelectionExpression":"$request.body.action"}`)
	mustDo(t, http.MethodPost, ts.URL+"/v2/apis/"+wsID+"/routes", `{"routeKey":"$connect"}`, http.StatusCreated)
	mustDo(t, http.MethodPost, ts.URL+"/v2/apis/"+wsID+"/routes", `{"routeKey":"sendMessage"}`, http.StatusCreated)

	apiBase := ts.URL + "/v2/apis/" + newHTTPAPI(t, ts.URL)

	wantErr(t, http.MethodPost, apiBase+"/stages", `{"stageName":"bad name!"}`,
		http.StatusBadRequest, "BadRequestException", "Stage name only allows a-zA-Z0-9._- or $default")

	mustDo(t, http.MethodPost, apiBase+"/stages", `{"stageName":"prod.v1_a-b"}`, http.StatusCreated)
}
