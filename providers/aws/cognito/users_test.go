package cognito

import (
	"context"
	"errors"
	"fmt"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

func assertException(t *testing.T, err error, exception, wantMsg string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != exception {
		t.Fatalf("expected %s, got %v", exception, err)
	}

	if wantMsg != "" && cerrors.Message(err) != wantMsg {
		t.Fatalf("message = %q, want %q", cerrors.Message(err), wantMsg)
	}
}

func mustCreateUser(t *testing.T, m *Mock, poolID, username string, attrs ...driver.Attribute) *driver.User {
	t.Helper()

	u, err := m.AdminCreateUser(context.Background(), driver.AdminCreateUserInput{
		UserPoolID:     poolID,
		Username:       username,
		UserAttributes: attrs,
		MessageAction:  driver.MessageActionSuppress,
	})
	requireNoError(t, err, "AdminCreateUser "+username)

	return u
}

func TestAdminCreateUserDefaults(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "users")

	u := mustCreateUser(t, m, pool.ID, "alice", driver.Attribute{Name: "email", Value: "a@example.com"})

	if u.Username != "alice" || !u.Enabled || u.UserStatus != driver.UserStatusForceChangePassword {
		t.Fatalf("new user = %+v", u)
	}

	if len(u.Attributes) != 2 || u.Attributes[0].Name != "sub" || len(u.Attributes[0].Value) != 36 {
		t.Fatalf("attributes = %+v, want sub then email", u.Attributes)
	}

	if u.UserCreateDate.IsZero() || !u.UserCreateDate.Equal(u.UserLastModifiedDate) {
		t.Fatalf("dates = %v / %v", u.UserCreateDate, u.UserLastModifiedDate)
	}

	got, err := m.DescribeUserPool(context.Background(), pool.ID)
	requireNoError(t, err, "DescribeUserPool")

	if got.EstimatedNumberOfUsers != 1 {
		t.Fatalf("EstimatedNumberOfUsers = %d, want 1", got.EstimatedNumberOfUsers)
	}
}

func TestAdminCreateUserErrors(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "errs")
	mustCreateUser(t, m, pool.ID, "bob")

	cases := []struct {
		name      string
		in        driver.AdminCreateUserInput
		exception string
		msg       string
	}{
		{"duplicate", driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "bob"},
			driver.ExUsernameExists, "User account already exists"},
		{"missing pool", driver.AdminCreateUserInput{UserPoolID: "us-east-1_nope00000", Username: "x"},
			driver.ExResourceNotFound, "User pool us-east-1_nope00000 does not exist."},
		{"unknown attribute", driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "c",
			UserAttributes: []driver.Attribute{{Name: "custom:nope", Value: "1"}}},
			driver.ExInvalidParameter, "Attributes did not conform to the schema: custom:nope: Attribute does not exist in the schema."},
		{"short temp password", driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "d", TemporaryPassword: "Ab1!"},
			driver.ExInvalidPassword, "Password did not conform with policy: Password not long enough"},
		{"no symbol", driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "e", TemporaryPassword: "Abcdefg12"},
			driver.ExInvalidPassword, "Password did not conform with policy: Password must have symbol characters"},
		{"resend unknown", driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "ghost",
			MessageAction: driver.MessageActionResend}, driver.ExUserNotFound, "User does not exist."},
		{"bad message action", driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "f", MessageAction: "LOUD"},
			driver.ExInvalidParameter, ""},
		{"empty username", driver.AdminCreateUserInput{UserPoolID: pool.ID}, driver.ExInvalidParameter, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.AdminCreateUser(ctx, tc.in)
			assertException(t, err, tc.exception, tc.msg)
		})
	}
}

func TestAdminCreateUserResend(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "resend")
	mustCreateUser(t, m, pool.ID, "carol")

	before, _ := m.users.Get(userKey(pool.ID, "carol"))

	u, err := m.AdminCreateUser(ctx, driver.AdminCreateUserInput{
		UserPoolID: pool.ID, Username: "carol", MessageAction: driver.MessageActionResend,
	})
	requireNoError(t, err, "RESEND")

	after, _ := m.users.Get(userKey(pool.ID, "carol"))
	if u.UserStatus != driver.UserStatusForceChangePassword || after.PasswordHash == before.PasswordHash {
		t.Fatal("RESEND should keep FORCE_CHANGE_PASSWORD and issue a new temporary password")
	}

	requireNoError(t, m.AdminSetUserPassword(ctx, pool.ID, "carol", "Perm4nent!pw", true), "AdminSetUserPassword")

	_, err = m.AdminCreateUser(ctx, driver.AdminCreateUserInput{
		UserPoolID: pool.ID, Username: "carol", MessageAction: driver.MessageActionResend,
	})
	assertException(t, err, driver.ExUnsupportedUserState, "")
}

