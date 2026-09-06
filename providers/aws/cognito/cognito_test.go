package cognito

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions())
}

func requireNoError(t *testing.T, err error, msg string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func mustCreatePool(t *testing.T, m *Mock, name string) *driver.UserPool {
	t.Helper()

	pool, err := m.CreateUserPool(context.Background(), driver.CreateUserPoolInput{Name: name})
	requireNoError(t, err, "CreateUserPool")

	return pool
}

func TestCreateUserPoolDefaults(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "pool-a")

	if !strings.HasPrefix(pool.ID, "us-east-1_") || len(strings.TrimPrefix(pool.ID, "us-east-1_")) != 9 {
		t.Fatalf("bad pool id: %q", pool.ID)
	}

	wantARN := "arn:aws:cognito-idp:us-east-1:123456789012:userpool/" + pool.ID
	if pool.ARN != wantARN {
		t.Fatalf("ARN = %q, want %q", pool.ARN, wantARN)
	}

	if pool.MFAConfiguration != driver.MFAConfigurationOff {
		t.Fatalf("MFAConfiguration = %q, want OFF", pool.MFAConfiguration)
	}

	if pool.DeletionProtection != driver.DeletionProtectionInactive {
		t.Fatalf("DeletionProtection = %q, want INACTIVE", pool.DeletionProtection)
	}

	if pool.UserPoolTier != driver.UserPoolTierEssentials {
		t.Fatalf("UserPoolTier = %q, want ESSENTIALS", pool.UserPoolTier)
	}

	if len(pool.SchemaAttributes) != 20 {
		t.Fatalf("schema attributes = %d, want 20", len(pool.SchemaAttributes))
	}

	pp := pool.Policies.PasswordPolicy
	if pp.MinimumLength != 8 || !pp.RequireUppercase || !pp.RequireLowercase ||
		!pp.RequireNumbers || !pp.RequireSymbols || pp.TemporaryPasswordValidityDays != 7 {
		t.Fatalf("default password policy wrong: %+v", pp)
	}
}

func TestDefaultSchemaShape(t *testing.T) {
	attrs := defaultSchemaAttributes()

	if got := attrs[0].Name; got != "sub" {
		t.Fatalf("first attr = %q, want sub", got)
	}

	byName := map[string]driver.SchemaAttribute{}
	for _, a := range attrs {
		byName[a.Name] = a
	}

	sub := byName["sub"]
	if sub.Mutable || !sub.Required || sub.AttributeDataType != driver.AttributeTypeString {
		t.Fatalf("sub attribute wrong: %+v", sub)
	}

	ev := byName["email_verified"]
	if ev.AttributeDataType != driver.AttributeTypeBoolean || ev.StringAttributeConstraints != nil {
		t.Fatalf("email_verified should be a constraint-free Boolean: %+v", ev)
	}

	ua := byName["updated_at"]
	if ua.AttributeDataType != driver.AttributeTypeNumber || ua.NumberAttributeConstraints == nil {
		t.Fatalf("updated_at should be a Number with constraints: %+v", ua)
	}

	bd := byName["birthdate"]
	if bd.StringAttributeConstraints == nil || bd.StringAttributeConstraints.MinLength != "10" {
		t.Fatalf("birthdate should be fixed length 10: %+v", bd)
	}
}

func TestCustomSchemaAttributesArePrefixed(t *testing.T) {
	m := newMock(t)

	pool, err := m.CreateUserPool(context.Background(), driver.CreateUserPoolInput{
		Name: "custom-pool",
		SchemaAttributes: []driver.SchemaAttribute{
			{Name: "tenant", AttributeDataType: driver.AttributeTypeString},
			{Name: "internal", AttributeDataType: driver.AttributeTypeString, DeveloperOnlyAttribute: true},
			{Name: "email"}, // standard name is ignored, no duplicate
		},
	})
	requireNoError(t, err, "CreateUserPool")

	names := map[string]bool{}
	for _, a := range pool.SchemaAttributes {
		names[a.Name] = true
	}

	if !names["custom:tenant"] {
		t.Fatal("custom attribute not prefixed with custom:")
	}

	if !names["dev:internal"] {
		t.Fatal("developer-only attribute not prefixed with dev:")
	}

	// The standard-named custom attr must not have created a duplicate.
	if len(pool.SchemaAttributes) != 22 {
		t.Fatalf("schema attributes = %d, want 22 (20 default + 2 custom)", len(pool.SchemaAttributes))
	}
}

