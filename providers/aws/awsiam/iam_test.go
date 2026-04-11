package awsiam

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/iam/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithAccountID("123456789012"))
	return New(opts)
}

func makePolicyDoc(statements []map[string]any) string {
	doc := map[string]any{
		"Version":   "2012-10-17",
		"Statement": statements,
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func TestCreateUser(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.UserConfig
		setup     func(m *Mock)
		expectErr bool
	}{
		{name: "success", cfg: driver.UserConfig{Name: "alice", Tags: map[string]string{"team": "dev"}}},
		{name: "empty name", cfg: driver.UserConfig{}, expectErr: true},
		{
			name: "already exists",
			cfg:  driver.UserConfig{Name: "alice"},
			setup: func(m *Mock) {
				_, _ = m.CreateUser(context.Background(), driver.UserConfig{Name: "alice"})
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			info, err := m.CreateUser(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, "alice", info.Name)
			assertEqual(t, "/", info.Path)
			assertNotEmpty(t, info.ID)
			assertNotEmpty(t, info.ARN)
		})
	}
}

func TestDeleteUser(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteUser(ctx, "alice")
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteUser(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestGetUser(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})

	t.Run("found", func(t *testing.T) {
		info, err := m.GetUser(ctx, "alice")
		requireNoError(t, err)
		assertEqual(t, "alice", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetUser(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestListUsers(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "bob"})

	users, err := m.ListUsers(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(users))
}

func TestCreateRole(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.RoleConfig
		setup     func(m *Mock)
		expectErr bool
	}{
		{name: "success", cfg: driver.RoleConfig{Name: "admin", AssumeRolePolicyDoc: "{}"}},
		{name: "empty name", cfg: driver.RoleConfig{}, expectErr: true},
		{
			name: "already exists",
			cfg:  driver.RoleConfig{Name: "admin"},
			setup: func(m *Mock) {
				_, _ = m.CreateRole(context.Background(), driver.RoleConfig{Name: "admin"})
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			info, err := m.CreateRole(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, "admin", info.Name)
			assertNotEmpty(t, info.ARN)
		})
	}
}

func TestDeleteRole(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})

	requireNoError(t, m.DeleteRole(ctx, "role1"))
	assertError(t, m.DeleteRole(ctx, "nope"), true)
}

func TestGetAndListRoles(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "r1"})
	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "r2"})

	info, err := m.GetRole(ctx, "r1")
	requireNoError(t, err)
	assertEqual(t, "r1", info.Name)

	_, err = m.GetRole(ctx, "nope")
	assertError(t, err, true)

	roles, err := m.ListRoles(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(roles))
}

func TestCreatePolicy(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.PolicyConfig
		expectErr bool
	}{
		{name: "success", cfg: driver.PolicyConfig{Name: "read-only", PolicyDocument: "{}"}},
		{name: "empty name", cfg: driver.PolicyConfig{}, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			info, err := m.CreatePolicy(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, "read-only", info.Name)
			assertNotEmpty(t, info.ARN)
		})
	}
}

func TestDeletePolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})

	requireNoError(t, m.DeletePolicy(ctx, p.ARN))
	assertError(t, m.DeletePolicy(ctx, "arn:nonexistent"), true)
}

func TestAttachUserPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})

	t.Run("success", func(t *testing.T) {
		err := m.AttachUserPolicy(ctx, "alice", p.ARN)
		requireNoError(t, err)

		policies, err := m.ListAttachedUserPolicies(ctx, "alice")
		requireNoError(t, err)
		assertEqual(t, 1, len(policies))
	})

	t.Run("user not found", func(t *testing.T) {
		err := m.AttachUserPolicy(ctx, "nope", p.ARN)
		assertError(t, err, true)
	})

	t.Run("policy not found", func(t *testing.T) {
		err := m.AttachUserPolicy(ctx, "alice", "arn:nope")
		assertError(t, err, true)
	})
}

func TestDetachUserPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_ = m.AttachUserPolicy(ctx, "alice", p.ARN)

	t.Run("success", func(t *testing.T) {
		err := m.DetachUserPolicy(ctx, "alice", p.ARN)
		requireNoError(t, err)
	})

	t.Run("not attached", func(t *testing.T) {
		err := m.DetachUserPolicy(ctx, "alice", p.ARN)
		assertError(t, err, true)
	})

	t.Run("user not found", func(t *testing.T) {
		err := m.DetachUserPolicy(ctx, "nope", p.ARN)
		assertError(t, err, true)
	})
}

func TestAttachRolePolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})

	t.Run("success", func(t *testing.T) {
		err := m.AttachRolePolicy(ctx, "role1", p.ARN)
		requireNoError(t, err)

		policies, err := m.ListAttachedRolePolicies(ctx, "role1")
		requireNoError(t, err)
		assertEqual(t, 1, len(policies))
	})

	t.Run("role not found", func(t *testing.T) {
		err := m.AttachRolePolicy(ctx, "nope", p.ARN)
		assertError(t, err, true)
	})
}

func TestDetachRolePolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_ = m.AttachRolePolicy(ctx, "role1", p.ARN)

	requireNoError(t, m.DetachRolePolicy(ctx, "role1", p.ARN))
	assertError(t, m.DetachRolePolicy(ctx, "role1", p.ARN), true)
	assertError(t, m.DetachRolePolicy(ctx, "nope", p.ARN), true)
}

func TestCheckPermissionAllow(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "s3-read",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{
				"Effect":   "Allow",
				"Action":   []any{"s3:GetObject", "s3:ListBucket"},
				"Resource": []any{"arn:aws:s3:::my-bucket/*"},
			},
		}),
	})
	_ = m.AttachUserPolicy(ctx, "alice", p.ARN)

	t.Run("allowed action", func(t *testing.T) {
		allowed, err := m.CheckPermission(ctx, "alice", "s3:GetObject", "arn:aws:s3:::my-bucket/file.txt")
		requireNoError(t, err)
		assertEqual(t, true, allowed)
	})

	t.Run("denied action - not matching", func(t *testing.T) {
		allowed, err := m.CheckPermission(ctx, "alice", "s3:PutObject", "arn:aws:s3:::my-bucket/file.txt")
		requireNoError(t, err)
		assertEqual(t, false, allowed)
	})

	t.Run("denied resource - not matching", func(t *testing.T) {
		allowed, err := m.CheckPermission(ctx, "alice", "s3:GetObject", "arn:aws:s3:::other-bucket/file.txt")
		requireNoError(t, err)
		assertEqual(t, false, allowed)
	})
}

func TestCheckPermissionExplicitDeny(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "bob"})

	allowPolicy, _ := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "allow-all",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Allow", "Action": "*", "Resource": "*"},
		}),
	})

	denyPolicy, _ := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "deny-delete",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Deny", "Action": "s3:DeleteObject", "Resource": "*"},
		}),
	})

	_ = m.AttachUserPolicy(ctx, "bob", allowPolicy.ARN)
	_ = m.AttachUserPolicy(ctx, "bob", denyPolicy.ARN)

	t.Run("explicit deny overrides allow", func(t *testing.T) {
		allowed, err := m.CheckPermission(ctx, "bob", "s3:DeleteObject", "arn:aws:s3:::bucket/key")
		requireNoError(t, err)
		assertEqual(t, false, allowed)
	})

	t.Run("other actions still allowed", func(t *testing.T) {
		allowed, err := m.CheckPermission(ctx, "bob", "s3:GetObject", "arn:aws:s3:::bucket/key")
		requireNoError(t, err)
		assertEqual(t, true, allowed)
	})
}

func TestCheckPermissionWildcard(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "admin"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "admin-all",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Allow", "Action": "*", "Resource": "*"},
		}),
	})
	_ = m.AttachUserPolicy(ctx, "admin", p.ARN)

	allowed, err := m.CheckPermission(ctx, "admin", "ec2:RunInstances", "arn:aws:ec2:us-east-1:123:instance/i-123")
	requireNoError(t, err)
	assertEqual(t, true, allowed)
}

func TestCheckPermissionNoPolicies(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "nobody"})

	allowed, err := m.CheckPermission(ctx, "nobody", "s3:GetObject", "*")
	requireNoError(t, err)
	assertEqual(t, false, allowed)
}

func TestWildcardMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   string
		expect  bool
	}{
		{name: "exact match", pattern: "s3:GetObject", value: "s3:GetObject", expect: true},
		{name: "no match", pattern: "s3:GetObject", value: "s3:PutObject", expect: false},
		{name: "star matches all", pattern: "*", value: "anything", expect: true},
		{name: "prefix wildcard", pattern: "s3:*", value: "s3:GetObject", expect: true},
		{name: "suffix wildcard", pattern: "arn:aws:s3:::my-bucket/*", value: "arn:aws:s3:::my-bucket/file.txt", expect: true},
		{name: "no suffix match", pattern: "arn:aws:s3:::my-bucket/*", value: "arn:aws:s3:::other/file.txt", expect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := wildcardMatch(tc.pattern, tc.value)
			assertEqual(t, tc.expect, result)
		})
	}
}

func TestCreateGroup(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.GroupConfig
		setup     func(m *Mock)
		expectErr bool
	}{
		{name: "success", cfg: driver.GroupConfig{Name: "developers"}},
		{name: "empty name", cfg: driver.GroupConfig{}, expectErr: true},
		{
			name: "duplicate",
			cfg:  driver.GroupConfig{Name: "developers"},
			setup: func(m *Mock) {
				_, _ = m.CreateGroup(context.Background(), driver.GroupConfig{Name: "developers"})
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			info, err := m.CreateGroup(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, "developers", info.Name)
			assertNotEmpty(t, info.ARN)
		})
	}
}

