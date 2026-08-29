package azure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const claimsAuthSubscriptionID = "11111111-1111-1111-1111-111111111111"

// makeJWT hand-builds an unsigned (dummy-signature) three-part JWT from the
// given claims. cloudemu validates a token's structure and claims, not its
// signature, so a real signing key is unnecessary — this is exactly the shape a
// real Azure token has on the wire.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	enc := base64.RawURLEncoding.EncodeToString

	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	return enc(header) + "." + enc(payload) + "." + enc([]byte("dummy-signature"))
}

// validClaims returns a well-formed claim set: the ARM audience, an object id
// principal, a tenant, and an expiry far in the future.
func validClaims() map[string]any {
	return map[string]any{
		"aud": "https://management.azure.com",
		"oid": "abcd1234-0000-0000-0000-00000000abcd",
		"tid": claimsAuthSubscriptionID,
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	}
}

// staticJWTCred is an azcore.TokenCredential that always returns the same
// pre-built JWT, so a real Azure SDK client injects it as the bearer token.
type staticJWTCred struct{ token string }

func (c staticJWTCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: c.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// armClientOptions points an ARM client at the emulator's TLS endpoint with the
// ARM audience configured. ARM is a bearer-token API, so it runs over TLS.
func armClientOptions(sess *compat.AzureSession) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {
						Endpoint: sess.Endpoint(),
						Audience: "https://management.azure.com",
					},
				},
			},
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

// listRoleDefinitions drives one real ARM read (role-definition list) through
// the armauthorization SDK using cred, returning any error. A nil error means
// the claims gate admitted the request and the handler served it.
func listRoleDefinitions(ctx context.Context, sess *compat.AzureSession, cred azcore.TokenCredential) error {
	cf, err := armauthorization.NewClientFactory(claimsAuthSubscriptionID, cred, armClientOptions(sess))
	if err != nil {
		return err
	}

	pager := cf.NewRoleDefinitionsClient().NewListPager("/subscriptions/"+claimsAuthSubscriptionID, nil)
	for pager.More() {
		if _, err := pager.NextPage(ctx); err != nil {
			return err
		}
	}

	return nil
}

// TestCompatAzureClaimsAuthDisabled confirms the default (auth off) path is
// unchanged: a normal Azure SDK call still succeeds with the harness's fake
// credential (whose token is not even a JWT).
func TestCompatAzureClaimsAuthDisabled(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{IAM: provider.IAM})

	if err := listRoleDefinitions(context.Background(), sess, compat.FakeAzureCred()); err != nil {
		t.Fatalf("ARM call with auth disabled should succeed, got: %v", err)
	}
}

// TestCompatAzureClaimsAuthEnabledValid exercises the enforced path with a real
// SDK client injecting a valid bearer JWT (ARM audience, an oid principal): the
// ARM call succeeds, proving the gate admitted it and dispatch proceeded.
func TestCompatAzureClaimsAuthEnabledValid(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{IAM: provider.IAM, EnforceAuth: true})

	cred := staticJWTCred{token: makeJWT(t, validClaims())}
	if err := listRoleDefinitions(context.Background(), sess, cred); err != nil {
		t.Fatalf("ARM call with a valid bearer token should succeed, got: %v", err)
	}
}

// armGet issues a raw ARM GET carrying the given Authorization header value
// (empty means no header) and returns the response, so the negative cases can
// assert the exact 401 shape the SDK would surface. It targets a subscription
// path (ARM-shaped) — the request never reaches a handler when the gate rejects
// it.
func armGet(t *testing.T, sess *compat.AzureSession, authHeader string) *http.Response {
	t.Helper()

	url := sess.Endpoint() + "/subscriptions/" + claimsAuthSubscriptionID + "?api-version=2022-12-01"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := sess.Transport().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	return resp
}

// assertInvalidToken asserts the response is a 401 InvalidAuthenticationToken
// with the bearer challenge header, the shape real Azure AD returns.
func assertInvalidToken(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate challenge header")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode error envelope %q: %v", string(body), err)
	}

	if envelope.Error.Code != "InvalidAuthenticationToken" {
		t.Fatalf("error code = %q, want InvalidAuthenticationToken", envelope.Error.Code)
	}
}

// TestCompatAzureClaimsAuthRejections covers every rejection path under
// enforcement: a missing header, a malformed token, a foreign audience, a token
// with no principal claim, and an expired token — each a 401
// InvalidAuthenticationToken.
func TestCompatAzureClaimsAuthRejections(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{IAM: provider.IAM, EnforceAuth: true})

	t.Run("NoAuthorizationHeader", func(t *testing.T) {
		assertInvalidToken(t, armGet(t, sess, ""))
	})

	t.Run("NotBearerScheme", func(t *testing.T) {
		assertInvalidToken(t, armGet(t, sess, "Basic dXNlcjpwYXNz"))
	})

	t.Run("MalformedToken", func(t *testing.T) {
		assertInvalidToken(t, armGet(t, sess, "Bearer not-a-jwt"))
	})

	t.Run("WrongAudience", func(t *testing.T) {
		claims := validClaims()
		claims["aud"] = "https://attacker.example.com"
		assertInvalidToken(t, armGet(t, sess, "Bearer "+makeJWT(t, claims)))
	})

	t.Run("NoPrincipalClaim", func(t *testing.T) {
		claims := validClaims()
		delete(claims, "oid")
		assertInvalidToken(t, armGet(t, sess, "Bearer "+makeJWT(t, claims)))
	})

	t.Run("ExpiredToken", func(t *testing.T) {
		claims := validClaims()
		claims["exp"] = float64(time.Now().Add(-time.Hour).Unix())
		assertInvalidToken(t, armGet(t, sess, "Bearer "+makeJWT(t, claims)))
	})
}

// TestCompatAzureClaimsAuthAbsentExpAccepted confirms the lenient expiry rule: a
// token with no "exp" claim is admitted (the response is anything but a 401),
// so tokens minted under a fixed clock without an expiry still authenticate.
func TestCompatAzureClaimsAuthAbsentExpAccepted(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{IAM: provider.IAM, EnforceAuth: true})

	claims := validClaims()
	delete(claims, "exp")

	resp := armGet(t, sess, "Bearer "+makeJWT(t, claims))
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("token without exp should be accepted, got 401")
	}
}
