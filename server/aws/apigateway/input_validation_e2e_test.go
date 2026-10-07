package apigateway_test

import (
	"net/http"
	"testing"
)

// TestE2E_InputValidationErrors checks the wire shape of the authorization-type,
// integration-timeout and stage-variable validations, and that the
// /httpMethod integration patch round-trips.
func TestE2E_InputValidationErrors(t *testing.T) {
	srv := newE2E(t)
	base := srv.URL

	api := doJSON(t, http.MethodPost, base+"/restapis", `{"name":"v"}`)
	apiID, _ := api["id"].(string)
	rootID, _ := api["rootResourceId"].(string)
	methods := base + "/restapis/" + apiID + "/resources/" + rootID + "/methods/"

	assertWireError(t, http.MethodPut, methods+"GET", `{"authorizationType":"BASIC"}`,
		http.StatusBadRequest, "BadRequestException", "Invalid authorization type specified")

	doJSON(t, http.MethodPut, methods+"GET", `{"authorizationType":"AWS_IAM"}`)

	assertWireError(t, http.MethodPatch, methods+"GET",
		`{"patchOperations":[{"op":"replace","path":"/authorizationType","value":"BASIC"}]}`,
		http.StatusBadRequest, "BadRequestException", "Invalid authorization type specified")

	for _, timeout := range []string{"10", "30000"} {
		assertWireError(t, http.MethodPut, methods+"GET/integration",
			`{"type":"HTTP","httpMethod":"GET","uri":"https://example.com","timeoutInMillis":`+timeout+`}`,
			http.StatusBadRequest, "BadRequestException", "Timeout should be between 50 ms and 29000 ms")
	}

	doJSON(t, http.MethodPut, methods+"GET/integration",
		`{"type":"HTTP","httpMethod":"GET","uri":"https://example.com","timeoutInMillis":29000}`)

	assertWireError(t, http.MethodPatch, methods+"GET/integration",
		`{"patchOperations":[{"op":"replace","path":"/timeoutInMillis","value":"49"}]}`,
		http.StatusBadRequest, "BadRequestException", "Timeout should be between 50 ms and 29000 ms")

	ig := doJSON(t, http.MethodPatch, methods+"GET/integration",
		`{"patchOperations":[{"op":"replace","path":"/httpMethod","value":"POST"}]}`)
	if ig["httpMethod"] != "POST" {
		t.Fatalf("/httpMethod patch did not round-trip: %v", ig)
	}

	dep := doJSON(t, http.MethodPost, base+"/restapis/"+apiID+"/deployments", `{}`)
	depID, _ := dep["id"].(string)

	assertWireError(t, http.MethodPost, base+"/restapis/"+apiID+"/stages",
		`{"stageName":"prod","deploymentId":"`+depID+`","variables":{"bad-name":"v"}}`,
		http.StatusBadRequest, "BadRequestException",
		"Stage variable names may only contain alphanumeric characters and underscores")

	assertWireError(t, http.MethodPost, base+"/restapis/"+apiID+"/stages",
		`{"stageName":"prod","deploymentId":"`+depID+`","variables":{"name":"has space"}}`,
		http.StatusBadRequest, "BadRequestException",
		"Stage variable values must match the regular expression [A-Za-z0-9-._~:/?#&=,]+")

	assertWireError(t, http.MethodPost, base+"/restapis/"+apiID+"/deployments",
		`{"stageName":"live","variables":{"name":"semi;colon"}}`,
		http.StatusBadRequest, "BadRequestException",
		"Stage variable values must match the regular expression [A-Za-z0-9-._~:/?#&=,]+")

	st := doJSON(t, http.MethodPost, base+"/restapis/"+apiID+"/stages",
		`{"stageName":"prod","deploymentId":"`+depID+`","variables":{"env":"prod-1"}}`)

	vars, _ := st["variables"].(map[string]any)
	if vars["env"] != "prod-1" {
		t.Fatalf("valid stage variables did not round-trip: %v", st)
	}

	assertWireError(t, http.MethodPatch, base+"/restapis/"+apiID+"/stages/prod",
		`{"patchOperations":[{"op":"add","path":"/variables/x","value":"a b"}]}`,
		http.StatusBadRequest, "BadRequestException",
		"Stage variable values must match the regular expression [A-Za-z0-9-._~:/?#&=,]+")
}
