package cognito

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Flows a test client enables for password sign-in.
//
//nolint:gochecknoglobals // test fixture
var passwordFlows = []string{"ALLOW_USER_PASSWORD_AUTH", "ALLOW_ADMIN_USER_PASSWORD_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"}

const testPassword = "Passw0rd!"

func newClockMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(fc))), fc
}

func mustCreateEmailPool(t *testing.T, m *Mock) *driver.UserPool {
	t.Helper()

	pool, err := m.CreateUserPool(context.Background(), driver.CreateUserPoolInput{
		Name: "signup", AutoVerifiedAttributes: []string{"email"},
	})
	requireNoError(t, err, "CreateUserPool")

	return pool
}

func mustCreateClient(t *testing.T, m *Mock, poolID string, secret bool, flows ...string) *driver.UserPoolClient {
	t.Helper()

	c, err := m.CreateUserPoolClient(context.Background(), driver.CreateUserPoolClientInput{
		UserPoolID: poolID, ClientName: "app", GenerateSecret: secret, ExplicitAuthFlows: flows,
	})
	requireNoError(t, err, "CreateUserPoolClient")

	return c
}

// testSecretHash computes SECRET_HASH the way the AWS docs specify it, without
// the provider's helper.
func testSecretHash(secret, username, clientID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(username + clientID))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func signUpInput(clientID, username, password string, attrs ...driver.Attribute) driver.SignUpInput {
	return driver.SignUpInput{
		ClientUserInput: driver.ClientUserInput{ClientID: clientID, Username: username},
		Password:        password,
		UserAttributes:  attrs,
	}
}

func emailAttr(v string) driver.Attribute { return driver.Attribute{Name: "email", Value: v} }

func mustCode(t *testing.T, m *Mock, poolID, username string) string {
	t.Helper()

	c, err := m.ConfirmationCode(context.Background(), poolID, username)
	requireNoError(t, err, "ConfirmationCode")

	return c.Code
}

func TestSignUpAndConfirm(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)

	out, err := m.SignUp(ctx, signUpInput(client.ClientID, "alice", testPassword, emailAttr("alice@example.com")))
	requireNoError(t, err, "SignUp")

	if out.UserConfirmed || len(out.UserSub) != 36 {
		t.Fatalf("SignUp = %+v", out)
	}

	d := out.CodeDeliveryDetails
	if d == nil || d.DeliveryMedium != driver.DeliveryMediumEmail || d.AttributeName != "email" || d.Destination != "a***@e***" {
		t.Fatalf("CodeDeliveryDetails = %+v", d)
	}

	u, err := m.AdminGetUser(ctx, pool.ID, "alice")
	requireNoError(t, err, "AdminGetUser")

	if u.UserStatus != driver.UserStatusUnconfirmed || attrValue(u.Attributes, "sub") != out.UserSub {
		t.Fatalf("user = %+v", u)
	}

	code := mustCode(t, m, pool.ID, "alice")
	if len(code) != 6 {
		t.Fatalf("code %q is not 6 digits", code)
	}

	bad := driver.ConfirmSignUpInput{ClientUserInput: driver.ClientUserInput{ClientID: client.ClientID, Username: "alice"}}

	bad.ConfirmationCode = wrongCode(code)
	assertException(t, m.ConfirmSignUp(ctx, bad), driver.ExCodeMismatch, "Invalid verification code provided, please try again.")

	bad.ConfirmationCode = code
	requireNoError(t, m.ConfirmSignUp(ctx, bad), "ConfirmSignUp")

	u, _ = m.AdminGetUser(ctx, pool.ID, "alice")
	if u.UserStatus != driver.UserStatusConfirmed || attrValue(u.Attributes, "email_verified") != "true" {
		t.Fatalf("confirmed user = %+v", u)
	}

	assertException(t, m.ConfirmSignUp(ctx, bad), driver.ExNotAuthorized, "User cannot be confirmed. Current status is CONFIRMED")

	_, err = m.ResendConfirmationCode(ctx, bad.ClientUserInput)
	assertException(t, err, driver.ExInvalidParameter, "User is already confirmed.")
}

func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}

	return "000000"
}

