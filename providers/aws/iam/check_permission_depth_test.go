package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// TestCheckPermissionViaGroup proves the deepened evaluation honors policies a
// user inherits from a group it belongs to (not just directly attached ones).
func TestCheckPermissionViaGroup(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, mustUser(t, m, "dana"))
	requireNoError(t, mustGroup(t, m, "readers"))

	p, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "s3-read",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"},
		}),
	})
	requireNoError(t, err)
	requireNoError(t, m.AttachGroupPolicy(ctx, "readers", p.ARN))
	requireNoError(t, m.AddUserToGroup(ctx, "dana", "readers"))

	allowed, err := m.CheckPermission(ctx, "dana", "s3:GetObject", "*")
	requireNoError(t, err)
	assertEqual(t, true, allowed)

	denied, err := m.CheckPermission(ctx, "dana", "s3:PutObject", "*")
	requireNoError(t, err)
	assertEqual(t, false, denied)
}

// TestCheckPermissionViaInlinePolicy proves inline user policies are evaluated.
func TestCheckPermissionViaInlinePolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, mustUser(t, m, "erin"))
	requireNoError(t, m.PutUserPolicy(ctx, "erin", "inline-read", makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"},
	})))

	allowed, err := m.CheckPermission(ctx, "erin", "s3:GetObject", "*")
	requireNoError(t, err)
	assertEqual(t, true, allowed)
}

// TestCheckPermissionPermissionsBoundary proves a permissions boundary intersects
// the identity policies: an action allowed by an attached policy is still denied
// when the boundary does not permit it, and allowed when both do.
func TestCheckPermissionPermissionsBoundary(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, mustUser(t, m, "frank"))

	admin, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "admin-all",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Allow", "Action": "*", "Resource": "*"},
		}),
	})
	requireNoError(t, err)
	requireNoError(t, m.AttachUserPolicy(ctx, "frank", admin.ARN))

	boundary, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "boundary-s3-only",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
		}),
	})
	requireNoError(t, err)
	requireNoError(t, m.PutUserPermissionsBoundary(ctx, "frank", boundary.ARN))

	t.Run("within boundary allowed", func(t *testing.T) {
		allowed, err := m.CheckPermission(ctx, "frank", "s3:GetObject", "*")
		requireNoError(t, err)
		assertEqual(t, true, allowed)
	})

	t.Run("outside boundary denied despite admin allow", func(t *testing.T) {
		allowed, err := m.CheckPermission(ctx, "frank", "ec2:RunInstances", "*")
		requireNoError(t, err)
		assertEqual(t, false, allowed)
	})
}

// TestCheckPermissionExplicitDenyInGroupWins proves an explicit Deny inherited
// from a group overrides an Allow on a directly attached policy.
func TestCheckPermissionExplicitDenyInGroupWins(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, mustUser(t, m, "gwen"))
	requireNoError(t, mustGroup(t, m, "locked"))

	allow, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "allow-all",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Allow", "Action": "*", "Resource": "*"},
		}),
	})
	requireNoError(t, err)
	requireNoError(t, m.AttachUserPolicy(ctx, "gwen", allow.ARN))

	deny, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "deny-terminate",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Deny", "Action": "ec2:TerminateInstances", "Resource": "*"},
		}),
	})
	requireNoError(t, err)
	requireNoError(t, m.AttachGroupPolicy(ctx, "locked", deny.ARN))
	requireNoError(t, m.AddUserToGroup(ctx, "gwen", "locked"))

	allowed, err := m.CheckPermission(ctx, "gwen", "ec2:TerminateInstances", "*")
	requireNoError(t, err)
	assertEqual(t, false, allowed)
}

// TestPrincipalHasPolicies confirms the inspector distinguishes a principal with
// no policies from one that has an attached, inline, group, or boundary policy.
func TestPrincipalHasPolicies(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, mustUser(t, m, "hank"))
	if m.PrincipalHasPolicies(ctx, "hank") {
		t.Fatal("fresh user should report no policies")
	}

	requireNoError(t, m.PutUserPolicy(ctx, "hank", "inline", makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"},
	})))
	if !m.PrincipalHasPolicies(ctx, "hank") {
		t.Fatal("user with an inline policy should report having policies")
	}
}

func mustUser(t *testing.T, m *Mock, name string) error {
	t.Helper()
	_, err := m.CreateUser(context.Background(), driver.UserConfig{Name: name})

	return err
}

func mustGroup(t *testing.T, m *Mock, name string) error {
	t.Helper()
	_, err := m.CreateGroup(context.Background(), driver.GroupConfig{Name: name})

	return err
}
