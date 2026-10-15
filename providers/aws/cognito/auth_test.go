package cognito

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// verifyJWT checks an RS256 token against the pool's published JWKS with
// crypto/rsa only, independent of the signer, and returns the header kid and
// the claims.
func verifyJWT(t *testing.T, m *Mock, poolID, token string) (string, map[string]any) {
	t.Helper()

	_, set, err := m.SigningKeys(context.Background(), poolID)
	requireNoError(t, err, "SigningKeys")

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments", len(parts))
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}

	decodeSegment(t, parts[0], &header)

	if header.Alg != "RS256" {
		t.Fatalf("alg = %q", header.Alg)
	}

	var pub *rsa.PublicKey

	for _, k := range set.Keys {
		if k.Kid == header.Kid {
			n, _ := base64.RawURLEncoding.DecodeString(k.N)
			e, _ := base64.RawURLEncoding.DecodeString(k.E)
			pub = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		}
	}

	if pub == nil {
		t.Fatalf("kid %q not in JWKS", header.Kid)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	requireNoError(t, err, "signature encoding")

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature does not verify against JWKS: %v", err)
	}

	var claims map[string]any

	decodeSegment(t, parts[1], &claims)

	return header.Kid, claims
}

func decodeSegment(t *testing.T, seg string, v any) {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(seg)
	requireNoError(t, err, "segment encoding")
	requireNoError(t, json.Unmarshal(raw, v), "segment json")
}

// confirmedUser signs up and confirms a user with an email, returning its sub.
func confirmedUser(t *testing.T, m *Mock, poolID, clientID, username string) string {
	t.Helper()

	ctx := context.Background()

	out, err := m.SignUp(ctx, signUpInput(clientID, username, testPassword, emailAttr(username+"@example.com")))
	requireNoError(t, err, "SignUp")

	requireNoError(t, m.ConfirmSignUp(ctx, driver.ConfirmSignUpInput{
		ClientUserInput:  driver.ClientUserInput{ClientID: clientID, Username: username},
		ConfirmationCode: mustCode(t, m, poolID, username),
	}), "ConfirmSignUp")

	return out.UserSub
}

func passwordAuth(clientID, username, password string) driver.InitiateAuthInput {
	return driver.InitiateAuthInput{
		ClientID: clientID, AuthFlow: driver.AuthFlowUserPassword,
		AuthParameters: map[string]string{"USERNAME": username, "PASSWORD": password},
	}
}

func TestInitiateAuthIssuesSignedTokens(t *testing.T) {
	m, fc := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	sub := confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	_, err := m.CreateGroup(ctx, driver.CreateGroupInput{UserPoolID: pool.ID, GroupName: "readers", Precedence: int32Ptr(5)})
	requireNoError(t, err, "CreateGroup")
	_, err = m.CreateGroup(ctx, driver.CreateGroupInput{UserPoolID: pool.ID, GroupName: "admins", Precedence: int32Ptr(1)})
	requireNoError(t, err, "CreateGroup")
	requireNoError(t, m.AdminAddUserToGroup(ctx, pool.ID, "alice", "readers"), "add")
	requireNoError(t, m.AdminAddUserToGroup(ctx, pool.ID, "alice", "admins"), "add")

	res, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "InitiateAuth")

	ar := res.AuthenticationResult
	if res.ChallengeName != "" || ar == nil || ar.TokenType != "Bearer" || ar.ExpiresIn != 3600 || ar.RefreshToken == "" {
		t.Fatalf("result = %+v / %+v", res, ar)
	}

	if n := len(strings.Split(ar.RefreshToken, ".")); n != 5 {
		t.Fatalf("refresh token has %d segments, want 5 (JWE shape)", n)
	}

	iss := "https://cognito-idp.us-east-1.amazonaws.com/" + pool.ID
	now := fc.Now().Unix()

	idKid, id := verifyJWT(t, m, pool.ID, ar.IDToken)
	accessKid, access := verifyJWT(t, m, pool.ID, ar.AccessToken)

	if idKid == accessKid {
		t.Fatal("ID and access tokens must be signed with different keys")
	}

	wantID := map[string]any{
		"iss": iss, "sub": sub, "aud": client.ClientID, "token_use": "id", "cognito:username": "alice",
		"email": "alice@example.com", "email_verified": true,
		"auth_time": float64(now), "iat": float64(now), "exp": float64(now + 3600),
	}
	assertClaims(t, "id", id, wantID)

	wantAccess := map[string]any{
		"iss": iss, "sub": sub, "client_id": client.ClientID, "token_use": "access", "username": "alice",
		"scope": "aws.cognito.signin.user.admin", "auth_time": float64(now), "exp": float64(now + 3600),
	}
	assertClaims(t, "access", access, wantAccess)

	for _, c := range []map[string]any{id, access} {
		groups, _ := c["cognito:groups"].([]any)
		if len(groups) != 2 || groups[0] != "admins" || groups[1] != "readers" {
			t.Fatalf("cognito:groups = %v, want precedence order [admins readers]", c["cognito:groups"])
		}

		if c["origin_jti"] == "" || c["jti"] == "" || c["event_id"] == "" {
			t.Fatalf("missing jti/origin_jti/event_id in %v", c)
		}
	}

	if id["origin_jti"] != access["origin_jti"] || id["jti"] == access["jti"] {
		t.Fatal("ID and access tokens share origin_jti but not jti")
	}

	if _, ok := access["aud"]; ok {
		t.Fatal("access token carries no aud")
	}

	u, err := m.GetUser(ctx, ar.AccessToken)
	requireNoError(t, err, "GetUser")

	if u.Username != "alice" || attrValue(u.Attributes, "sub") != sub {
		t.Fatalf("GetUser = %+v", u)
	}

	_, err = m.GetUser(ctx, ar.IDToken)
	assertException(t, err, driver.ExNotAuthorized, "Invalid Access Token")
}

