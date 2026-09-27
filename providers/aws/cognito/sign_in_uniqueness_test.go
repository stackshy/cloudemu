package cognito

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

func createPoolWith(t *testing.T, m *Mock, in driver.CreateUserPoolInput) *driver.UserPool {
	t.Helper()

	pool, err := m.CreateUserPool(context.Background(), in)
	requireNoError(t, err, "CreateUserPool")

	return pool
}

func TestUsernameAttributeEmailStaysUnique(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := createPoolWith(t, m, driver.CreateUserPoolInput{Name: "email-login", UsernameAttributes: []string{"email"}})

	a := mustCreateUser(t, m, pool.ID, "a@x.com")
	b := mustCreateUser(t, m, pool.ID, "b@x.com")

	// Email comparison ignores case, on create and on update.
	_, err := m.AdminCreateUser(ctx, driver.AdminCreateUserInput{UserPoolID: pool.ID, Username: "A@X.com"})
	assertException(t, err, driver.ExUsernameExists, "An account with the given email already exists.")

	err = m.AdminUpdateUserAttributes(ctx, pool.ID, b.Username, []driver.Attribute{{Name: "email", Value: "A@x.com"}})
	assertException(t, err, driver.ExAliasExists, "An account with the given email already exists.")

	got, err := m.AdminGetUser(ctx, pool.ID, "b@x.com")
	requireNoError(t, err, "AdminGetUser b")

	if got.Username != b.Username {
		t.Fatalf("b's sign-in changed after the refused update")
	}

	got, err = m.AdminGetUser(ctx, pool.ID, "A@X.COM")
	requireNoError(t, err, "AdminGetUser mixed case")

	if got.Username != a.Username {
		t.Fatalf("mixed-case lookup found %q, want %q", got.Username, a.Username)
	}

	// Moving b to a free address still works.
	requireNoError(t, m.AdminUpdateUserAttributes(ctx, pool.ID, b.Username,
		[]driver.Attribute{{Name: "email", Value: "c@x.com"}}), "update to free email")
}

func verifiedEmail(email string) []driver.Attribute {
	return []driver.Attribute{{Name: "email", Value: email}, {Name: "email_verified", Value: "true"}}
}

func TestEmailAliasCreateConflictAndForce(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := createPoolWith(t, m, driver.CreateUserPoolInput{Name: "alias", AliasAttributes: []string{"email"}})

	mustCreateUser(t, m, pool.ID, "first", verifiedEmail("shared@x.com")...)

	_, err := m.AdminCreateUser(ctx, driver.AdminCreateUserInput{
		UserPoolID: pool.ID, Username: "second", UserAttributes: verifiedEmail("Shared@x.com"),
	})
	assertException(t, err, driver.ExAliasExists, "An account with the given email already exists.")

	if m.users.Has(userKey(pool.ID, "second")) {
		t.Fatal("refused create still stored the user")
	}

	// An unverified copy of the address is not an alias, so it never conflicts.
	mustCreateUser(t, m, pool.ID, "unverified", driver.Attribute{Name: "email", Value: "shared@x.com"})

	_, err = m.AdminCreateUser(ctx, driver.AdminCreateUserInput{
		UserPoolID: pool.ID, Username: "second", UserAttributes: verifiedEmail("shared@x.com"),
		ForceAliasCreation: true, MessageAction: driver.MessageActionSuppress,
	})
	requireNoError(t, err, "AdminCreateUser ForceAliasCreation")

	first, err := m.AdminGetUser(ctx, pool.ID, "first")
	requireNoError(t, err, "AdminGetUser first")

	if attrValue(first.Attributes, "email_verified") != "false" || attrValue(first.Attributes, "email") != "shared@x.com" {
		t.Fatalf("old owner should keep the email but lose verification: %+v", first.Attributes)
	}

	owner, err := m.AdminGetUser(ctx, pool.ID, "shared@x.com")
	requireNoError(t, err, "AdminGetUser by alias")

	if owner.Username != "second" {
		t.Fatalf("alias resolves to %q, want second", owner.Username)
	}
}