func TestDescribeUnknownPool(t *testing.T) {
	m := newMock(t)

	_, err := m.DescribeUserPool(context.Background(), "us-east-1_missing00")
	assertNotFound(t, err)
}

func TestUpdateAndDeleteUserPool(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "up-pool")

	err := m.UpdateUserPool(context.Background(), driver.UpdateUserPoolInput{
		ID:                 pool.ID,
		MFAConfiguration:   driver.MFAConfigurationOptional,
		DeletionProtection: driver.DeletionProtectionActive,
	})
	requireNoError(t, err, "UpdateUserPool")

	got, err := m.DescribeUserPool(context.Background(), pool.ID)
	requireNoError(t, err, "DescribeUserPool")

	if got.MFAConfiguration != driver.MFAConfigurationOptional || got.DeletionProtection != driver.DeletionProtectionActive {
		t.Fatalf("update not applied: %+v", got)
	}

	requireNoError(t, m.DeleteUserPool(context.Background(), pool.ID), "DeleteUserPool")
	assertNotFound(t, m.DeleteUserPool(context.Background(), pool.ID))
}

func TestClientSecretOnlyWithGenerate(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "client-pool")
	ctx := context.Background()

	noSecret, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID: pool.ID,
		ClientName: "public",
	})
	requireNoError(t, err, "CreateUserPoolClient")

	if noSecret.ClientSecret != "" {
		t.Fatalf("secret set without GenerateSecret: %q", noSecret.ClientSecret)
	}

	if len(noSecret.ClientID) != 26 {
		t.Fatalf("client id length = %d, want 26", len(noSecret.ClientID))
	}

	if noSecret.RefreshTokenValidity != 30 || noSecret.AuthSessionValidity != 3 || !noSecret.EnableTokenRevocation {
		t.Fatalf("client defaults wrong: %+v", noSecret)
	}

	if len(noSecret.ExplicitAuthFlows) != 3 {
		t.Fatalf("explicit auth flows = %v, want 3-flow default", noSecret.ExplicitAuthFlows)
	}

	withSecret, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID:     pool.ID,
		ClientName:     "confidential",
		GenerateSecret: true,
	})
	requireNoError(t, err, "CreateUserPoolClient (secret)")

	if len(withSecret.ClientSecret) != 51 {
		t.Fatalf("secret length = %d, want 51", len(withSecret.ClientSecret))
	}
}

func TestUpdateClientPreservesSecret(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "upd-client-pool")
	ctx := context.Background()

	created, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID:     pool.ID,
		ClientName:     "c1",
		GenerateSecret: true,
	})
	requireNoError(t, err, "CreateUserPoolClient")

	updated, err := m.UpdateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
		UserPoolID:   pool.ID,
		ClientID:     created.ClientID,
		ClientName:   "c1-renamed",
		CallbackURLs: []string{"https://example.com/cb"},
	})
	requireNoError(t, err, "UpdateUserPoolClient")

	if updated.ClientSecret != created.ClientSecret {
		t.Fatal("client secret changed on update")
	}

	if updated.ClientName != "c1-renamed" || len(updated.CallbackURLs) != 1 {
		t.Fatalf("update not applied: %+v", updated)
	}

	if !updated.CreationDate.Equal(created.CreationDate) {
		t.Fatal("creation date changed on update")
	}
}

func TestClientOnMissingPool(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateUserPoolClient(context.Background(), driver.CreateUserPoolClientInput{
		UserPoolID: "us-east-1_missing00",
		ClientName: "x",
	})
	assertNotFound(t, err)
}

func TestUserPoolDomainLifecycle(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "domain-pool")
	ctx := context.Background()

	err := m.CreateUserPoolDomain(ctx, driver.CreateUserPoolDomainInput{
		Domain:     "auth.example",
		UserPoolID: pool.ID,
	})
	requireNoError(t, err, "CreateUserPoolDomain")

	got, err := m.DescribeUserPoolDomain(ctx, "auth.example")
	requireNoError(t, err, "DescribeUserPoolDomain")

	if got.Status != driver.DomainStatusActive || got.CloudFrontDistribution == "" {
		t.Fatalf("domain not active/CDN set: %+v", got)
	}

	// Unknown domain returns an empty description and no error.
	empty, err := m.DescribeUserPoolDomain(ctx, "nope.example")
	requireNoError(t, err, "DescribeUserPoolDomain(unknown)")

	if empty.Domain != "" || empty.Status != "" {
		t.Fatalf("unknown domain should be empty: %+v", empty)
	}

	requireNoError(t, m.DeleteUserPoolDomain(ctx, "auth.example", pool.ID), "DeleteUserPoolDomain")

	after, err := m.DescribeUserPoolDomain(ctx, "auth.example")
	requireNoError(t, err, "DescribeUserPoolDomain after delete")

	if after.Domain != "" {
		t.Fatal("domain still present after delete")
	}
}