func assertClaims(t *testing.T, name string, got, want map[string]any) {
	t.Helper()

	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s token %s = %v (%T), want %v", name, k, got[k], got[k], v)
		}
	}
}

func TestInitiateAuthErrors(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	_, err := m.SignUp(ctx, signUpInput(client.ClientID, "pending", testPassword))
	requireNoError(t, err, "SignUp pending")

	defaultClient := mustCreateClient(t, m, pool.ID, false)

	cases := []struct {
		name      string
		in        driver.InitiateAuthInput
		exception string
		msg       string
	}{
		{"wrong password", passwordAuth(client.ClientID, "alice", "Wr0ng!pass"), driver.ExNotAuthorized, "Incorrect username or password."},
		{"unknown user legacy", passwordAuth(client.ClientID, "ghost", testPassword), driver.ExUserNotFound, "User does not exist."},
		{"unconfirmed", passwordAuth(client.ClientID, "pending", testPassword), driver.ExUserNotConfirmed, "User is not confirmed."},
		{"flow not enabled", passwordAuth(defaultClient.ClientID, "alice", testPassword),
			driver.ExInvalidParameter, "USER_PASSWORD_AUTH flow not enabled for this client"},
		{"admin flow on public api", driver.InitiateAuthInput{ClientID: client.ClientID, AuthFlow: driver.AuthFlowAdminUserPassword},
			driver.ExInvalidParameter, "Initiate Auth method not supported."},
		{"unknown client", passwordAuth("nosuchclient", "alice", testPassword), driver.ExResourceNotFound, ""},
		{"missing password", driver.InitiateAuthInput{
			ClientID: client.ClientID, AuthFlow: driver.AuthFlowUserPassword, AuthParameters: map[string]string{"USERNAME": "alice"},
		}, driver.ExInvalidParameter, "Missing required parameter PASSWORD"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.InitiateAuth(ctx, tc.in)
			assertException(t, err, tc.exception, tc.msg)
		})
	}

	requireNoError(t, m.AdminDisableUser(ctx, pool.ID, "alice"), "AdminDisableUser")

	_, err = m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	assertException(t, err, driver.ExNotAuthorized, "User is disabled.")
}

func TestPreventUserExistenceErrorsEnabled(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)

	client, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID: pool.ID, ClientName: "hidden", ExplicitAuthFlows: passwordFlows, PreventUserExistenceErrors: "ENABLED",
	})
	requireNoError(t, err, "CreateUserPoolClient")

	_, err = m.InitiateAuth(ctx, passwordAuth(client.ClientID, "ghost", testPassword))
	assertException(t, err, driver.ExNotAuthorized, "Incorrect username or password.")
}