func TestEmailAliasUpdateConflict(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := createPoolWith(t, m, driver.CreateUserPoolInput{Name: "alias-upd", AliasAttributes: []string{"email", "phone_number"}})

	mustCreateUser(t, m, pool.ID, "owner", verifiedEmail("taken@x.com")...)
	mustCreateUser(t, m, pool.ID, "other", driver.Attribute{Name: "email", Value: "taken@x.com"})

	// Flipping email_verified to true on a value another user holds verified.
	err := m.AdminUpdateUserAttributes(ctx, pool.ID, "other", []driver.Attribute{{Name: "email_verified", Value: "true"}})
	assertException(t, err, driver.ExAliasExists, "An account with the given email already exists.")

	// Setting a verified email that is already someone's alias.
	mustCreateUser(t, m, pool.ID, "third")

	err = m.AdminUpdateUserAttributes(ctx, pool.ID, "third", verifiedEmail("TAKEN@x.com"))
	assertException(t, err, driver.ExAliasExists, "")

	// Phone numbers follow the same rule.
	mustCreateUser(t, m, pool.ID, "caller", driver.Attribute{Name: "phone_number", Value: "+15550100"},
		driver.Attribute{Name: "phone_number_verified", Value: "true"})

	err = m.AdminUpdateUserAttributes(ctx, pool.ID, "third", []driver.Attribute{
		{Name: "phone_number", Value: "+15550100"}, {Name: "phone_number_verified", Value: "true"},
	})
	assertException(t, err, driver.ExAliasExists, "An account with the given phone_number already exists.")

	// An unverified value is fine.
	requireNoError(t, m.AdminUpdateUserAttributes(ctx, pool.ID, "third",
		[]driver.Attribute{{Name: "email", Value: "taken@x.com"}}), "unverified duplicate")
}

func TestPreferredUsernameAliasUnique(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := createPoolWith(t, m, driver.CreateUserPoolInput{Name: "pref", AliasAttributes: []string{"preferred_username"}})

	mustCreateUser(t, m, pool.ID, "u1", driver.Attribute{Name: "preferred_username", Value: "neo"})
	mustCreateUser(t, m, pool.ID, "u2")

	_, err := m.AdminCreateUser(ctx, driver.AdminCreateUserInput{
		UserPoolID: pool.ID, Username: "u3", UserAttributes: []driver.Attribute{{Name: "preferred_username", Value: "neo"}},
	})
	assertException(t, err, driver.ExAliasExists, "An account with the given preferred_username already exists.")

	err = m.AdminUpdateUserAttributes(ctx, pool.ID, "u2", []driver.Attribute{{Name: "preferred_username", Value: "neo"}})
	assertException(t, err, driver.ExAliasExists, "")

	got, err := m.AdminGetUser(ctx, pool.ID, "neo")
	requireNoError(t, err, "AdminGetUser by preferred_username")

	if got.Username != "u1" {
		t.Fatalf("preferred_username resolves to %q", got.Username)
	}
}

func TestCustomAttributeNameForms(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "names")

	requireNoError(t, m.AddCustomAttributes(ctx, pool.ID, []driver.SchemaAttribute{{Name: "custom:t3"}}), "prefixed name")

	got, err := m.DescribeUserPool(ctx, pool.ID)
	requireNoError(t, err, "DescribeUserPool")

	if _, ok := schemaAttribute(got, "custom:t3"); !ok {
		t.Fatalf("custom:t3 not stored under its own name: %+v", got.SchemaAttributes)
	}

	if _, ok := schemaAttribute(got, "custom:custom:t3"); ok {
		t.Fatal("prefix doubled")
	}

	err = m.AddCustomAttributes(ctx, pool.ID, []driver.SchemaAttribute{{Name: "t3"}})
	assertException(t, err, driver.ExInvalidParameter, "Existing attribute already has name custom:t3.")

	err = m.AddCustomAttributes(ctx, pool.ID, []driver.SchemaAttribute{{Name: strings.Repeat("n", 21)}})
	assertException(t, err, driver.ExInvalidParameter, "")

	requireNoError(t, m.AddCustomAttributes(ctx, pool.ID,
		[]driver.SchemaAttribute{{Name: strings.Repeat("n", 20)}}), "20-character name")
}

func TestStandardAttributeOverrides(t *testing.T) {
	m := newMock(t)
	pool := createPoolWith(t, m, driver.CreateUserPoolInput{
		Name: "required-email",
		SchemaAttributes: []driver.SchemaAttribute{{
			Name: "email", AttributeDataType: driver.AttributeTypeString, Required: true, Mutable: true,
			StringAttributeConstraints: &driver.StringAttributeConstraints{MinLength: "5", MaxLength: "100"},
		}},
	})

	email, ok := schemaAttribute(pool, "email")
	if !ok || !email.Required || !email.Mutable || email.StringAttributeConstraints.MaxLength != "100" {
		t.Fatalf("email override not applied: %+v", email)
	}

	if len(pool.SchemaAttributes) != 20 {
		t.Fatalf("override added a duplicate attribute: %d attributes", len(pool.SchemaAttributes))
	}

	// AdminCreateUser may leave required attributes empty (AWS docs, "Working
	// with user attributes"), so no required check applies here.
	mustCreateUser(t, m, pool.ID, "no-email")
}
