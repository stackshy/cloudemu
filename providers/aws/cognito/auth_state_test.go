package cognito

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// startNewPasswordChallenge creates a FORCE_CHANGE_PASSWORD user and opens its
// NEW_PASSWORD_REQUIRED challenge.
func startNewPasswordChallenge(t *testing.T, m *Mock, poolID, clientID string) driver.RespondToAuthChallengeInput {
	t.Helper()

	ctx := context.Background()

	_, err := m.AdminCreateUser(ctx, driver.AdminCreateUserInput{
		UserPoolID: poolID, Username: "temp", TemporaryPassword: "Temp0rary!", MessageAction: driver.MessageActionSuppress,
	})
	requireNoError(t, err, "AdminCreateUser")

	res, err := m.InitiateAuth(ctx, passwordAuth(clientID, "temp", "Temp0rary!"))
	requireNoError(t, err, "InitiateAuth")

	if res.ChallengeName != driver.ChallengeNewPasswordRequired {
		t.Fatalf("challenge = %q", res.ChallengeName)
	}

	return driver.RespondToAuthChallengeInput{
		ClientID: clientID, ChallengeName: driver.ChallengeNewPasswordRequired, Session: res.Session,
		ChallengeResponses: map[string]string{"USERNAME": "temp", "NEW_PASSWORD": "N3wPassword!"},
	}
}

func TestRespondRejectsUserDisabledMidChallenge(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)

	respond := startNewPasswordChallenge(t, m, pool.ID, client.ClientID)
	requireNoError(t, m.AdminDisableUser(ctx, pool.ID, "temp"), "AdminDisableUser")

	_, err := m.RespondToAuthChallenge(ctx, respond)
	assertException(t, err, driver.ExNotAuthorized, "User is disabled.")

	u, _ := m.AdminGetUser(ctx, pool.ID, "temp")
	if u.Enabled || u.UserStatus != driver.UserStatusForceChangePassword {
		t.Fatalf("user = enabled %v status %s, want disabled and still FORCE_CHANGE_PASSWORD", u.Enabled, u.UserStatus)
	}

	if n := len(m.logins.Keys()); n != 0 {
		t.Fatalf("%d logins recorded for a disabled user", n)
	}
}

func TestRespondRejectsUserNoLongerInChallengeState(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)

	respond := startNewPasswordChallenge(t, m, pool.ID, client.ClientID)
	requireNoError(t, m.AdminSetUserPassword(ctx, pool.ID, "temp", "Perm4nent!pw", true), "AdminSetUserPassword")

	_, err := m.RespondToAuthChallenge(ctx, respond)
	assertException(t, err, driver.ExNotAuthorized, "Invalid session for the user.")

	// A user deleted and re-created under the same name cannot use the old
	// user's session.
	respond = startNewPasswordChallengeAgain(t, m, pool.ID, client.ClientID)
	requireNoError(t, m.AdminDeleteUser(ctx, pool.ID, "temp"), "AdminDeleteUser")
	_, err = m.AdminCreateUser(ctx, driver.AdminCreateUserInput{
		UserPoolID: pool.ID, Username: "temp", TemporaryPassword: "Temp0rary!", MessageAction: driver.MessageActionSuppress,
	})
	requireNoError(t, err, "AdminCreateUser again")

	_, err = m.RespondToAuthChallenge(ctx, respond)
	assertException(t, err, driver.ExNotAuthorized, "Invalid session for the user.")
}

// startNewPasswordChallengeAgain reopens the challenge for the existing temp
// user after resetting it to a temporary password.
func startNewPasswordChallengeAgain(t *testing.T, m *Mock, poolID, clientID string) driver.RespondToAuthChallengeInput {
	t.Helper()

	ctx := context.Background()
	requireNoError(t, m.AdminSetUserPassword(ctx, poolID, "temp", "Temp0rary!", false), "AdminSetUserPassword temp")

	res, err := m.InitiateAuth(ctx, passwordAuth(clientID, "temp", "Temp0rary!"))
	requireNoError(t, err, "InitiateAuth")

	return driver.RespondToAuthChallengeInput{
		ClientID: clientID, ChallengeName: driver.ChallengeNewPasswordRequired, Session: res.Session,
		ChallengeResponses: map[string]string{"USERNAME": "temp", "NEW_PASSWORD": "N3wPassword!"},
	}
}