func TestInitiateAuthSecretHash(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, true, passwordFlows...)

	in := signUpInput(client.ClientID, "alice", testPassword)
	in.SecretHash = testSecretHash(client.ClientSecret, "alice", client.ClientID)
	_, err := m.SignUp(ctx, in)
	requireNoError(t, err, "SignUp")
	requireNoError(t, m.AdminConfirmSignUp(ctx, pool.ID, "alice"), "AdminConfirmSignUp")

	auth := passwordAuth(client.ClientID, "alice", testPassword)

	_, err = m.InitiateAuth(ctx, auth)
	assertException(t, err, driver.ExNotAuthorized, "Client "+client.ClientID+" is configured for secret but secret was not received")

	auth.AuthParameters["SECRET_HASH"] = "bm9wZQ=="
	_, err = m.InitiateAuth(ctx, auth)
	assertException(t, err, driver.ExNotAuthorized, "Unable to verify secret hash for client "+client.ClientID)

	auth.AuthParameters["SECRET_HASH"] = testSecretHash(client.ClientSecret, "alice", client.ClientID)
	res, err := m.InitiateAuth(ctx, auth)
	requireNoError(t, err, "InitiateAuth with SECRET_HASH")

	refresh := driver.InitiateAuthInput{
		ClientID: client.ClientID, AuthFlow: driver.AuthFlowRefreshTokenAuth,
		AuthParameters: map[string]string{"REFRESH_TOKEN": res.AuthenticationResult.RefreshToken},
	}

	_, err = m.InitiateAuth(ctx, refresh)
	assertException(t, err, driver.ExNotAuthorized, "Client "+client.ClientID+" is configured for secret but secret was not received")

	refresh.AuthParameters["SECRET_HASH"] = testSecretHash(client.ClientSecret, "alice", client.ClientID)
	_, err = m.InitiateAuth(ctx, refresh)
	requireNoError(t, err, "refresh with SECRET_HASH")
}

func TestRefreshTokenAuth(t *testing.T) {
	m, fc := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	first, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "InitiateAuth")

	authTime := fc.Now().Unix()

	fc.Advance(10 * time.Minute)

	for _, flow := range []string{driver.AuthFlowRefreshTokenAuth, driver.AuthFlowRefreshToken} {
		res, err := m.InitiateAuth(ctx, driver.InitiateAuthInput{
			ClientID: client.ClientID, AuthFlow: flow,
			AuthParameters: map[string]string{"REFRESH_TOKEN": first.AuthenticationResult.RefreshToken},
		})
		requireNoError(t, err, "refresh "+flow)

		ar := res.AuthenticationResult
		if ar.RefreshToken != "" || ar.AccessToken == "" || ar.IDToken == "" {
			t.Fatalf("refresh result = %+v, want new access/id and no refresh token", ar)
		}

		_, claims := verifyJWT(t, m, pool.ID, ar.AccessToken)
		_, orig := verifyJWT(t, m, pool.ID, first.AuthenticationResult.AccessToken)

		if claims["auth_time"] != float64(authTime) || claims["origin_jti"] != orig["origin_jti"] {
			t.Fatalf("refreshed claims keep auth_time and origin_jti: %v vs %v", claims, orig)
		}
	}

	_, err = m.InitiateAuth(ctx, driver.InitiateAuthInput{
		ClientID: client.ClientID, AuthFlow: driver.AuthFlowRefreshTokenAuth,
		AuthParameters: map[string]string{"REFRESH_TOKEN": "garbage"},
	})
	assertException(t, err, driver.ExNotAuthorized, "Invalid Refresh Token")

	other := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	_, err = m.InitiateAuth(ctx, driver.InitiateAuthInput{
		ClientID: other.ClientID, AuthFlow: driver.AuthFlowRefreshTokenAuth,
		AuthParameters: map[string]string{"REFRESH_TOKEN": first.AuthenticationResult.RefreshToken},
	})
	assertException(t, err, driver.ExNotAuthorized, "Invalid Refresh Token")

	fc.Advance(31 * 24 * time.Hour)

	_, err = m.InitiateAuth(ctx, driver.InitiateAuthInput{
		ClientID: client.ClientID, AuthFlow: driver.AuthFlowRefreshTokenAuth,
		AuthParameters: map[string]string{"REFRESH_TOKEN": first.AuthenticationResult.RefreshToken},
	})
	assertException(t, err, driver.ExNotAuthorized, "Refresh Token has expired")
}

