package cognito_test

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	ciptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"

	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// authEnv is a full AWS wire server with an admin (signed) and a public
// (anonymous) Cognito client.
type authEnv struct {
	url    string
	admin  *cip.Client
	public *cip.Client
	cloud  *awsprovider.Provider
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	client := func(creds aws.CredentialsProvider) *cip.Client {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
			awsconfig.WithRegion("us-east-1"), awsconfig.WithCredentialsProvider(creds))
		if err != nil {
			t.Fatalf("aws config: %v", err)
		}

		return cip.NewFromConfig(cfg, func(o *cip.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	}

	return &authEnv{
		url:    ts.URL,
		admin:  client(credentials.NewStaticCredentialsProvider("test", "test", "")),
		public: client(aws.AnonymousCredentials{}),
		cloud:  cloud,
	}
}

// jwks fetches the pool's key set over HTTP, as a JWT library would.
func (e *authEnv) jwks(t *testing.T, poolID string) map[string]*rsa.PublicKey {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, e.url+"/"+poolID+"/.well-known/jwks.json", http.NoBody)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET jwks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET jwks = %d", resp.StatusCode)
	}

	var set struct {
		Keys []struct {
			Kid, Kty, Alg, Use, N, E string
		} `json:"keys"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		t.Fatalf("decode jwks: %v", err)
	}

	out := map[string]*rsa.PublicKey{}

	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Alg != "RS256" || k.Use != "sig" {
			t.Fatalf("unexpected JWK %+v", k)
		}

		n, _ := base64.RawURLEncoding.DecodeString(k.N)
		ex, _ := base64.RawURLEncoding.DecodeString(k.E)
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(ex).Int64())}
	}

	return out
}

// verify checks an RS256 JWT against a key set with crypto/rsa and returns
// its claims.
func verify(t *testing.T, keys map[string]*rsa.PublicKey, token string) map[string]any {
	t.Helper()

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments", len(parts))
	}

	var header struct{ Alg, Kid string }

	raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = json.Unmarshal(raw, &header)

	pub, ok := keys[header.Kid]
	if !ok || header.Alg != "RS256" {
		t.Fatalf("header %+v not in JWKS", header)
	}

	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))

	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}

	var claims map[string]any

	raw, _ = base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(raw, &claims)

	return claims
}

func TestSDKSignUpSignInFlow(t *testing.T) {
	ctx := context.Background()
	e := newAuthEnv(t)

	pool, err := e.admin.CreateUserPool(ctx, &cip.CreateUserPoolInput{
		PoolName:               aws.String("app"),
		AutoVerifiedAttributes: []ciptypes.VerifiedAttributeType{ciptypes.VerifiedAttributeTypeEmail},
	})
	if err != nil {
		t.Fatalf("CreateUserPool: %v", err)
	}

	poolID := aws.ToString(pool.UserPool.Id)

	client, err := e.admin.CreateUserPoolClient(ctx, &cip.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID), ClientName: aws.String("web"),
		ExplicitAuthFlows: []ciptypes.ExplicitAuthFlowsType{
			ciptypes.ExplicitAuthFlowsTypeAllowUserPasswordAuth, ciptypes.ExplicitAuthFlowsTypeAllowRefreshTokenAuth,
		},
	})
	if err != nil {
		t.Fatalf("CreateUserPoolClient: %v", err)
	}

	clientID := client.UserPoolClient.ClientId

	_, err = e.public.SignUp(ctx, &cip.SignUpInput{ClientId: clientID, Username: aws.String("alice"), Password: aws.String("short")})
	requireErrorCode(t, err, "InvalidPasswordException", "Password did not conform with policy: Password not long enough")

	su, err := e.public.SignUp(ctx, &cip.SignUpInput{
		ClientId: clientID, Username: aws.String("alice"), Password: aws.String("Passw0rd!"),
		UserAttributes: []ciptypes.AttributeType{{Name: aws.String("email"), Value: aws.String("alice@example.com")}},
	})
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	if su.UserConfirmed || su.CodeDeliveryDetails == nil || su.CodeDeliveryDetails.DeliveryMedium != ciptypes.DeliveryMediumTypeEmail {
		t.Fatalf("SignUp = %+v", su)
	}

	_, err = e.public.SignUp(ctx, &cip.SignUpInput{ClientId: clientID, Username: aws.String("alice"), Password: aws.String("Passw0rd!")})
	requireErrorCode(t, err, "UsernameExistsException", "User already exists")

	auth := &cip.InitiateAuthInput{
		ClientId: clientID, AuthFlow: ciptypes.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{"USERNAME": "alice", "PASSWORD": "Passw0rd!"},
	}

	_, err = e.public.InitiateAuth(ctx, auth)
	requireErrorCode(t, err, "UserNotConfirmedException", "User is not confirmed.")

	_, err = e.public.ConfirmSignUp(ctx, &cip.ConfirmSignUpInput{ClientId: clientID, Username: aws.String("alice"), ConfirmationCode: aws.String("x")})
	requireErrorCode(t, err, "CodeMismatchException", "")

	code, err := e.cloud.Cognito.ConfirmationCode(ctx, poolID, "alice")
	if err != nil {
		t.Fatalf("ConfirmationCode: %v", err)
	}

	if _, err = e.public.ConfirmSignUp(ctx, &cip.ConfirmSignUpInput{
		ClientId: clientID, Username: aws.String("alice"), ConfirmationCode: aws.String(code.Code),
	}); err != nil {
		t.Fatalf("ConfirmSignUp: %v", err)
	}

	if _, err = e.admin.CreateGroup(ctx, &cip.CreateGroupInput{UserPoolId: aws.String(poolID), GroupName: aws.String("admins")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if _, err = e.admin.AdminAddUserToGroup(ctx, &cip.AdminAddUserToGroupInput{
		UserPoolId: aws.String(poolID), Username: aws.String("alice"), GroupName: aws.String("admins"),
	}); err != nil {
		t.Fatalf("AdminAddUserToGroup: %v", err)
	}

	res, err := e.public.InitiateAuth(ctx, auth)
	if err != nil {
		t.Fatalf("InitiateAuth: %v", err)
	}

	ar := res.AuthenticationResult
	keys := e.jwks(t, poolID)
	id := verify(t, keys, aws.ToString(ar.IdToken))
	access := verify(t, keys, aws.ToString(ar.AccessToken))

	if id["iss"] != "https://cognito-idp.us-east-1.amazonaws.com/"+poolID || id["aud"] != aws.ToString(clientID) ||
		id["token_use"] != "id" || id["email"] != "alice@example.com" || access["token_use"] != "access" {
		t.Fatalf("claims id=%v access=%v", id, access)
	}

	if groups, _ := access["cognito:groups"].([]any); len(groups) != 1 || groups[0] != "admins" {
		t.Fatalf("cognito:groups = %v", access["cognito:groups"])
	}

	u, err := e.public.GetUser(ctx, &cip.GetUserInput{AccessToken: ar.AccessToken})
	if err != nil || aws.ToString(u.Username) != "alice" {
		t.Fatalf("GetUser = %+v, %v", u, err)
	}

	ref, err := e.public.InitiateAuth(ctx, &cip.InitiateAuthInput{
		ClientId: clientID, AuthFlow: ciptypes.AuthFlowTypeRefreshTokenAuth,
		AuthParameters: map[string]string{"REFRESH_TOKEN": aws.ToString(ar.RefreshToken)},
	})
	if err != nil || ref.AuthenticationResult.RefreshToken != nil {
		t.Fatalf("refresh = %+v, %v", ref, err)
	}

	if _, err = e.public.GlobalSignOut(ctx, &cip.GlobalSignOutInput{AccessToken: ar.AccessToken}); err != nil {
		t.Fatalf("GlobalSignOut: %v", err)
	}

	_, err = e.public.GetUser(ctx, &cip.GetUserInput{AccessToken: ar.AccessToken})
	requireErrorCode(t, err, "NotAuthorizedException", "Access Token has been revoked")

	_, err = e.public.InitiateAuth(ctx, &cip.InitiateAuthInput{
		ClientId: clientID, AuthFlow: ciptypes.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{"USERNAME": "alice", "PASSWORD": "Wr0ng!pass"},
	})
	requireErrorCode(t, err, "NotAuthorizedException", "Incorrect username or password.")
}

func TestSDKGroupsAndAdminAuth(t *testing.T) {
	ctx := context.Background()
	e := newAuthEnv(t)
	poolID := createPool(t, e.admin, "groups")

	g, err := e.admin.CreateGroup(ctx, &cip.CreateGroupInput{
		UserPoolId: aws.String(poolID), GroupName: aws.String("ops"), Description: aws.String("Ops"), Precedence: aws.Int32(2),
	})
	if err != nil || aws.ToInt32(g.Group.Precedence) != 2 || g.Group.CreationDate == nil {
		t.Fatalf("CreateGroup = %+v, %v", g, err)
	}

	_, err = e.admin.CreateGroup(ctx, &cip.CreateGroupInput{UserPoolId: aws.String(poolID), GroupName: aws.String("ops")})
	requireErrorCode(t, err, "GroupExistsException", "A group with the name ops already exists.")

	if _, err = e.admin.UpdateGroup(ctx, &cip.UpdateGroupInput{
		UserPoolId: aws.String(poolID), GroupName: aws.String("ops"), Description: aws.String("Operations"),
	}); err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}

	got, err := e.admin.GetGroup(ctx, &cip.GetGroupInput{UserPoolId: aws.String(poolID), GroupName: aws.String("ops")})
	if err != nil || aws.ToString(got.Group.Description) != "Operations" || aws.ToInt32(got.Group.Precedence) != 2 {
		t.Fatalf("GetGroup = %+v, %v", got, err)
	}

	client, err := e.admin.CreateUserPoolClient(ctx, &cip.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID), ClientName: aws.String("srv"),
		ExplicitAuthFlows: []ciptypes.ExplicitAuthFlowsType{ciptypes.ExplicitAuthFlowsTypeAllowAdminUserPasswordAuth},
	})
	if err != nil {
		t.Fatalf("CreateUserPoolClient: %v", err)
	}

	if _, err = e.admin.AdminCreateUser(ctx, &cip.AdminCreateUserInput{
		UserPoolId: aws.String(poolID), Username: aws.String("bob"), TemporaryPassword: aws.String("Temp0rary!"),
		MessageAction: ciptypes.MessageActionTypeSuppress,
	}); err != nil {
		t.Fatalf("AdminCreateUser: %v", err)
	}

	if _, err = e.admin.AdminAddUserToGroup(ctx, &cip.AdminAddUserToGroupInput{
		UserPoolId: aws.String(poolID), Username: aws.String("bob"), GroupName: aws.String("ops"),
	}); err != nil {
		t.Fatalf("AdminAddUserToGroup: %v", err)
	}

	members, err := e.admin.ListUsersInGroup(ctx, &cip.ListUsersInGroupInput{UserPoolId: aws.String(poolID), GroupName: aws.String("ops")})
	if err != nil || len(members.Users) != 1 || aws.ToString(members.Users[0].Username) != "bob" {
		t.Fatalf("ListUsersInGroup = %+v, %v", members, err)
	}

	start, err := e.admin.AdminInitiateAuth(ctx, &cip.AdminInitiateAuthInput{
		UserPoolId: aws.String(poolID), ClientId: client.UserPoolClient.ClientId, AuthFlow: ciptypes.AuthFlowTypeAdminUserPasswordAuth,
		AuthParameters: map[string]string{"USERNAME": "bob", "PASSWORD": "Temp0rary!"},
	})
	if err != nil || start.ChallengeName != ciptypes.ChallengeNameTypeNewPasswordRequired {
		t.Fatalf("AdminInitiateAuth = %+v, %v", start, err)
	}

	done, err := e.admin.AdminRespondToAuthChallenge(ctx, &cip.AdminRespondToAuthChallengeInput{
		UserPoolId: aws.String(poolID), ClientId: client.UserPoolClient.ClientId,
		ChallengeName: ciptypes.ChallengeNameTypeNewPasswordRequired, Session: start.Session,
		ChallengeResponses: map[string]string{"USERNAME": "bob", "NEW_PASSWORD": "N3wPassword!"},
	})
	if err != nil || done.AuthenticationResult == nil {
		t.Fatalf("AdminRespondToAuthChallenge = %+v, %v", done, err)
	}

	groups, err := e.admin.AdminListGroupsForUser(ctx, &cip.AdminListGroupsForUserInput{
		UserPoolId: aws.String(poolID), Username: aws.String("bob"),
	})
	if err != nil || len(groups.Groups) != 1 {
		t.Fatalf("AdminListGroupsForUser = %+v, %v", groups, err)
	}

	if _, err = e.admin.DeleteGroup(ctx, &cip.DeleteGroupInput{UserPoolId: aws.String(poolID), GroupName: aws.String("ops")}); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	list, err := e.admin.ListGroups(ctx, &cip.ListGroupsInput{UserPoolId: aws.String(poolID)})
	if err != nil || len(list.Groups) != 0 {
		t.Fatalf("ListGroups = %+v, %v", list, err)
	}
}

func TestOpenIDConfiguration(t *testing.T) {
	e := newAuthEnv(t)
	poolID := createPool(t, e.admin, "oidc")

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		e.url+"/"+poolID+"/.well-known/openid-configuration", http.NoBody)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var doc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&doc)

	if doc["issuer"] != "https://cognito-idp.us-east-1.amazonaws.com/"+poolID ||
		doc["jwks_uri"] != e.url+"/"+poolID+"/.well-known/jwks.json" {
		t.Fatalf("openid-configuration = %v", doc)
	}

	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet,
		e.url+"/us-east-1_nope12345/.well-known/jwks.json", http.NoBody)

	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown pool jwks = %d, want 404", resp2.StatusCode)
	}
}