func TestRefreshAndTokensRecheckUserState(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	res, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "InitiateAuth")

	refresh := driver.InitiateAuthInput{
		ClientID: client.ClientID, AuthFlow: driver.AuthFlowRefreshTokenAuth,
		AuthParameters: map[string]string{"REFRESH_TOKEN": res.AuthenticationResult.RefreshToken},
	}

	requireNoError(t, m.AdminDisableUser(ctx, pool.ID, "alice"), "AdminDisableUser")

	_, err = m.InitiateAuth(ctx, refresh)
	assertException(t, err, driver.ExNotAuthorized, "User is disabled.")

	_, err = m.GetUser(ctx, res.AuthenticationResult.AccessToken)
	assertException(t, err, driver.ExNotAuthorized, "User is disabled.")

	requireNoError(t, m.AdminEnableUser(ctx, pool.ID, "alice"), "AdminEnableUser")
	requireNoError(t, m.AdminResetUserPassword(ctx, pool.ID, "alice"), "AdminResetUserPassword")

	_, err = m.InitiateAuth(ctx, refresh)
	assertException(t, err, driver.ExPasswordResetRequired, "Password reset required for the user")

	_, err = m.GetUser(ctx, res.AuthenticationResult.AccessToken)
	assertException(t, err, driver.ExPasswordResetRequired, "Password reset required for the user")
}

func TestUserExistenceHiddenOnConfirmAndResend(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)

	client, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID: pool.ID, ClientName: "hidden", PreventUserExistenceErrors: "ENABLED",
	})
	requireNoError(t, err, "CreateUserPoolClient")

	ghost := driver.ClientUserInput{ClientID: client.ClientID, Username: "ghost@example.com"}

	err = m.ConfirmSignUp(ctx, driver.ConfirmSignUpInput{ClientUserInput: ghost, ConfirmationCode: "123456"})
	assertException(t, err, driver.ExCodeMismatch, "Invalid verification code provided, please try again.")

	d, err := m.ResendConfirmationCode(ctx, ghost)
	requireNoError(t, err, "ResendConfirmationCode for an unknown user")

	if d == nil || d.DeliveryMedium != driver.DeliveryMediumEmail || d.Destination != "g***@e***" {
		t.Fatalf("simulated delivery = %+v", d)
	}

	if n := len(m.users.Keys()); n != 0 {
		t.Fatalf("%d users created by the simulated paths", n)
	}

	legacy := mustCreateClient(t, m, pool.ID, false)

	_, err = m.ResendConfirmationCode(ctx, driver.ClientUserInput{ClientID: legacy.ClientID, Username: "ghost"})
	assertException(t, err, driver.ExUserNotFound, "Username/client id combination not found.")
}

func TestClientTokenValidityRange(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)

	units := func(access, id, refresh string) *driver.TokenValidityUnits {
		return &driver.TokenValidityUnits{AccessToken: access, IDToken: id, RefreshToken: refresh}
	}

	cases := []struct {
		name                string
		access, id, refresh *int32
		units               *driver.TokenValidityUnits
		ok                  bool
	}{
		{"defaults", nil, nil, nil, nil, true},
		{"access 5 minutes", int32Ptr(5), nil, nil, units("minutes", "", ""), true},
		{"access 4 minutes", int32Ptr(4), nil, nil, units("minutes", "", ""), false},
		{"access 24 hours", int32Ptr(24), nil, nil, nil, true},
		{"access 25 hours", int32Ptr(25), nil, nil, nil, false},
		{"access 1 day", int32Ptr(1), nil, nil, units("days", "", ""), true},
		{"access 2 days", int32Ptr(2), nil, nil, units("days", "", ""), false},
		{"id 299 seconds", nil, int32Ptr(299), nil, units("", "seconds", ""), false},
		{"id 86400 seconds", nil, int32Ptr(86400), nil, units("", "seconds", ""), true},
		{"refresh 60 minutes", nil, nil, int32Ptr(60), units("", "", "minutes"), true},
		{"refresh 59 minutes", nil, nil, int32Ptr(59), units("", "", "minutes"), false},
		{"refresh 3650 days", nil, nil, int32Ptr(3650), nil, true},
		{"refresh 3651 days", nil, nil, int32Ptr(3651), nil, false},
		{"bad unit", int32Ptr(1), nil, nil, units("weeks", "", ""), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := driver.CreateUserPoolClientInput{
				UserPoolID: pool.ID, ClientName: "v", AccessTokenValidity: tc.access, IDTokenValidity: tc.id,
				RefreshTokenValidity: tc.refresh, TokenValidityUnits: tc.units,
			}

			c, err := m.CreateUserPoolClient(ctx, in)
			if tc.ok {
				requireNoError(t, err, "CreateUserPoolClient")

				in.ClientID = c.ClientID
				_, err = m.UpdateUserPoolClient(ctx, in)
				requireNoError(t, err, "UpdateUserPoolClient")

				return
			}

			assertException(t, err, driver.ExInvalidParameter, "")
		})
	}

	c := mustCreateClient(t, m, pool.ID, false)
	_, err := m.UpdateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID: pool.ID, ClientID: c.ClientID, AccessTokenValidity: int32Ptr(2),
		TokenValidityUnits: units("minutes", "", ""),
	})
	assertException(t, err, driver.ExInvalidParameter, "")
}