func TestDeleteGroup(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "devs"})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteGroup(ctx, "devs")
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteGroup(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestGetGroup(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "devs"})

	t.Run("found", func(t *testing.T) {
		info, err := m.GetGroup(ctx, "devs")
		requireNoError(t, err)
		assertEqual(t, "devs", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetGroup(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestListGroups(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "g1"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "g2"})

	groups, err := m.ListGroups(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(groups))
}

func TestAddUserToGroup(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "devs"})

	t.Run("success", func(t *testing.T) {
		err := m.AddUserToGroup(ctx, "alice", "devs")
		requireNoError(t, err)

		groups, err := m.ListGroupsForUser(ctx, "alice")
		requireNoError(t, err)
		assertEqual(t, 1, len(groups))
	})

	t.Run("user not found", func(t *testing.T) {
		err := m.AddUserToGroup(ctx, "nope", "devs")
		assertError(t, err, true)
	})

	t.Run("group not found", func(t *testing.T) {
		err := m.AddUserToGroup(ctx, "alice", "nope")
		assertError(t, err, true)
	})
}

func TestRemoveUserFromGroup(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "devs"})
	_ = m.AddUserToGroup(ctx, "alice", "devs")

	t.Run("success", func(t *testing.T) {
		err := m.RemoveUserFromGroup(ctx, "alice", "devs")
		requireNoError(t, err)

		groups, err := m.ListGroupsForUser(ctx, "alice")
		requireNoError(t, err)
		assertEqual(t, 0, len(groups))
	})

	t.Run("not a member", func(t *testing.T) {
		err := m.RemoveUserFromGroup(ctx, "alice", "devs")
		assertError(t, err, true)
	})

	t.Run("group not found", func(t *testing.T) {
		err := m.RemoveUserFromGroup(ctx, "alice", "nope")
		assertError(t, err, true)
	})
}

func TestListGroupsForUser(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "g1"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "g2"})
	_ = m.AddUserToGroup(ctx, "alice", "g1")
	_ = m.AddUserToGroup(ctx, "alice", "g2")

	t.Run("success", func(t *testing.T) {
		groups, err := m.ListGroupsForUser(ctx, "alice")
		requireNoError(t, err)
		assertEqual(t, 2, len(groups))
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.ListGroupsForUser(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestCreateAccessKey(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})

	t.Run("success", func(t *testing.T) {
		ak, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})
		requireNoError(t, err)
		assertNotEmpty(t, ak.AccessKeyID)
		assertNotEmpty(t, ak.SecretAccessKey)
		assertEqual(t, "alice", ak.UserName)
		assertEqual(t, "Active", ak.Status)
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "nope"})
		assertError(t, err, true)
	})

	t.Run("empty user name", func(t *testing.T) {
		_, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{})
		assertError(t, err, true)
	})
}

func TestDeleteAccessKey(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	ak, _ := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteAccessKey(ctx, "alice", ak.AccessKeyID)
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteAccessKey(ctx, "alice", "nonexistent-key")
		assertError(t, err, true)
	})
}

func TestListAccessKeys(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})
	_, _ = m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})

	t.Run("success", func(t *testing.T) {
		keys, err := m.ListAccessKeys(ctx, "alice")
		requireNoError(t, err)
		assertEqual(t, 2, len(keys))
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.ListAccessKeys(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestListAttachedRolePolicies(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_ = m.AttachRolePolicy(ctx, "role1", p.ARN)

	t.Run("success", func(t *testing.T) {
		policies, err := m.ListAttachedRolePolicies(ctx, "role1")
		requireNoError(t, err)
		assertEqual(t, 1, len(policies))
	})

	t.Run("role not found", func(t *testing.T) {
		_, err := m.ListAttachedRolePolicies(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestListAttachedUserPolicies(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_ = m.AttachUserPolicy(ctx, "alice", p.ARN)

	t.Run("success", func(t *testing.T) {
		policies, err := m.ListAttachedUserPolicies(ctx, "alice")
		requireNoError(t, err)
		assertEqual(t, 1, len(policies))
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.ListAttachedUserPolicies(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestCheckPermissionViaRole(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "reader"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "read-policy",
		PolicyDocument: makePolicyDoc([]map[string]any{
			{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"},
		}),
	})
	_ = m.AttachRolePolicy(ctx, "reader", p.ARN)

	allowed, err := m.CheckPermission(ctx, "reader", "s3:GetObject", "arn:aws:s3:::bucket/key")
	requireNoError(t, err)
	assertEqual(t, true, allowed)
}

// --- test helpers ---

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}
