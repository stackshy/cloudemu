package apigateway_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// invoke sends a data-plane request and returns status, headers and body.
func invoke(t *testing.T, method, url, contentType, accept, body string) (int, http.Header, string) {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, resp.Header, string(raw)
}

// buildMockAPI creates /pets with a MOCK GET whose request template picks the
// status from ?code=, three method responses and two integration responses,
// plus a MOCK POST that never passes unmapped content through. It deploys to
// stage "test" with a stage variable and returns the API id and the
// method base URL.
func buildMockAPI(t *testing.T, base string) (apiID, resourceID string) {
	t.Helper()

	api := doJSON(t, http.MethodPost, base+"/restapis", `{"name":"mock"}`)
	apiID, _ = api["id"].(string)
	rootID, _ := api["rootResourceId"].(string)

	res := doJSON(t, http.MethodPost, base+"/restapis/"+apiID+"/resources/"+rootID, `{"pathPart":"pets"}`)
	resourceID, _ = res["id"].(string)

	m := base + "/restapis/" + apiID + "/resources/" + resourceID + "/methods/GET"
	doJSON(t, http.MethodPut, m, `{"authorizationType":"NONE","requestParameters":{"method.request.querystring.code":false}}`)
	doJSON(t, http.MethodPut, m+"/integration", `{"type":"MOCK","requestTemplates":{"application/json":`+
		`"{\"statusCode\": #if($input.params('code') != \"\")$input.params('code')#{else}200#end}"}}`)
	doJSON(t, http.MethodPut, m+"/responses/200", `{"responseParameters":{"method.response.header.X-Custom":false}}`)
	doJSON(t, http.MethodPut, m+"/responses/404", `{}`)
	doJSON(t, http.MethodPut, m+"/responses/500", `{}`)
	doJSON(t, http.MethodPut, m+"/integration/responses/200", `{"selectionPattern":"",`+
		`"responseParameters":{"method.response.header.X-Custom":"'yes'"},`+
		`"responseTemplates":{"application/json":`+
		`"{\"name\":\"$input.params('name')\",\"stage\":\"$context.stage\",\"v\":\"$stageVariables.v\"}"}}`)
	doJSON(t, http.MethodPut, m+"/integration/responses/404", `{"selectionPattern":"4\\d\\d",`+
		`"responseTemplates":{"application/json":"{\"error\":\"not found\"}"}}`)

	p := base + "/restapis/" + apiID + "/resources/" + resourceID + "/methods/POST"
	doJSON(t, http.MethodPut, p, `{"authorizationType":"NONE"}`)
	doJSON(t, http.MethodPut, p+"/integration", `{"type":"MOCK","passthroughBehavior":"NEVER",`+
		`"requestTemplates":{"application/json":"{\"statusCode\": 200}"}}`)
	doJSON(t, http.MethodPut, p+"/responses/200", `{}`)
	doJSON(t, http.MethodPut, p+"/integration/responses/200", `{"responseTemplates":{"application/json":"$input.json('$')"}}`)

	doJSON(t, http.MethodPost, base+"/restapis/"+apiID+"/deployments", `{"stageName":"test","variables":{"v":"one"}}`)

	return apiID, resourceID
}

func TestMockIntegrationInvoke(t *testing.T) {
	srv := newE2E(t)
	apiID, _ := buildMockAPI(t, srv.URL)
	stage := srv.URL + "/restapis/" + apiID + "/test/_user_request_/pets"

	status, hdr, body := invoke(t, http.MethodGet, stage+"?name=rex", "", "", "")
	if status != http.StatusOK || body != `{"name":"rex","stage":"test","v":"one"}` {
		t.Fatalf("GET 200 = %d %s", status, body)
	}

	if hdr.Get("X-Custom") != "yes" || hdr.Get("Content-Type") != "application/json" || hdr.Get("X-Amzn-Requestid") == "" {
		t.Fatalf("GET 200 headers = %v", hdr)
	}

	status, _, body = invoke(t, http.MethodGet, stage+"?code=404", "", "", "")
	if status != http.StatusNotFound || body != `{"error":"not found"}` {
		t.Fatalf("GET 404 = %d %s", status, body)
	}

	// 500 has a method response but no integration response matches it and
	// 200's default does: the default wins and maps to 200.
	status, _, _ = invoke(t, http.MethodGet, stage+"?code=500", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("GET 500 falls to default = %d", status)
	}

	status, hdr, body = invoke(t, http.MethodPost, stage, "application/json", "", `{"a":1,"b":[true]}`)
	if status != http.StatusOK || body != "{}" {
		t.Fatalf("POST json = %d %s", status, body)
	}

	status, hdr, body = invoke(t, http.MethodPost, stage, "text/plain", "", "hello")
	if status != http.StatusUnsupportedMediaType || body != `{"message": "Unsupported Media Type"}` ||
		hdr.Get("X-Amzn-Errortype") != "UnsupportedMediaTypeException" {
		t.Fatalf("POST text/plain = %d %s %v", status, body, hdr)
	}
}

func TestMockIntegrationNoMatchingResponse(t *testing.T) {
	srv := newE2E(t)
	apiID, resourceID := buildMockAPI(t, srv.URL)
	m := srv.URL + "/restapis/" + apiID + "/resources/" + resourceID + "/methods/GET"

	// Drop the default response: a 200 from the mock now matches nothing.
	deleteOK(t, m+"/integration/responses/200")
	doJSON(t, http.MethodPost, srv.URL+"/restapis/"+apiID+"/deployments", `{"stageName":"test"}`)

	status, hdr, body := invoke(t, http.MethodGet, srv.URL+"/restapis/"+apiID+"/test/_user_request_/pets", "", "", "")
	if status != http.StatusInternalServerError || body != `{"message": "Internal server error"}` ||
		hdr.Get("X-Amzn-Errortype") != "InternalServerErrorException" {
		t.Fatalf("no match = %d %s %v", status, body, hdr)
	}
}