func TestGeneratedTemporaryPasswordMeetsPolicy(t *testing.T) {
	pp := driver.PasswordPolicy{
		MinimumLength: 30, RequireUppercase: true, RequireLowercase: true, RequireNumbers: true, RequireSymbols: true,
	}

	for range 50 {
		if err := checkPassword(generatePassword(pp), pp); err != nil {
			t.Fatalf("generated password failed policy: %v", err)
		}
	}
}

func TestPasswordIsStoredHashed(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "hash")

	_, err := m.AdminCreateUser(context.Background(), driver.AdminCreateUserInput{
		UserPoolID: pool.ID, Username: "dan", TemporaryPassword: "Temp0rary!pw",
	})
	requireNoError(t, err, "AdminCreateUser")

	rec, _ := m.users.Get(userKey(pool.ID, "dan"))
	if rec.PasswordHash == "" || rec.PasswordHash == "Temp0rary!pw" || rec.PasswordSalt == "" {
		t.Fatalf("password not hashed: %+v", rec)
	}

	if digest(rec.PasswordSalt, "Temp0rary!pw") != rec.PasswordHash {
		t.Fatal("stored digest does not match the password")
	}
}

func TestEmailUsernamePool(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	pool, err := m.CreateUserPool(ctx, driver.CreateUserPoolInput{Name: "email-login", UsernameAttributes: []string{"email"}})
	requireNoError(t, err, "CreateUserPool")

	_, err = m.AdminCreateUser(ctx, driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "plainname"})
	assertException(t, err, driver.ExInvalidParameter, "Username should be an email.")

	u := mustCreateUser(t, m, pool.ID, "erin@example.com")
	if u.Username != attrValue(u.Attributes, "sub") || attrValue(u.Attributes, "email") != "erin@example.com" {
		t.Fatalf("email-username user = %+v, want username=sub and email set", u)
	}

	got, err := m.AdminGetUser(ctx, pool.ID, "erin@example.com")
	requireNoError(t, err, "AdminGetUser by email")

	if got.Username != u.Username {
		t.Fatalf("lookup by email found %q, want %q", got.Username, u.Username)
	}

	_, err = m.AdminCreateUser(ctx, driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "erin@example.com"})
	assertException(t, err, driver.ExUsernameExists, "An account with the given email already exists.")
}

func TestEmailAliasPool(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	pool, err := m.CreateUserPool(ctx, driver.CreateUserPoolInput{Name: "alias", AliasAttributes: []string{"email"}})
	requireNoError(t, err, "CreateUserPool")

	_, err = m.AdminCreateUser(ctx, driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "x@example.com"})
	assertException(t, err, driver.ExInvalidParameter,
		"Username cannot be of email format, since user pool is configured for email alias.")

	mustCreateUser(t, m, pool.ID, "frank", driver.Attribute{Name: "email", Value: "f@example.com"})

	_, err = m.AdminGetUser(ctx, pool.ID, "f@example.com")
	assertException(t, err, driver.ExUserNotFound, "")

	requireNoError(t, m.AdminUpdateUserAttributes(ctx, pool.ID, "frank",
		[]driver.Attribute{{Name: "email_verified", Value: "true"}}), "verify email")

	got, err := m.AdminGetUser(ctx, pool.ID, "f@example.com")
	requireNoError(t, err, "AdminGetUser by verified alias")

	if got.Username != "frank" {
		t.Fatalf("alias lookup = %q", got.Username)
	}
}

func TestAdminGetUserNotFound(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "get")

	_, err := m.AdminGetUser(context.Background(), pool.ID, "nobody")
	assertException(t, err, driver.ExUserNotFound, "User does not exist.")

	_, err = m.AdminGetUser(context.Background(), "us-east-1_nope00000", "nobody")
	assertException(t, err, driver.ExResourceNotFound, "")
}