func TestAccessTokenExpiry(t *testing.T) {
	m, fc := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)

	client, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID: pool.ID, ClientName: "short", ExplicitAuthFlows: passwordFlows,
		AccessTokenValidity: int32Ptr(5), IDTokenValidity: int32Ptr(10),
		TokenValidityUnits: &driver.TokenValidityUnits{AccessToken: "minutes", IDToken: "minutes", RefreshToken: "days"},
	})
	requireNoError(t, err, "CreateUserPoolClient")
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	res, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "InitiateAuth")

	if res.AuthenticationResult.ExpiresIn != 300 {
		t.Fatalf("ExpiresIn = %d, want 300", res.AuthenticationResult.ExpiresIn)
	}

	_, id := verifyJWT(t, m, pool.ID, res.AuthenticationResult.IDToken)
	if id["exp"] != float64(fc.Now().Unix()+600) {
		t.Fatalf("id exp = %v", id["exp"])
	}

	fc.Advance(6 * time.Minute)

	_, err = m.GetUser(ctx, res.AuthenticationResult.AccessToken)
	assertException(t, err, driver.ExNotAuthorized, "Access Token has expired")
}

func TestAccessTokenTamperRejected(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")
	confirmedUser(t, m, pool.ID, client.ClientID, "bob")

	res, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "InitiateAuth")

	parts := strings.Split(res.AuthenticationResult.AccessToken, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	forged := strings.Replace(string(payload), `"username":"alice"`, `"username":"bob"`, 1)
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(forged))

	for _, tok := range []string{strings.Join(parts, "."), "not-a-jwt", ""} {
		_, err = m.GetUser(ctx, tok)
		assertException(t, err, driver.ExNotAuthorized, "Invalid Access Token")
	}
}

func TestNewPasswordRequiredChallenge(t *testing.T) {
	m, fc := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)

	_, err := m.AdminCreateUser(ctx, driver.AdminCreateUserInput{
		UserPoolID: pool.ID, Username: "temp", TemporaryPassword: "Temp0rary!", MessageAction: driver.MessageActionSuppress,
		UserAttributes: []driver.Attribute{emailAttr("temp@example.com")},
	})
	requireNoError(t, err, "AdminCreateUser")

	res, err := m.AdminInitiateAuth(ctx, driver.InitiateAuthInput{
		UserPoolID: pool.ID, ClientID: client.ClientID, AuthFlow: driver.AuthFlowAdminUserPassword,
		AuthParameters: map[string]string{"USERNAME": "temp", "PASSWORD": "Temp0rary!"},
	})
	requireNoError(t, err, "AdminInitiateAuth")

	if res.ChallengeName != driver.ChallengeNewPasswordRequired || res.Session == "" || res.AuthenticationResult != nil {
		t.Fatalf("result = %+v", res)
	}

	if res.ChallengeParameters["USER_ID_FOR_SRP"] != "temp" || res.ChallengeParameters["requiredAttributes"] != "[]" {
		t.Fatalf("challenge parameters = %v", res.ChallengeParameters)
	}

	var userAttrs map[string]string
	requireNoError(t, json.Unmarshal([]byte(res.ChallengeParameters["userAttributes"]), &userAttrs), "userAttributes json")

	if userAttrs["email"] != "temp@example.com" {
		t.Fatalf("userAttributes = %v", userAttrs)
	}

	respond := driver.RespondToAuthChallengeInput{
		ClientID: client.ClientID, ChallengeName: driver.ChallengeNewPasswordRequired, Session: res.Session,
		ChallengeResponses: map[string]string{"USERNAME": "temp", "NEW_PASSWORD": "short"},
	}

	_, err = m.RespondToAuthChallenge(ctx, respond)
	assertException(t, err, driver.ExInvalidPassword, "Password did not conform with policy: Password not long enough")

	_, err = m.RespondToAuthChallenge(ctx, driver.RespondToAuthChallengeInput{
		ClientID: client.ClientID, ChallengeName: driver.ChallengeNewPasswordRequired, Session: "bogus",
		ChallengeResponses: map[string]string{"USERNAME": "temp", "NEW_PASSWORD": "N3wPassword!"},
	})
	assertException(t, err, driver.ExNotAuthorized, "Invalid session for the user.")

	respond.ChallengeResponses["NEW_PASSWORD"] = "N3wPassword!"
	out, err := m.RespondToAuthChallenge(ctx, respond)
	requireNoError(t, err, "RespondToAuthChallenge")

	if out.AuthenticationResult == nil || out.AuthenticationResult.RefreshToken == "" {
		t.Fatalf("respond result = %+v", out)
	}

	u, _ := m.AdminGetUser(ctx, pool.ID, "temp")
	if u.UserStatus != driver.UserStatusConfirmed {
		t.Fatalf("status = %s, want CONFIRMED", u.UserStatus)
	}

	_, err = m.InitiateAuth(ctx, passwordAuth(client.ClientID, "temp", "N3wPassword!"))
	requireNoError(t, err, "sign in with the new password")

	// A session cannot be reused, and expires after AuthSessionValidity minutes.
	_, err = m.RespondToAuthChallenge(ctx, respond)
	assertException(t, err, driver.ExNotAuthorized, "Invalid session for the user.")

	requireNoError(t, m.AdminSetUserPassword(ctx, pool.ID, "temp", "Temp0rary!", false), "AdminSetUserPassword")

	res, err = m.InitiateAuth(ctx, passwordAuth(client.ClientID, "temp", "Temp0rary!"))
	requireNoError(t, err, "InitiateAuth after reset")

	fc.Advance(4 * time.Minute)

	respond.Session = res.Session
	_, err = m.RespondToAuthChallenge(ctx, respond)
	assertException(t, err, driver.ExNotAuthorized, "Invalid session for the user, session is expired.")
}

