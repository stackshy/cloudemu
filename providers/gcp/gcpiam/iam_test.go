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
