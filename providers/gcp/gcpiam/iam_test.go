package gcpiam

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/iam/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	return New(opts)
}

func makePolicyDoc(effect, action, resource string) string {
	doc := policyDoc{
		Version: "2012-10-17",
		Statement: []policyStatement{
			{Effect: effect, Action: action, Resource: resource},
		},
	}
	b, _ := json.Marshal(doc)

	return string(b)
}

func TestCreateUser(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.UserConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.UserConfig{Name: "alice", Tags: map[string]string{"team": "eng"}}},
		{name: "duplicate", cfg: driver.UserConfig{Name: "alice"}, wantErr: true, errSubstr: "already exists"},
		{name: "empty name", cfg: driver.UserConfig{}, wantErr: true, errSubstr: "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateUser(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "alice", info.Name)
				assert.NotEmpty(t, info.ID)
				assert.NotEmpty(t, info.ARN)
				assert.Contains(t, info.ARN, "test-project")
			}
		})
	}
}

func TestDeleteUser(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		userName  string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", userName: "alice"},
		{name: "not found", userName: "bob", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteUser(ctx, tt.userName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestCreateRole(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.RoleConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.RoleConfig{Name: "admin", AssumeRolePolicyDoc: "{}"}},
		{name: "duplicate", cfg: driver.RoleConfig{Name: "admin"}, wantErr: true, errSubstr: "already exists"},
		{name: "empty name", cfg: driver.RoleConfig{}, wantErr: true, errSubstr: "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateRole(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "admin", info.Name)
				assert.NotEmpty(t, info.ARN)
			}
		})
	}
}

func TestDeleteRole(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		roleName  string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", roleName: "role1"},
		{name: "not found", roleName: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteRole(ctx, tt.roleName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestCreatePolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.PolicyConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.PolicyConfig{Name: "read-policy", PolicyDocument: "{}", Description: "Read only"}},
		{name: "duplicate", cfg: driver.PolicyConfig{Name: "read-policy"}, wantErr: true, errSubstr: "already exists"},
		{name: "empty name", cfg: driver.PolicyConfig{}, wantErr: true, errSubstr: "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreatePolicy(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "read-policy", info.Name)
				assert.NotEmpty(t, info.ARN)
			}
		})
	}
}

func TestDeletePolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1", PolicyDocument: "{}"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		arn       string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", arn: pol.ARN},
		{name: "not found", arn: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeletePolicy(ctx, tt.arn)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestAttachUserPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1", PolicyDocument: "{}"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		user      string
		polARN    string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", user: "alice", polARN: pol.ARN},
		{name: "user not found", user: "bob", polARN: pol.ARN, wantErr: true, errSubstr: "not found"},
		{name: "policy not found", user: "alice", polARN: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.AttachUserPolicy(ctx, tt.user, tt.polARN)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				policies, listErr := m.ListAttachedUserPolicies(ctx, "alice")
				require.NoError(t, listErr)
				assert.Contains(t, policies, pol.ARN)
			}
		})
	}
}

func TestAttachRolePolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	require.NoError(t, err)

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1", PolicyDocument: "{}"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		role      string
		polARN    string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", role: "role1", polARN: pol.ARN},
		{name: "role not found", role: "missing", polARN: pol.ARN, wantErr: true, errSubstr: "not found"},
		{name: "policy not found", role: "role1", polARN: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.AttachRolePolicy(ctx, tt.role, tt.polARN)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				policies, listErr := m.ListAttachedRolePolicies(ctx, "role1")
				require.NoError(t, listErr)
				assert.Contains(t, policies, pol.ARN)
			}
		})
	}
}