func TestListUsersFilter(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "list")

	mustCreateUser(t, m, pool.ID, "amy", driver.Attribute{Name: "email", Value: "amy@corp.example"},
		driver.Attribute{Name: "given_name", Value: "Amy"})
	mustCreateUser(t, m, pool.ID, "ben", driver.Attribute{Name: "email", Value: "ben@other.example"})
	mustCreateUser(t, m, pool.ID, "abe", driver.Attribute{Name: "email", Value: "abe@corp.example"})
	requireNoError(t, m.AdminDisableUser(ctx, pool.ID, "ben"), "AdminDisableUser")

	cases := []struct {
		filter string
		want   []string
	}{
		{"", []string{"abe", "amy", "ben"}},
		{`username = "amy"`, []string{"amy"}},
		{`username ^= "a"`, []string{"abe", "amy"}},
		{`email = "ben@other.example"`, []string{"ben"}},
		{`given_name ^= "Am"`, []string{"amy"}},
		{`status = "Disabled"`, []string{"ben"}},
		{`status = "Enabled"`, []string{"abe", "amy"}},
		{`cognito:user_status = "force_change_password"`, []string{"abe", "amy", "ben"}},
		{`email = "nobody@x"`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.filter, func(t *testing.T) {
			users, _, err := m.ListUsers(ctx, driver.ListUsersInput{UserPoolID: pool.ID, Filter: tc.filter})
			requireNoError(t, err, "ListUsers")

			var names []string
			for _, u := range users {
				names = append(names, u.Username)
			}

			if fmt.Sprint(names) != fmt.Sprint(tc.want) {
				t.Fatalf("filter %q = %v, want %v", tc.filter, names, tc.want)
			}
		})
	}
}

func TestListUsersErrorsAndProjection(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "list-errs")
	mustCreateUser(t, m, pool.ID, "gus", driver.Attribute{Name: "email", Value: "g@example.com"})

	_, _, err := m.ListUsers(ctx, driver.ListUsersInput{UserPoolID: pool.ID, Filter: `custom:tier = "x"`})
	assertException(t, err, driver.ExInvalidParameter, "Invalid search attribute: custom:tier")

	_, _, err = m.ListUsers(ctx, driver.ListUsersInput{UserPoolID: pool.ID, Filter: `email equals x`})
	assertException(t, err, driver.ExInvalidParameter, "Error while parsing filter.")

	_, _, err = m.ListUsers(ctx, driver.ListUsersInput{UserPoolID: pool.ID, Limit: 61})
	assertException(t, err, driver.ExInvalidParameter, "")

	_, _, err = m.ListUsers(ctx, driver.ListUsersInput{UserPoolID: "us-east-1_nope00000"})
	assertException(t, err, driver.ExResourceNotFound, "")

	users, _, err := m.ListUsers(ctx, driver.ListUsersInput{UserPoolID: pool.ID, AttributesToGet: []string{"email"}})
	requireNoError(t, err, "ListUsers AttributesToGet")

	if len(users[0].Attributes) != 1 || users[0].Attributes[0].Name != "email" {
		t.Fatalf("AttributesToGet=[email] gave %+v", users[0].Attributes)
	}

	users, _, err = m.ListUsers(ctx, driver.ListUsersInput{UserPoolID: pool.ID, AttributesToGet: []string{}})
	requireNoError(t, err, "ListUsers AttributesToGet empty")

	if len(users[0].Attributes) != 0 {
		t.Fatalf("empty AttributesToGet gave %+v", users[0].Attributes)
	}

	// The stored user is not changed by a projected read.
	got, err := m.AdminGetUser(ctx, pool.ID, "gus")
	requireNoError(t, err, "AdminGetUser")

	if len(got.Attributes) != 2 {
		t.Fatalf("stored attributes changed by projection: %+v", got.Attributes)
	}
}

func TestListUsersPagination(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "paged")

	for i := range 65 {
		mustCreateUser(t, m, pool.ID, fmt.Sprintf("user%03d", i))
	}

	seen := map[string]bool{}
	token := ""
	pages := 0

	for {
		users, next, err := m.ListUsers(ctx, driver.ListUsersInput{UserPoolID: pool.ID, PaginationToken: token})
		requireNoError(t, err, "ListUsers")

		pages++

		for _, u := range users {
			seen[u.Username] = true
		}

		if next == "" {
			break
		}

		token = next
	}

	if pages != 2 || len(seen) != 65 {
		t.Fatalf("pages=%d users=%d, want 2 pages covering 65 users", pages, len(seen))
	}
}

func TestListUserPoolsMaxResultsCeiling(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for i := range 61 {
		mustCreatePool(t, m, fmt.Sprintf("pool-%02d", i))
	}

	_, _, err := m.ListUserPools(ctx, driver.Pagination{MaxResults: 61})
	assertException(t, err, driver.ExInvalidParameter, "")

	first, next, err := m.ListUserPools(ctx, driver.Pagination{MaxResults: 60})
	requireNoError(t, err, "ListUserPools page 1")

	second, last, err := m.ListUserPools(ctx, driver.Pagination{MaxResults: 60, NextToken: next})
	requireNoError(t, err, "ListUserPools page 2")

	if len(first) != 60 || next == "" || len(second) != 1 || last != "" {
		t.Fatalf("pages = %d (next %q) + %d (next %q)", len(first), next, len(second), last)
	}
}