func TestDeletePoolCascades(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "cascade-pool")
	ctx := context.Background()

	client, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{UserPoolID: pool.ID, ClientName: "c"})
	requireNoError(t, err, "CreateUserPoolClient")

	requireNoError(t, m.CreateUserPoolDomain(ctx,
		driver.CreateUserPoolDomainInput{Domain: "d.example", UserPoolID: pool.ID}), "CreateUserPoolDomain")

	requireNoError(t, m.DeleteUserPool(ctx, pool.ID), "DeleteUserPool")

	if _, err := m.DescribeUserPoolClient(ctx, pool.ID, client.ClientID); !cerrors.IsNotFound(err) {
		t.Fatal("client not removed on pool delete")
	}

	dom, err := m.DescribeUserPoolDomain(ctx, "d.example")
	requireNoError(t, err, "DescribeUserPoolDomain")

	if dom.Domain != "" {
		t.Fatal("domain not removed on pool delete")
	}
}

func TestTagsRoundTripAndReplace(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	pool, err := m.CreateUserPool(ctx, driver.CreateUserPoolInput{
		Name:         "tagged",
		UserPoolTags: map[string]string{"env": "dev", "team": "auth"},
	})
	requireNoError(t, err, "CreateUserPool")

	tags, err := m.ListTagsForResource(ctx, pool.ARN)
	requireNoError(t, err, "ListTagsForResource")

	if tags["env"] != "dev" || tags["team"] != "auth" {
		t.Fatalf("tags not stored: %+v", tags)
	}

	// UpdateUserPool with UserPoolTags replaces the whole set.
	err = m.UpdateUserPool(ctx, driver.UpdateUserPoolInput{
		ID:           pool.ID,
		UserPoolTags: map[string]string{"env": "prod"},
	})
	requireNoError(t, err, "UpdateUserPool tags")

	tags, err = m.ListTagsForResource(ctx, pool.ARN)
	requireNoError(t, err, "ListTagsForResource")

	if _, ok := tags["team"]; ok || tags["env"] != "prod" {
		t.Fatalf("tags not replaced: %+v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, pool.ARN, []string{"env"}), "UntagResource")

	tags, err = m.ListTagsForResource(ctx, pool.ARN)
	requireNoError(t, err, "ListTagsForResource")

	if len(tags) != 0 {
		t.Fatalf("tags not removed: %+v", tags)
	}
}

func TestListPaginationDeterministic(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, n := range []string{"p3", "p1", "p2"} {
		mustCreatePool(t, m, n)
	}

	first, next, err := m.ListUserPools(ctx, driver.Pagination{MaxResults: 2})
	requireNoError(t, err, "ListUserPools page 1")

	if len(first) != 2 || next == "" {
		t.Fatalf("page 1 = %d pools, next=%q", len(first), next)
	}

	second, next2, err := m.ListUserPools(ctx, driver.Pagination{MaxResults: 2, NextToken: next})
	requireNoError(t, err, "ListUserPools page 2")

	if len(second) != 1 || next2 != "" {
		t.Fatalf("page 2 = %d pools, next=%q", len(second), next2)
	}

	// Deterministic: ids sorted ascending across pages.
	all := append(append([]driver.UserPoolDescription{}, first...), second...)
	for i := 1; i < len(all); i++ {
		if all[i-1].ID >= all[i].ID {
			t.Fatalf("list not sorted: %q then %q", all[i-1].ID, all[i].ID)
		}
	}
}

func assertNotFound(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected NotFound error, got nil")
	}

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExResourceNotFound {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}

	if !cerrors.IsNotFound(err) {
		t.Fatalf("expected canonical NotFound code, got %v", err)
	}
}