func TestAdminAuthFlows(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, "ALLOW_USER_PASSWORD_AUTH", "ALLOW_REFRESH_TOKEN_AUTH")
	adminClient := mustCreateClient(t, m, pool.ID, false, "ADMIN_NO_SRP_AUTH")
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	admin := func(clientID, flow string) driver.InitiateAuthInput {
		return driver.InitiateAuthInput{
			UserPoolID: pool.ID, ClientID: clientID, AuthFlow: flow,
			AuthParameters: map[string]string{"USERNAME": "alice", "PASSWORD": testPassword},
		}
	}

	_, err := m.AdminInitiateAuth(ctx, admin(client.ClientID, driver.AuthFlowAdminUserPassword))
	assertException(t, err, driver.ExInvalidParameter, "Auth flow not enabled for this client")

	for _, flow := range []string{driver.AuthFlowAdminUserPassword, driver.AuthFlowAdminNoSRP} {
		_, err = m.AdminInitiateAuth(ctx, admin(adminClient.ClientID, flow))
		requireNoError(t, err, "AdminInitiateAuth "+flow)
	}

	_, err = m.AdminInitiateAuth(ctx, admin(adminClient.ClientID, driver.AuthFlowUserPassword))
	assertException(t, err, driver.ExInvalidParameter, "Initiate Auth method not supported.")

	in := admin(adminClient.ClientID, driver.AuthFlowAdminUserPassword)
	in.UserPoolID = "us-east-1_other0000"
	_, err = m.AdminInitiateAuth(ctx, in)
	assertException(t, err, driver.ExResourceNotFound, "")
}

func TestGlobalSignOutRevokesTokens(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	a, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "sign-in 1")
	b, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "sign-in 2")

	requireNoError(t, m.GlobalSignOut(ctx, a.AuthenticationResult.AccessToken), "GlobalSignOut")

	for _, tok := range []string{a.AuthenticationResult.AccessToken, b.AuthenticationResult.AccessToken} {
		_, err = m.GetUser(ctx, tok)
		assertException(t, err, driver.ExNotAuthorized, "Access Token has been revoked")
	}

	_, err = m.InitiateAuth(ctx, driver.InitiateAuthInput{
		ClientID: client.ClientID, AuthFlow: driver.AuthFlowRefreshTokenAuth,
		AuthParameters: map[string]string{"REFRESH_TOKEN": b.AuthenticationResult.RefreshToken},
	})
	assertException(t, err, driver.ExNotAuthorized, "Refresh Token has been revoked")

	c, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "sign-in after sign-out")

	_, err = m.GetUser(ctx, c.AuthenticationResult.AccessToken)
	requireNoError(t, err, "a fresh sign-in works")

	requireNoError(t, m.AdminUserGlobalSignOut(ctx, pool.ID, "alice"), "AdminUserGlobalSignOut")

	_, err = m.GetUser(ctx, c.AuthenticationResult.AccessToken)
	assertException(t, err, driver.ExNotAuthorized, "Access Token has been revoked")
}