func TestAdminUpdateUserAttributes(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "update")
	mustCreateUser(t, m, pool.ID, "hal",
		driver.Attribute{Name: "email", Value: "old@example.com"}, driver.Attribute{Name: "email_verified", Value: "true"})

	requireNoError(t, m.AdminUpdateUserAttributes(ctx, pool.ID, "hal", []driver.Attribute{
		{Name: "email", Value: "new@example.com"}, {Name: "name", Value: "Hal"},
	}), "AdminUpdateUserAttributes")

	u, err := m.AdminGetUser(ctx, pool.ID, "hal")
	requireNoError(t, err, "AdminGetUser")

	if attrValue(u.Attributes, "email") != "new@example.com" || attrValue(u.Attributes, "name") != "Hal" {
		t.Fatalf("attributes not updated: %+v", u.Attributes)
	}

	if attrValue(u.Attributes, "email_verified") != "false" {
		t.Fatalf("changed email should be unverified: %+v", u.Attributes)
	}

	err = m.AdminUpdateUserAttributes(ctx, pool.ID, "hal", []driver.Attribute{{Name: "sub", Value: "x"}})
	assertException(t, err, driver.ExInvalidParameter, "")

	err = m.AdminUpdateUserAttributes(ctx, pool.ID, "ghost", []driver.Attribute{{Name: "name", Value: "x"}})
	assertException(t, err, driver.ExUserNotFound, "")
}

func TestAdminDeleteUserAttributes(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "delattr")
	mustCreateUser(t, m, pool.ID, "ivy", driver.Attribute{Name: "name", Value: "Ivy"})

	requireNoError(t, m.AdminDeleteUserAttributes(ctx, pool.ID, "ivy", []string{"name"}), "AdminDeleteUserAttributes")

	u, err := m.AdminGetUser(ctx, pool.ID, "ivy")
	requireNoError(t, err, "AdminGetUser")

	if _, ok := lastValue(u.Attributes, "name"); ok {
		t.Fatalf("name not deleted: %+v", u.Attributes)
	}

	assertException(t, m.AdminDeleteUserAttributes(ctx, pool.ID, "ivy", []string{"sub"}), driver.ExInvalidParameter, "")
	assertException(t, m.AdminDeleteUserAttributes(ctx, pool.ID, "ivy", []string{"custom:x"}), driver.ExInvalidParameter,
		"Attributes did not conform to the schema: custom:x: Attribute does not exist in the schema.")
}

func TestUserStateTransitions(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "states")
	mustCreateUser(t, m, pool.ID, "jay")

	err := m.AdminResetUserPassword(ctx, pool.ID, "jay")
	assertException(t, err, driver.ExNotAuthorized, "User password cannot be reset in the current state.")

	assertException(t, m.AdminSetUserPassword(ctx, pool.ID, "jay", "weak", true), driver.ExInvalidPassword, "")

	requireNoError(t, m.AdminSetUserPassword(ctx, pool.ID, "jay", "Str0ng!Password", true), "AdminSetUserPassword")
	requireStatus(t, m, pool.ID, "jay", driver.UserStatusConfirmed)

	requireNoError(t, m.AdminResetUserPassword(ctx, pool.ID, "jay"), "AdminResetUserPassword")
	requireStatus(t, m, pool.ID, "jay", driver.UserStatusResetRequired)

	requireNoError(t, m.AdminSetUserPassword(ctx, pool.ID, "jay", "Str0ng!Password", false), "temporary set")
	requireStatus(t, m, pool.ID, "jay", driver.UserStatusForceChangePassword)

	requireNoError(t, m.AdminDisableUser(ctx, pool.ID, "jay"), "AdminDisableUser")

	u, _ := m.AdminGetUser(ctx, pool.ID, "jay")
	if u.Enabled {
		t.Fatal("user still enabled after AdminDisableUser")
	}

	requireNoError(t, m.AdminEnableUser(ctx, pool.ID, "jay"), "AdminEnableUser")

	u, _ = m.AdminGetUser(ctx, pool.ID, "jay")
	if !u.Enabled {
		t.Fatal("user still disabled after AdminEnableUser")
	}

	requireNoError(t, m.AdminDeleteUser(ctx, pool.ID, "jay"), "AdminDeleteUser")
	assertException(t, m.AdminDeleteUser(ctx, pool.ID, "jay"), driver.ExUserNotFound, "User does not exist.")
	assertException(t, m.AdminEnableUser(ctx, pool.ID, "jay"), driver.ExUserNotFound, "")
}