func TestCheckPermission(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)

	allowDoc := makePolicyDoc("Allow", "s3:GetObject", "arn:aws:s3:::my-bucket/*")
	denyDoc := makePolicyDoc("Deny", "s3:*", "arn:aws:s3:::my-bucket/*")
	wildcardDoc := makePolicyDoc("Allow", "s3:*", "*")

	allowPol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "allow-s3", PolicyDocument: allowDoc})
	require.NoError(t, err)
	denyPol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "deny-s3", PolicyDocument: denyDoc})
	require.NoError(t, err)
	wildcardPol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "wildcard-s3", PolicyDocument: wildcardDoc})
	require.NoError(t, err)

	require.NoError(t, m.AttachUserPolicy(ctx, "alice", allowPol.ARN))

	tests := []struct {
		name      string
		setup     func()
		principal string
		action    string
		resource  string
		want      bool
	}{
		{name: "allowed action", principal: "alice", action: "s3:GetObject", resource: "arn:aws:s3:::my-bucket/key", want: true},
		{name: "denied by default", principal: "alice", action: "s3:PutObject", resource: "arn:aws:s3:::my-bucket/key", want: false},
		{name: "no policies", principal: "nobody", action: "s3:GetObject", resource: "arn:aws:s3:::my-bucket/key", want: false},
		{name: "explicit deny overrides allow", setup: func() {
			require.NoError(t, m.AttachUserPolicy(ctx, "alice", denyPol.ARN))
		}, principal: "alice", action: "s3:GetObject", resource: "arn:aws:s3:::my-bucket/key", want: false},
		{name: "wildcard action", setup: func() {
			_, wErr := m.CreateUser(ctx, driver.UserConfig{Name: "bob"})
			require.NoError(t, wErr)
			require.NoError(t, m.AttachUserPolicy(ctx, "bob", wildcardPol.ARN))
		}, principal: "bob", action: "s3:DeleteObject", resource: "anything", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			allowed, err := m.CheckPermission(ctx, tt.principal, tt.action, tt.resource)
			require.NoError(t, err)
			assert.Equal(t, tt.want, allowed)
		})
	}
}

func TestWildcardMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		{name: "exact match", pattern: "s3:GetObject", value: "s3:GetObject", want: true},
		{name: "star matches all", pattern: "*", value: "anything", want: true},
		{name: "prefix wildcard", pattern: "s3:*", value: "s3:GetObject", want: true},
		{name: "no match", pattern: "s3:Get*", value: "s3:PutObject", want: false},
		{name: "middle wildcard", pattern: "arn:aws:s3:::*/*", value: "arn:aws:s3:::bucket/key", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, wildcardMatch(tt.pattern, tt.value))
		})
	}
}

func TestListUsers(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)
	_, err = m.CreateUser(ctx, driver.UserConfig{Name: "bob"})
	require.NoError(t, err)

	users, err := m.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 2)
}

func TestListRoles(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateRole(ctx, driver.RoleConfig{Name: "r1"})
	require.NoError(t, err)

	roles, err := m.ListRoles(ctx)
	require.NoError(t, err)
	assert.Len(t, roles, 1)
}

func TestListPolicies(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1", PolicyDocument: "{}"})
	require.NoError(t, err)

	policies, err := m.ListPolicies(ctx)
	require.NoError(t, err)
	assert.Len(t, policies, 1)
}

func TestCreateGroup(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		cfg       driver.GroupConfig
		setup     func(m *Mock)
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.GroupConfig{Name: "developers"}},
		{name: "empty name", cfg: driver.GroupConfig{}, wantErr: true, errSubstr: "required"},
		{
			name: "duplicate",
			cfg:  driver.GroupConfig{Name: "developers"},
			setup: func(m *Mock) {
				_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "developers"})
			},
			wantErr: true, errSubstr: "already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			if tt.setup != nil {
				tt.setup(m)
			}
			info, err := m.CreateGroup(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "developers", info.Name)
				assert.NotEmpty(t, info.ARN)
			}
		})
	}
}

func TestDeleteGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateGroup(ctx, driver.GroupConfig{Name: "devs"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		group     string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", group: "devs"},
		{name: "not found", group: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteGroup(ctx, tt.group)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateGroup(ctx, driver.GroupConfig{Name: "devs"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := m.GetGroup(ctx, "devs")
		require.NoError(t, err)
		assert.Equal(t, "devs", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetGroup(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListGroups(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "g1"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "g2"})

	groups, err := m.ListGroups(ctx)
	require.NoError(t, err)
	assert.Len(t, groups, 2)
}

func TestAddUserToGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "devs"})

	t.Run("success", func(t *testing.T) {
		err := m.AddUserToGroup(ctx, "alice", "devs")
		require.NoError(t, err)

		groups, err := m.ListGroupsForUser(ctx, "alice")
		require.NoError(t, err)
		assert.Len(t, groups, 1)
	})

	t.Run("user not found", func(t *testing.T) {
		err := m.AddUserToGroup(ctx, "missing", "devs")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("group not found", func(t *testing.T) {
		err := m.AddUserToGroup(ctx, "alice", "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRemoveUserFromGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "devs"})
	_ = m.AddUserToGroup(ctx, "alice", "devs")

	t.Run("success", func(t *testing.T) {
		err := m.RemoveUserFromGroup(ctx, "alice", "devs")
		require.NoError(t, err)

		groups, err := m.ListGroupsForUser(ctx, "alice")
		require.NoError(t, err)
		assert.Len(t, groups, 0)
	})

	t.Run("not a member", func(t *testing.T) {
		err := m.RemoveUserFromGroup(ctx, "alice", "devs")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a member")
	})

	t.Run("group not found", func(t *testing.T) {
		err := m.RemoveUserFromGroup(ctx, "alice", "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListGroupsForUser(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "g1"})
	_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "g2"})
	_ = m.AddUserToGroup(ctx, "alice", "g1")
	_ = m.AddUserToGroup(ctx, "alice", "g2")

	t.Run("success", func(t *testing.T) {
		groups, err := m.ListGroupsForUser(ctx, "alice")
		require.NoError(t, err)
		assert.Len(t, groups, 2)
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.ListGroupsForUser(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestCreateAccessKey(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})

	t.Run("success", func(t *testing.T) {
		ak, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})
		require.NoError(t, err)
		assert.NotEmpty(t, ak.AccessKeyID)
		assert.NotEmpty(t, ak.SecretAccessKey)
		assert.Equal(t, "alice", ak.UserName)
		assert.Equal(t, "Active", ak.Status)
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "missing"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("empty user name", func(t *testing.T) {
		_, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})
}

func TestDeleteAccessKey(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	ak, _ := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteAccessKey(ctx, "alice", ak.AccessKeyID)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteAccessKey(ctx, "alice", "nonexistent-key")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListAccessKeys(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})
	_, _ = m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})

	t.Run("success", func(t *testing.T) {
		keys, err := m.ListAccessKeys(ctx, "alice")
		require.NoError(t, err)
		assert.Len(t, keys, 2)
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.ListAccessKeys(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListAttachedRolePolicies(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	pol, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_ = m.AttachRolePolicy(ctx, "role1", pol.ARN)

	t.Run("success", func(t *testing.T) {
		policies, err := m.ListAttachedRolePolicies(ctx, "role1")
		require.NoError(t, err)
		assert.Len(t, policies, 1)
		assert.Contains(t, policies, pol.ARN)
	})

	t.Run("role not found", func(t *testing.T) {
		_, err := m.ListAttachedRolePolicies(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListAttachedUserPolicies(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	pol, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_ = m.AttachUserPolicy(ctx, "alice", pol.ARN)

	t.Run("success", func(t *testing.T) {
		policies, err := m.ListAttachedUserPolicies(ctx, "alice")
		require.NoError(t, err)
		assert.Len(t, policies, 1)
		assert.Contains(t, policies, pol.ARN)
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.ListAttachedUserPolicies(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDetachUserPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)
	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1", PolicyDocument: "{}"})
	require.NoError(t, err)
	require.NoError(t, m.AttachUserPolicy(ctx, "alice", pol.ARN))

	tests := []struct {
		name      string
		user      string
		polARN    string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", user: "alice", polARN: pol.ARN},
		{name: "user not found", user: "missing", polARN: pol.ARN, wantErr: true, errSubstr: "not found"},
		{name: "policy not attached", user: "alice", polARN: pol.ARN, wantErr: true, errSubstr: "not attached"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DetachUserPolicy(ctx, tt.user, tt.polARN)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)

	u, err := m.GetUser(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, "alice", u.Name)

	_, err = m.GetUser(ctx, "missing")
	require.Error(t, err)
}

func TestGetRole(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateRole(ctx, driver.RoleConfig{Name: "admin", AssumeRolePolicyDoc: "{}"})
	require.NoError(t, err)

	r, err := m.GetRole(ctx, "admin")
	require.NoError(t, err)
	assert.Equal(t, "admin", r.Name)

	_, err = m.GetRole(ctx, "missing")
	require.Error(t, err)
}

func TestGetPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	p, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name:           "read-only",
		PolicyDocument: makePolicyDoc("Allow", "s3:GetObject", "*"),
	})
	require.NoError(t, err)

	got, err := m.GetPolicy(ctx, p.ARN)
	require.NoError(t, err)
	assert.Equal(t, "read-only", got.Name)

	_, err = m.GetPolicy(ctx, "arn:fake:missing")
	require.Error(t, err)
}

func TestDetachRolePolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateRole(ctx, driver.RoleConfig{Name: "svc-role", AssumeRolePolicyDoc: "{}"})
	require.NoError(t, err)

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name:           "pol1",
		PolicyDocument: makePolicyDoc("Allow", "*", "*"),
	})
	require.NoError(t, err)

	err = m.AttachRolePolicy(ctx, "svc-role", pol.ARN)
	require.NoError(t, err)

	err = m.DetachRolePolicy(ctx, "svc-role", pol.ARN)
	require.NoError(t, err)

	policies, err := m.ListAttachedRolePolicies(ctx, "svc-role")
	require.NoError(t, err)
	assert.Equal(t, 0, len(policies))
}
