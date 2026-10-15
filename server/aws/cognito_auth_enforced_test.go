package aws

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	cognitodriver "github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// TestEnforcedCognitoSignInIsPublic drives sign-up, confirmation, sign-in and
// the JWKS fetch with no SigV4 at all under --enforce-auth, and checks that the
// admin operations and other .well-known paths still need credentials.
func TestEnforcedCognitoSignInIsPublic(t *testing.T) {
	ts, cloud := enforcedServer(t)
	ctx := context.Background()

	pool, err := cloud.Cognito.CreateUserPool(ctx, cognitodriver.CreateUserPoolInput{
		Name: "enforced", AutoVerifiedAttributes: []string{"email"},
	})
	if err != nil {
		t.Fatalf("CreateUserPool: %v", err)
	}

	client, err := cloud.Cognito.CreateUserPoolClient(ctx, cognitodriver.CreateUserPoolClientInput{
		UserPoolID: pool.ID, ClientName: "web", ExplicitAuthFlows: []string{"ALLOW_USER_PASSWORD_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"},
	})
	if err != nil {
		t.Fatalf("CreateUserPoolClient: %v", err)
	}

	call := func(op, body string) (int, string) {
		return doRaw(t, ts, jsonRPC(idpTarget+op, body))
	}

	if status, body := call("SignUp", `{"ClientId":"`+client.ClientID+`","Username":"alice","Password":"Passw0rd!",`+
		`"UserAttributes":[{"Name":"email","Value":"alice@example.com"}]}`); status != http.StatusOK {
		t.Fatalf("SignUp = %d %s", status, body)
	}

	code, err := cloud.Cognito.ConfirmationCode(ctx, pool.ID, "alice")
	if err != nil {
		t.Fatalf("ConfirmationCode: %v", err)
	}

	if status, body := call("ConfirmSignUp", `{"ClientId":"`+client.ClientID+`","Username":"alice","ConfirmationCode":"`+
		code.Code+`"}`); status != http.StatusOK {
		t.Fatalf("ConfirmSignUp = %d %s", status, body)
	}

	status, body := call("InitiateAuth", `{"ClientId":"`+client.ClientID+`","AuthFlow":"USER_PASSWORD_AUTH",`+
		`"AuthParameters":{"USERNAME":"alice","PASSWORD":"Passw0rd!"}}`)
	if status != http.StatusOK {
		t.Fatalf("InitiateAuth = %d %s", status, body)
	}

	var auth struct {
		AuthenticationResult struct{ AccessToken string } `json:"AuthenticationResult"`
	}
	_ = json.Unmarshal([]byte(body), &auth)

	if status, body = call("GetUser", `{"AccessToken":"`+auth.AuthenticationResult.AccessToken+`"}`); status != http.StatusOK {
		t.Fatalf("GetUser = %d %s", status, body)
	}

	if status, body = doRaw(t, ts, rawReq{method: http.MethodGet, path: "/" + pool.ID + "/.well-known/jwks.json"}); status != http.StatusOK ||
		!strings.Contains(body, `"keys"`) {
		t.Fatalf("jwks = %d %s", status, body)
	}

	denied := []struct {
		name string
		req  rawReq
	}{
		{"CreateGroup", jsonRPC(idpTarget+"CreateGroup", `{"UserPoolId":"`+pool.ID+`","GroupName":"g"}`)},
		{"AdminInitiateAuth", jsonRPC(idpTarget+"AdminInitiateAuth", `{}`)},
		{"other well-known path", rawReq{method: http.MethodGet, path: "/" + pool.ID + "/.well-known/other"}},
		{"POST jwks", rawReq{method: http.MethodPost, path: "/" + pool.ID + "/.well-known/jwks.json"}},
	}

	for _, tc := range denied {
		if status, body := doRaw(t, ts, tc.req); status != http.StatusForbidden || !strings.Contains(body, missingTok) {
			t.Fatalf("%s unsigned = %d %s, want 403 %s", tc.name, status, body, missingTok)
		}
	}
}