func TestRevokeToken(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	a, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "sign-in 1")
	b, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "sign-in 2")

	err = m.RevokeToken(ctx, driver.RevokeTokenInput{Token: a.AuthenticationResult.AccessToken, ClientID: client.ClientID})
	assertException(t, err, driver.ExUnsupportedTokenType, "")

	requireNoError(t, m.RevokeToken(ctx, driver.RevokeTokenInput{
		Token: a.AuthenticationResult.RefreshToken, ClientID: client.ClientID,
	}), "RevokeToken")

	_, err = m.GetUser(ctx, a.AuthenticationResult.AccessToken)
	assertException(t, err, driver.ExNotAuthorized, "Access Token has been revoked")

	_, err = m.GetUser(ctx, b.AuthenticationResult.AccessToken)
	requireNoError(t, err, "the other session is untouched")

	noRevoke := false
	locked, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID: pool.ID, ClientName: "norevoke", ExplicitAuthFlows: passwordFlows, EnableTokenRevocation: &noRevoke,
	})
	requireNoError(t, err, "CreateUserPoolClient")

	c, err := m.InitiateAuth(ctx, passwordAuth(locked.ClientID, "alice", testPassword))
	requireNoError(t, err, "sign-in on norevoke client")

	err = m.RevokeToken(ctx, driver.RevokeTokenInput{Token: c.AuthenticationResult.RefreshToken, ClientID: locked.ClientID})
	assertException(t, err, driver.ExUnsupportedOperation, "")
}

func TestSigningKeysSurviveSnapshot(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	res, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "InitiateAuth")

	issuer, before, err := m.SigningKeys(ctx, pool.ID)
	requireNoError(t, err, "SigningKeys")

	if issuer != "https://cognito-idp.us-east-1.amazonaws.com/"+pool.ID || len(before.Keys) != 2 {
		t.Fatalf("issuer=%q keys=%d", issuer, len(before.Keys))
	}

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err, "Snapshot")

	if strings.Contains(string(snap), testPassword) {
		t.Fatal("snapshot carries a plaintext password")
	}

	restored, _ := newClockMock(t)
	requireNoError(t, restored.Restore(ctx, snap), "Restore")

	_, after, err := restored.SigningKeys(ctx, pool.ID)
	requireNoError(t, err, "SigningKeys after restore")

	b1, _ := json.Marshal(before)
	b2, _ := json.Marshal(after)

	if string(b1) != string(b2) {
		t.Fatal("JWKS changed across snapshot/restore")
	}

	_, err = restored.GetUser(ctx, res.AuthenticationResult.AccessToken)
	requireNoError(t, err, "access token still valid after restore")

	_, err = restored.InitiateAuth(ctx, driver.InitiateAuthInput{
		ClientID: client.ClientID, AuthFlow: driver.AuthFlowRefreshTokenAuth,
		AuthParameters: map[string]string{"REFRESH_TOKEN": res.AuthenticationResult.RefreshToken},
	})
	requireNoError(t, err, "refresh token still valid after restore")

	_, _, err = m.SigningKeys(ctx, "us-east-1_missing00")
	assertException(t, err, driver.ExResourceNotFound, "")
}

func TestDeleteUserInvalidatesTokens(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	res, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "InitiateAuth")

	requireNoError(t, m.AdminDeleteUser(ctx, pool.ID, "alice"), "AdminDeleteUser")

	_, err = m.GetUser(ctx, res.AuthenticationResult.AccessToken)
	assertException(t, err, driver.ExNotAuthorized, "Access Token has been revoked")

	// A new user with the same name must not inherit the old user's tokens.
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	_, err = m.GetUser(ctx, res.AuthenticationResult.AccessToken)
	assertException(t, err, driver.ExNotAuthorized, "Access Token has been revoked")
}
