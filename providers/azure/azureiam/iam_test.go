package azureiam

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/iam/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"))

	return New(opts)
}

func TestCreateUser(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.UserConfig
		wantErr bool
		errMsg  string
	}{
		{name: "success", cfg: driver.UserConfig{Name: "alice", Tags: map[string]string{"team": "dev"}}},
		{name: "empty name", cfg: driver.UserConfig{Name: ""}, wantErr: true, errMsg: "user name is required"},
		{name: "duplicate", cfg: driver.UserConfig{Name: "alice"}, wantErr: true, errMsg: "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateUser(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, "alice", info.Name)
				assert.NotEmpty(t, info.ID)
				assert.NotEmpty(t, info.ARN)
				assert.Equal(t, "/", info.Path)
				assert.NotEmpty(t, info.CreatedAt)
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
		name    string
		user    string
		wantErr bool
		errMsg  string
	}{
		{name: "success", user: "alice"},
		{name: "not found", user: "missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteUser(ctx, tt.user)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice", Path: "/admins/"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := m.GetUser(ctx, "alice")
		require.NoError(t, err)
		assert.Equal(t, "alice", info.Name)
		assert.Equal(t, "/admins/", info.Path)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetUser(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListUsers(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "bob"})

	users, err := m.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 2)
}

func TestCreateRole(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.RoleConfig
		wantErr bool
		errMsg  string
	}{
		{name: "success", cfg: driver.RoleConfig{Name: "admin-role", AssumeRolePolicyDoc: "{}", Tags: map[string]string{"env": "prod"}}},
		{name: "empty name", cfg: driver.RoleConfig{Name: ""}, wantErr: true, errMsg: "role name is required"},
		{name: "duplicate", cfg: driver.RoleConfig{Name: "admin-role"}, wantErr: true, errMsg: "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateRole(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, "admin-role", info.Name)
				assert.NotEmpty(t, info.ID)
				assert.NotEmpty(t, info.ARN)
				assert.Equal(t, "/", info.Path)
			}
		})
	}
}

func TestDeleteRole(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})

	tests := []struct {
		name    string
		role    string
		wantErr bool
		errMsg  string
	}{
		{name: "success", role: "role1"},
		{name: "not found", role: "missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteRole(ctx, tt.role)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
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
		name    string
		cfg     driver.PolicyConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.PolicyConfig{Name: "read-policy", PolicyDocument: `{"Version":"2012-10-17","Statement":[]}`, Description: "test policy"},
		},
		{
			name:    "empty name",
			cfg:     driver.PolicyConfig{Name: ""},
			wantErr: true, errMsg: "policy name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreatePolicy(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, "read-policy", info.Name)
				assert.NotEmpty(t, info.ARN)
				assert.NotEmpty(t, info.ID)
			}
		})
	}
}

func TestDeletePolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	p, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	require.NoError(t, err)

	tests := []struct {
		name    string
		arn     string
		wantErr bool
		errMsg  string
	}{
		{name: "success", arn: p.ARN},
		{name: "not found", arn: "missing-arn", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeletePolicy(ctx, tt.arn)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestAttachUserPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})

	tests := []struct {
		name    string
		user    string
		arn     string
		wantErr bool
		errMsg  string
	}{
		{name: "success", user: "alice", arn: p.ARN},
		{name: "user not found", user: "missing", arn: p.ARN, wantErr: true, errMsg: "user"},
		{name: "policy not found", user: "alice", arn: "bad-arn", wantErr: true, errMsg: "policy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.AttachUserPolicy(ctx, tt.user, tt.arn)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				policies, err := m.ListAttachedUserPolicies(ctx, "alice")
				require.NoError(t, err)
				assert.Contains(t, policies, p.ARN)
			}
		})
	}
}

func TestAttachRolePolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})

	tests := []struct {
		name    string
		role    string
		arn     string
		wantErr bool
		errMsg  string
	}{
		{name: "success", role: "role1", arn: p.ARN},
		{name: "role not found", role: "missing", arn: p.ARN, wantErr: true, errMsg: "role"},
		{name: "policy not found", role: "role1", arn: "bad-arn", wantErr: true, errMsg: "policy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.AttachRolePolicy(ctx, tt.role, tt.arn)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				policies, err := m.ListAttachedRolePolicies(ctx, "role1")
				require.NoError(t, err)
				assert.Contains(t, policies, p.ARN)
			}
		})
	}
}

func TestDetachUserPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	require.NoError(t, m.AttachUserPolicy(ctx, "alice", p.ARN))

	tests := []struct {
		name    string
		user    string
		arn     string
		wantErr bool
		errMsg  string
	}{
		{name: "success", user: "alice", arn: p.ARN},
		{name: "user not found", user: "missing", arn: p.ARN, wantErr: true, errMsg: "not found"},
		{name: "policy not attached", user: "alice", arn: p.ARN, wantErr: true, errMsg: "not attached"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DetachUserPolicy(ctx, tt.user, tt.arn)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDetachRolePolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	require.NoError(t, m.AttachRolePolicy(ctx, "role1", p.ARN))

	tests := []struct {
		name    string
		role    string
		arn     string
		wantErr bool
		errMsg  string
	}{
		{name: "success", role: "role1", arn: p.ARN},
		{name: "role not found", role: "missing", arn: p.ARN, wantErr: true, errMsg: "not found"},
		{name: "policy not attached", role: "role1", arn: p.ARN, wantErr: true, errMsg: "not attached"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DetachRolePolicy(ctx, tt.role, tt.arn)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestCheckPermission(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateUser(ctx, driver.UserConfig{Name: "alice"})

	allowPolicy, _ := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "allow-s3",
		PolicyDocument: `{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Action": "s3:GetObject",
					"Resource": "arn:aws:s3:::my-bucket/*"
				}
			]
		}`,
	})

	denyPolicy, _ := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "deny-s3",
		PolicyDocument: `{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Deny",
					"Action": "s3:*",
					"Resource": "*"
				}
			]
		}`,
	})

	wildcardPolicy, _ := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "wildcard-policy",
		PolicyDocument: `{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Action": ["ec2:*", "s3:List*"],
					"Resource": "*"
				}
			]
		}`,
	})

	tests := []struct {
		name      string
		setup     func()
		principal string
		action    string
		resource  string
		want      bool
	}{
		{
			name:      "no policies - deny by default",
			principal: "alice", action: "s3:GetObject", resource: "arn:aws:s3:::bucket/key",
			want: false,
		},
		{
			name: "allow policy attached",
			setup: func() {
				_ = m.AttachUserPolicy(ctx, "alice", allowPolicy.ARN)
			},
			principal: "alice", action: "s3:GetObject", resource: "arn:aws:s3:::my-bucket/file.txt",
			want: true,
		},
		{
			name:      "action not matched",
			principal: "alice", action: "s3:PutObject", resource: "arn:aws:s3:::my-bucket/file.txt",
			want: false,
		},
		{
			name: "explicit deny overrides allow",
			setup: func() {
				_ = m.AttachUserPolicy(ctx, "alice", denyPolicy.ARN)
			},
			principal: "alice", action: "s3:GetObject", resource: "arn:aws:s3:::my-bucket/file.txt",
			want: false,
		},
		{
			name: "wildcard action match",
			setup: func() {
				// Clean up previous policies, create fresh user
				_ = m.DetachUserPolicy(ctx, "alice", allowPolicy.ARN)
				_ = m.DetachUserPolicy(ctx, "alice", denyPolicy.ARN)
				_ = m.AttachUserPolicy(ctx, "alice", wildcardPolicy.ARN)
			},
			principal: "alice", action: "ec2:RunInstances", resource: "*",
			want: true,
		},
		{
			name:      "wildcard prefix match",
			principal: "alice", action: "s3:ListBuckets", resource: "*",
			want: true,
		},
		{
			name:      "principal with no policies",
			principal: "unknown-principal", action: "s3:GetObject", resource: "*",
			want: false,
		},
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
		{name: "star matches all", pattern: "*", value: "anything", want: true},
		{name: "exact match", pattern: "s3:GetObject", value: "s3:GetObject", want: true},
		{name: "no match", pattern: "s3:GetObject", value: "s3:PutObject", want: false},
		{name: "prefix wildcard", pattern: "s3:*", value: "s3:GetObject", want: true},
		{name: "suffix wildcard", pattern: "*Object", value: "s3:GetObject", want: true},
		{name: "middle wildcard", pattern: "s3:*Object", value: "s3:GetObject", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, wildcardMatch(tt.pattern, tt.value))
		})
	}
}

func TestListRoles(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1"})
	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role2"})

	roles, err := m.ListRoles(ctx)
	require.NoError(t, err)
	assert.Len(t, roles, 2)
}

func TestListPolicies(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_, _ = m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol2", PolicyDocument: "{}"})

	policies, err := m.ListPolicies(ctx)
	require.NoError(t, err)
	assert.Len(t, policies, 2)
}

func TestGetRole(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _ = m.CreateRole(ctx, driver.RoleConfig{Name: "role1", Path: "/service/"})

	t.Run("success", func(t *testing.T) {
		info, err := m.GetRole(ctx, "role1")
		require.NoError(t, err)
		assert.Equal(t, "role1", info.Name)
		assert.Equal(t, "/service/", info.Path)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetRole(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestCreateGroup(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		cfg     driver.GroupConfig
		setup   func(m *Mock)
		wantErr bool
		errMsg  string
	}{
		{name: "success", cfg: driver.GroupConfig{Name: "developers"}},
		{name: "empty name", cfg: driver.GroupConfig{}, wantErr: true, errMsg: "required"},
		{
			name: "duplicate",
			cfg:  driver.GroupConfig{Name: "developers"},
			setup: func(m *Mock) {
				_, _ = m.CreateGroup(ctx, driver.GroupConfig{Name: "developers"})
			},
			wantErr: true, errMsg: "already exists",
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
				assert.Contains(t, err.Error(), tt.errMsg)
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
		name    string
		group   string
		wantErr bool
		errMsg  string
	}{
		{name: "success", group: "devs"},
		{name: "not found", group: "missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteGroup(ctx, tt.group)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
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
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_ = m.AttachRolePolicy(ctx, "role1", p.ARN)

	t.Run("success", func(t *testing.T) {
		policies, err := m.ListAttachedRolePolicies(ctx, "role1")
		require.NoError(t, err)
		assert.Len(t, policies, 1)
		assert.Contains(t, policies, p.ARN)
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
	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: "{}"})
	_ = m.AttachUserPolicy(ctx, "alice", p.ARN)

	t.Run("success", func(t *testing.T) {
		policies, err := m.ListAttachedUserPolicies(ctx, "alice")
		require.NoError(t, err)
		assert.Len(t, policies, 1)
		assert.Contains(t, policies, p.ARN)
	})

	t.Run("user not found", func(t *testing.T) {
		_, err := m.ListAttachedUserPolicies(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestGetPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	p, _ := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "pol1", PolicyDocument: `{"Version":"2012-10-17"}`})

	t.Run("success", func(t *testing.T) {
		info, err := m.GetPolicy(ctx, p.ARN)
		require.NoError(t, err)
		assert.Equal(t, "pol1", info.Name)
		assert.Equal(t, `{"Version":"2012-10-17"}`, info.PolicyDocument)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetPolicy(ctx, "missing-arn")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}
