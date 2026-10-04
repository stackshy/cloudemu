package serverkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	cognitodriver "github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

func TestServeCognitoCode(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	pool, err := cloud.Cognito.CreateUserPool(ctx, cognitodriver.CreateUserPoolInput{
		Name: "codes", AutoVerifiedAttributes: []string{"email"},
	})
	if err != nil {
		t.Fatalf("CreateUserPool: %v", err)
	}

	client, err := cloud.Cognito.CreateUserPoolClient(ctx, cognitodriver.CreateUserPoolClientInput{UserPoolID: pool.ID, ClientName: "c"})
	if err != nil {
		t.Fatalf("CreateUserPoolClient: %v", err)
	}

	if _, err = cloud.Cognito.SignUp(ctx, cognitodriver.SignUpInput{
		ClientUserInput: cognitodriver.ClientUserInput{ClientID: client.ClientID, Username: "alice"},
		Password:        "Passw0rd!",
		UserAttributes:  []cognitodriver.Attribute{{Name: "email", Value: "alice@example.com"}},
	}); err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	regions := map[string]*awsprovider.Provider{"us-east-1": cloud}
	get := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		serveCognitoCode(rec, httptest.NewRequest(http.MethodGet, "/_cloudemu/cognito/codes?"+query, nil), regions)

		return rec
	}

	rec := get("userPoolId=" + pool.ID + "&username=alice")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var out cognitoCodeJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want, _ := cloud.Cognito.ConfirmationCode(ctx, pool.ID, "alice")
	if out.Code != want.Code || out.DeliveryMedium != "EMAIL" || out.Destination != "a***@e***" {
		t.Fatalf("codes = %+v", out)
	}

	for query, status := range map[string]int{
		"userPoolId=" + pool.ID + "&username=ghost": http.StatusNotFound,
		"userPoolId=eu-west-1_abc&username=alice":   http.StatusNotFound,
		"username=alice": http.StatusBadRequest,
	} {
		if rec := get(query); rec.Code != status {
			t.Fatalf("%s: status = %d, want %d", query, rec.Code, status)
		}
	}
}
