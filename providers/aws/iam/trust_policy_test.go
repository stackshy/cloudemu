package iam

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const rootCaller = "arn:aws:iam::123456789012:root"

func TestEvaluateAssumeRoleTrustAllow(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateRole(ctx, driver.RoleConfig{
		Name: "allowrole",
		AssumeRolePolicyDoc: `{"Statement":[{"Effect":"Allow",` +
			`"Principal":{"AWS":"` + rootCaller + `"},"Action":"sts:AssumeRole"}]}`,
	})
	requireNoError(t, err)

	exists, allowed := m.EvaluateAssumeRoleTrust(ctx, "allowrole", rootCaller)
	assertEqual(t, true, exists)
	assertEqual(t, true, allowed)
}

func TestEvaluateAssumeRoleTrustDeny(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateRole(ctx, driver.RoleConfig{
		Name: "denyrole",
		AssumeRolePolicyDoc: `{"Statement":[{"Effect":"Deny",` +
			`"Principal":"*","Action":"sts:AssumeRole"}]}`,
	})
	requireNoError(t, err)

	exists, allowed := m.EvaluateAssumeRoleTrust(ctx, "denyrole", rootCaller)
	assertEqual(t, true, exists)
	assertEqual(t, false, allowed)
}

func TestEvaluateAssumeRoleTrustDenyOverridesAllow(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateRole(ctx, driver.RoleConfig{
		Name: "mixedrole",
		AssumeRolePolicyDoc: `{"Statement":[` +
			`{"Effect":"Allow","Principal":"*","Action":"sts:AssumeRole"},` +
			`{"Effect":"Deny","Principal":"*","Action":"sts:AssumeRole"}]}`,
	})
	requireNoError(t, err)

	_, allowed := m.EvaluateAssumeRoleTrust(ctx, "mixedrole", rootCaller)
	assertEqual(t, false, allowed)
}

func TestEvaluateAssumeRoleTrustMissingRole(t *testing.T) {
	m := newTestMock()

	exists, allowed := m.EvaluateAssumeRoleTrust(context.Background(), "ghost", rootCaller)
	assertEqual(t, false, exists)
	assertEqual(t, false, allowed)
}

func TestEvaluateAssumeRoleTrustWrongPrincipalImplicitDeny(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Trust only a specific unrelated service principal; the account-root caller
	// is not named, so assumption is an implicit deny.
	_, err := m.CreateRole(ctx, driver.RoleConfig{
		Name: "servicerole",
		AssumeRolePolicyDoc: `{"Statement":[{"Effect":"Allow",` +
			`"Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`,
	})
	requireNoError(t, err)

	_, allowed := m.EvaluateAssumeRoleTrust(ctx, "servicerole", rootCaller)
	assertEqual(t, false, allowed)
}

func TestAddRoleToInstanceProfileSecondRoleLimitExceeded(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstanceProfile(ctx, driver.InstanceProfileConfig{Name: "ip1"})
	requireNoError(t, err)

	for _, name := range []string{"r1", "r2"} {
		_, err = m.CreateRole(ctx, driver.RoleConfig{Name: name, AssumeRolePolicyDoc: "{}"})
		requireNoError(t, err)
	}

	requireNoError(t, m.AddRoleToInstanceProfile(ctx, "ip1", "r1"))

	// A second, different role exceeds the one-role-per-profile quota.
	err = m.AddRoleToInstanceProfile(ctx, "ip1", "r2")
	assertEqual(t, cerrors.ResourceExhausted, cerrors.GetCode(err))
}

func TestListEntitiesForPolicyReverseLookup(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "shared", PolicyDocument: "{}"})
	requireNoError(t, err)

	_, err = m.CreateUser(ctx, driver.UserConfig{Name: "u1"})
	requireNoError(t, err)
	_, err = m.CreateRole(ctx, driver.RoleConfig{Name: "r1", AssumeRolePolicyDoc: "{}"})
	requireNoError(t, err)

	requireNoError(t, m.AttachUserPolicy(ctx, "u1", pol.ARN))
	requireNoError(t, m.AttachRolePolicy(ctx, "r1", pol.ARN))

	ents, err := m.ListEntitiesForPolicy(ctx, pol.ARN)
	requireNoError(t, err)

	assertEqual(t, 1, len(ents.Users))
	assertEqual(t, "u1", ents.Users[0].Name)
	assertEqual(t, 1, len(ents.Roles))
	assertEqual(t, "r1", ents.Roles[0].Name)
	assertEqual(t, 0, len(ents.Groups))
}

func TestListEntitiesForPolicyMissingPolicy(t *testing.T) {
	m := newTestMock()

	_, err := m.ListEntitiesForPolicy(context.Background(), "arn:aws:iam::123456789012:policy/ghost")
	assertEqual(t, cerrors.NotFound, cerrors.GetCode(err))
}