func TestSignUpErrors(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false)

	_, err := m.SignUp(ctx, signUpInput(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "SignUp")

	cases := []struct {
		name      string
		in        driver.SignUpInput
		exception string
		msg       string
	}{
		{"duplicate", signUpInput(client.ClientID, "alice", testPassword), driver.ExUsernameExists, "User already exists"},
		{"short password", signUpInput(client.ClientID, "bob", "Sh0rt!"),
			driver.ExInvalidPassword, "Password did not conform with policy: Password not long enough"},
		{"no symbol", signUpInput(client.ClientID, "bob", "Passw0rdxx"),
			driver.ExInvalidPassword, "Password did not conform with policy: Password must have symbol characters"},
		{"missing password", signUpInput(client.ClientID, "bob", ""), driver.ExInvalidParameter, ""},
		{"unknown client", signUpInput("nosuchclient", "bob", testPassword), driver.ExResourceNotFound, ""},
		{"bad attribute", signUpInput(client.ClientID, "bob", testPassword, driver.Attribute{Name: "custom:nope", Value: "x"}),
			driver.ExInvalidParameter, "Attributes did not conform to the schema: custom:nope: Attribute does not exist in the schema."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.SignUp(ctx, tc.in)
			assertException(t, err, tc.exception, tc.msg)
		})
	}
}

func TestSignUpSecretHash(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, true)

	in := signUpInput(client.ClientID, "alice", testPassword)

	_, err := m.SignUp(ctx, in)
	assertException(t, err, driver.ExNotAuthorized, "Unable to verify secret hash for client "+client.ClientID)

	in.SecretHash = testSecretHash(client.ClientSecret, "someone-else", client.ClientID)
	_, err = m.SignUp(ctx, in)
	assertException(t, err, driver.ExNotAuthorized, "Unable to verify secret hash for client "+client.ClientID)

	in.SecretHash = testSecretHash(client.ClientSecret, "alice", client.ClientID)
	_, err = m.SignUp(ctx, in)
	requireNoError(t, err, "SignUp with SECRET_HASH")
}

func TestConfirmationCodeExpiresAndResends(t *testing.T) {
	m, fc := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false)

	_, err := m.SignUp(ctx, signUpInput(client.ClientID, "alice", testPassword, emailAttr("alice@example.com")))
	requireNoError(t, err, "SignUp")

	code := mustCode(t, m, pool.ID, "alice")

	fc.Advance(25 * time.Hour)

	in := driver.ConfirmSignUpInput{
		ClientUserInput:  driver.ClientUserInput{ClientID: client.ClientID, Username: "alice"},
		ConfirmationCode: code,
	}
	assertException(t, m.ConfirmSignUp(ctx, in), driver.ExExpiredCode, "Invalid code provided, please request a code again.")

	d, err := m.ResendConfirmationCode(ctx, in.ClientUserInput)
	requireNoError(t, err, "ResendConfirmationCode")

	if d.Destination != "a***@e***" {
		t.Fatalf("resend delivery = %+v", d)
	}

	in.ConfirmationCode = mustCode(t, m, pool.ID, "alice")
	requireNoError(t, m.ConfirmSignUp(ctx, in), "ConfirmSignUp with resent code")

	_, err = m.ResendConfirmationCode(ctx, driver.ClientUserInput{ClientID: client.ClientID, Username: "ghost"})
	assertException(t, err, driver.ExUserNotFound, "Username/client id combination not found.")
}

func TestAdminConfirmSignUp(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false)

	_, err := m.SignUp(ctx, signUpInput(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "SignUp")
	requireNoError(t, m.AdminConfirmSignUp(ctx, pool.ID, "alice"), "AdminConfirmSignUp")

	u, _ := m.AdminGetUser(ctx, pool.ID, "alice")
	if u.UserStatus != driver.UserStatusConfirmed {
		t.Fatalf("status = %s", u.UserStatus)
	}

	assertException(t, m.AdminConfirmSignUp(ctx, pool.ID, "alice"), driver.ExNotAuthorized,
		"User cannot be confirmed. Current status is CONFIRMED")
	assertException(t, m.AdminConfirmSignUp(ctx, pool.ID, "ghost"), driver.ExUserNotFound, "User does not exist.")
}

func TestSignUpEmailUsernamePool(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()

	pool, err := m.CreateUserPool(ctx, driver.CreateUserPoolInput{
		Name: "email-login", UsernameAttributes: []string{"email"}, AutoVerifiedAttributes: []string{"email"},
	})
	requireNoError(t, err, "CreateUserPool")

	client := mustCreateClient(t, m, pool.ID, false)

	out, err := m.SignUp(ctx, signUpInput(client.ClientID, "carol@example.com", testPassword))
	requireNoError(t, err, "SignUp")

	u, err := m.AdminGetUser(ctx, pool.ID, "carol@example.com")
	requireNoError(t, err, "AdminGetUser by email")

	if u.Username != out.UserSub || attrValue(u.Attributes, "email") != "carol@example.com" {
		t.Fatalf("user = %+v, want username = sub and email copied", u)
	}

	_, err = m.SignUp(ctx, signUpInput(client.ClientID, "carol@example.com", testPassword))
	assertException(t, err, driver.ExUsernameExists, "An account with the given email already exists.")
}