func requireStatus(t *testing.T, m *Mock, poolID, username, want string) {
	t.Helper()

	u, err := m.AdminGetUser(context.Background(), poolID, username)
	requireNoError(t, err, "AdminGetUser")

	if u.UserStatus != want {
		t.Fatalf("status = %s, want %s", u.UserStatus, want)
	}
}

func TestAddCustomAttributes(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "custom")

	requireNoError(t, m.AddCustomAttributes(ctx, pool.ID, []driver.SchemaAttribute{
		{Name: "tier", AttributeDataType: driver.AttributeTypeString, Mutable: false},
	}), "AddCustomAttributes")

	got, err := m.DescribeUserPool(ctx, pool.ID)
	requireNoError(t, err, "DescribeUserPool")

	a, ok := schemaAttribute(got, "custom:tier")
	if !ok || a.Mutable {
		t.Fatalf("custom:tier not in schema as immutable: %+v", got.SchemaAttributes)
	}

	err = m.AddCustomAttributes(ctx, pool.ID, []driver.SchemaAttribute{{Name: "tier", AttributeDataType: driver.AttributeTypeString}})
	assertException(t, err, driver.ExInvalidParameter, "Existing attribute already has name custom:tier.")

	// The new attribute can be set on create, but is immutable afterwards.
	mustCreateUser(t, m, pool.ID, "kim", driver.Attribute{Name: "custom:tier", Value: "gold"})

	err = m.AdminUpdateUserAttributes(ctx, pool.ID, "kim", []driver.Attribute{{Name: "custom:tier", Value: "silver"}})
	assertException(t, err, driver.ExInvalidParameter, "")

	assertException(t, m.AddCustomAttributes(ctx, "us-east-1_nope00000",
		[]driver.SchemaAttribute{{Name: "x"}}), driver.ExResourceNotFound, "")
}

func TestAddCustomAttributesLimit(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "limit")

	attrs := make([]driver.SchemaAttribute, 0, maxCustomAttributes)
	for i := range maxCustomAttributes {
		attrs = append(attrs, driver.SchemaAttribute{Name: fmt.Sprintf("a%02d", i)})
	}

	requireNoError(t, m.AddCustomAttributes(ctx, pool.ID, attrs), "AddCustomAttributes 50")

	err := m.AddCustomAttributes(ctx, pool.ID, []driver.SchemaAttribute{{Name: "one-more"}})
	assertException(t, err, driver.ExInvalidParameter, "")
}

func TestDeletePoolCascadesUsers(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "cascade-users")
	other := mustCreatePool(t, m, "other")

	mustCreateUser(t, m, pool.ID, "leo")
	mustCreateUser(t, m, other.ID, "leo")

	requireNoError(t, m.DeleteUserPool(ctx, pool.ID), "DeleteUserPool")

	if m.users.Has(userKey(pool.ID, "leo")) {
		t.Fatal("user survived pool delete")
	}

	if _, err := m.AdminGetUser(ctx, other.ID, "leo"); err != nil {
		t.Fatalf("user in another pool removed: %v", err)
	}
}

func TestSnapshotRoundTripUsers(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "snap")
	u := mustCreateUser(t, m, pool.ID, "mia", driver.Attribute{Name: "email", Value: "m@example.com"})
	requireNoError(t, m.AdminSetUserPassword(ctx, pool.ID, "mia", "Str0ng!Password", true), "AdminSetUserPassword")

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err, "Snapshot")

	restored := newMock(t)
	requireNoError(t, restored.Restore(ctx, data), "Restore")

	got, err := restored.AdminGetUser(ctx, pool.ID, "mia")
	requireNoError(t, err, "AdminGetUser after restore")

	if got.UserStatus != driver.UserStatusConfirmed || attrValue(got.Attributes, "sub") != attrValue(u.Attributes, "sub") {
		t.Fatalf("restored user = %+v", got)
	}

	before, _ := m.users.Get(userKey(pool.ID, "mia"))
	after, _ := restored.users.Get(userKey(pool.ID, "mia"))

	if before.PasswordHash != after.PasswordHash || before.PasswordSalt != after.PasswordSalt {
		t.Fatal("password digest not persisted")
	}
}