func TestMockIntegrationUpdateAndRedeploy(t *testing.T) {
	srv := newE2E(t)
	apiID, resourceID := buildMockAPI(t, srv.URL)
	m := srv.URL + "/restapis/" + apiID + "/resources/" + resourceID + "/methods/GET"

	ir := doJSON(t, http.MethodPatch, m+"/integration/responses/200", `{"patchOperations":[`+
		`{"op":"replace","path":"/responseTemplates/application~1json","value":"{\"changed\":true}"}]}`)
	if tm, _ := ir["responseTemplates"].(map[string]any); tm["application/json"] != `{"changed":true}` {
		t.Fatalf("UpdateIntegrationResponse = %v", ir)
	}

	stage := srv.URL + "/restapis/" + apiID + "/test/_user_request_/pets"
	if _, _, body := invoke(t, http.MethodGet, stage, "", "", ""); body == `{"changed":true}` {
		t.Fatal("live edit visible before redeploy")
	}

	doJSON(t, http.MethodPost, srv.URL+"/restapis/"+apiID+"/deployments", `{"stageName":"test"}`)

	if _, _, body := invoke(t, http.MethodGet, stage, "", "", ""); body != `{"changed":true}` {
		t.Fatalf("after redeploy body = %s", body)
	}

	mth := doJSON(t, http.MethodGet, m, "")
	mrs, _ := mth["methodResponses"].(map[string]any)
	ig, _ := mth["methodIntegration"].(map[string]any)
	irs, _ := ig["integrationResponses"].(map[string]any)

	if len(mrs) != 3 || len(irs) != 2 || ig["cacheNamespace"] != resourceID {
		t.Fatalf("GetMethod = %v", mth)
	}

	mr := doJSON(t, http.MethodPatch, m+"/responses/404", `{"patchOperations":[`+
		`{"op":"add","path":"/responseModels/application~1json","value":"Empty"}]}`)
	if models, _ := mr["responseModels"].(map[string]any); models["application/json"] != "Empty" {
		t.Fatalf("UpdateMethodResponse = %v", mr)
	}

	ig = doJSON(t, http.MethodPatch, m+"/integration", `{"patchOperations":[`+
		`{"op":"add","path":"/requestTemplates/text~1plain","value":"{}"},`+
		`{"op":"replace","path":"/passthroughBehavior","value":"WHEN_NO_TEMPLATES"}]}`)
	if tm, _ := ig["requestTemplates"].(map[string]any); len(tm) != 2 || ig["passthroughBehavior"] != "WHEN_NO_TEMPLATES" {
		t.Fatalf("UpdateIntegration = %v", ig)
	}
}

func TestMethodAndIntegrationResponseErrors(t *testing.T) {
	srv := newE2E(t)
	apiID, resourceID := buildMockAPI(t, srv.URL)
	m := srv.URL + "/restapis/" + apiID + "/resources/" + resourceID + "/methods/GET"

	const (
		badRequest = "BadRequestException"
		notFound   = "NotFoundException"
	)

	assertWireError(t, http.MethodPut, m+"/responses/200", `{}`, http.StatusConflict, "ConflictException",
		"Response already exists for this resource")
	assertWireError(t, http.MethodPut, m+"/responses/99", `{}`, http.StatusBadRequest, badRequest, "Invalid status code specified")
	assertWireError(t, http.MethodPut, m+"/integration/responses/201", `{}`, http.StatusNotFound, notFound,
		"Invalid Response status code specified")
	assertWireError(t, http.MethodGet, m+"/responses/418", "", http.StatusNotFound, notFound,
		"Invalid Response status code specified")
	assertWireError(t, http.MethodPut, m+"/integration/responses/404",
		`{"responseParameters":{"method.response.header.X-Nope":"'x'"}}`, http.StatusBadRequest, badRequest,
		"Invalid mapping expression specified: Validation Result: warnings : [], errors : "+
			"[Invalid mapping expression parameter specified: method.response.header.X-Nope]")
	assertWireError(t, http.MethodPut, m+"/integration/responses/404", `{"selectionPattern":"("}`,
		http.StatusBadRequest, badRequest, "Invalid selection pattern specified")
	assertWireError(t, http.MethodPatch, m+"/integration", `{"patchOperations":[`+
		`{"op":"add","path":"/requestParameters/integration.request.header.X","value":"method.request.header.Undeclared"}]}`,
		http.StatusBadRequest, badRequest, "Invalid mapping expression specified: Validation Result: warnings : [], errors : "+
			"[Invalid mapping expression parameter specified: method.request.header.Undeclared]")
	assertWireError(t, http.MethodPatch, m+"/integration", `{"patchOperations":[`+
		`{"op":"replace","path":"/passthroughBehavior","value":"SOMETIMES"}]}`,
		http.StatusBadRequest, badRequest, "Invalid passthrough behavior specified")

	deleteOK(t, m+"/integration/responses/404")
	deleteOK(t, m+"/responses/404")
	assertWireError(t, http.MethodDelete, m+"/responses/404", "", http.StatusNotFound, notFound,
		"Invalid Response status code specified")
}
