package iam

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/iam/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements driver.IAM for testing the portable wrapper.
type mockDriver struct {
	users          map[string]*driver.UserInfo
	roles          map[string]*driver.RoleInfo
	policies       map[string]*driver.PolicyInfo
	userPolicies   map[string][]string // userName -> []policyARN
	rolePolicies   map[string][]string // roleName -> []policyARN
	seq            int
}

func newMockDriver() *mockDriver {
	return &mockDriver{
		users:        make(map[string]*driver.UserInfo),
		roles:        make(map[string]*driver.RoleInfo),
		policies:     make(map[string]*driver.PolicyInfo),
		userPolicies: make(map[string][]string),
		rolePolicies: make(map[string][]string),
	}
}

func (m *mockDriver) nextID(prefix string) string {
	m.seq++

	return fmt.Sprintf("%s-%d", prefix, m.seq)
}

func (m *mockDriver) CreateUser(_ context.Context, config driver.UserConfig) (*driver.UserInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	if _, ok := m.users[config.Name]; ok {
		return nil, fmt.Errorf("already exists")
	}

	info := &driver.UserInfo{
		Name: config.Name,
		ID:   m.nextID("user"),
		ARN:  "arn:aws:iam::123456789012:user/" + config.Name,
		Path: config.Path,
		Tags: config.Tags,
	}
	m.users[config.Name] = info

	return info, nil
}

func (m *mockDriver) DeleteUser(_ context.Context, name string) error {
	if _, ok := m.users[name]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.users, name)
	delete(m.userPolicies, name)

	return nil
}

func (m *mockDriver) GetUser(_ context.Context, name string) (*driver.UserInfo, error) {
	info, ok := m.users[name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return info, nil
}

func (m *mockDriver) ListUsers(_ context.Context) ([]driver.UserInfo, error) {
	result := make([]driver.UserInfo, 0, len(m.users))
	for _, info := range m.users {
		result = append(result, *info)
	}

	return result, nil
}

func (m *mockDriver) CreateRole(_ context.Context, config driver.RoleConfig) (*driver.RoleInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	if _, ok := m.roles[config.Name]; ok {
		return nil, fmt.Errorf("already exists")
	}

	info := &driver.RoleInfo{
		Name: config.Name,
		ID:   m.nextID("role"),
		ARN:  "arn:aws:iam::123456789012:role/" + config.Name,
		Path: config.Path,
	}
	m.roles[config.Name] = info

	return info, nil
}

func (m *mockDriver) DeleteRole(_ context.Context, name string) error {
	if _, ok := m.roles[name]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.roles, name)
	delete(m.rolePolicies, name)

	return nil
}

func (m *mockDriver) GetRole(_ context.Context, name string) (*driver.RoleInfo, error) {
	info, ok := m.roles[name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return info, nil
}

func (m *mockDriver) ListRoles(_ context.Context) ([]driver.RoleInfo, error) {
	result := make([]driver.RoleInfo, 0, len(m.roles))
	for _, info := range m.roles {
		result = append(result, *info)
	}

	return result, nil
}

func (m *mockDriver) CreatePolicy(_ context.Context, config driver.PolicyConfig) (*driver.PolicyInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	arn := "arn:aws:iam::123456789012:policy/" + config.Name

	if _, ok := m.policies[arn]; ok {
		return nil, fmt.Errorf("already exists")
	}

	info := &driver.PolicyInfo{
		Name:           config.Name,
		ID:             m.nextID("policy"),
		ARN:            arn,
		Path:           config.Path,
		PolicyDocument: config.PolicyDocument,
		Description:    config.Description,
	}
	m.policies[arn] = info

	return info, nil
}

func (m *mockDriver) DeletePolicy(_ context.Context, arn string) error {
	if _, ok := m.policies[arn]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.policies, arn)

	return nil
}

func (m *mockDriver) GetPolicy(_ context.Context, arn string) (*driver.PolicyInfo, error) {
	info, ok := m.policies[arn]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return info, nil
}

func (m *mockDriver) ListPolicies(_ context.Context) ([]driver.PolicyInfo, error) {
	result := make([]driver.PolicyInfo, 0, len(m.policies))
	for _, info := range m.policies {
		result = append(result, *info)
	}

	return result, nil
}

func (m *mockDriver) AttachUserPolicy(_ context.Context, userName, policyARN string) error {
	if _, ok := m.users[userName]; !ok {
		return fmt.Errorf("user not found")
	}

	m.userPolicies[userName] = append(m.userPolicies[userName], policyARN)

	return nil
}

func (m *mockDriver) DetachUserPolicy(_ context.Context, userName, _ string) error {
	if _, ok := m.users[userName]; !ok {
		return fmt.Errorf("user not found")
	}

	return nil
}

func (m *mockDriver) AttachRolePolicy(_ context.Context, roleName, policyARN string) error {
	if _, ok := m.roles[roleName]; !ok {
		return fmt.Errorf("role not found")
	}

	m.rolePolicies[roleName] = append(m.rolePolicies[roleName], policyARN)

	return nil
}

func (m *mockDriver) DetachRolePolicy(_ context.Context, roleName, _ string) error {
	if _, ok := m.roles[roleName]; !ok {
		return fmt.Errorf("role not found")
	}

	return nil
}

func (m *mockDriver) ListAttachedUserPolicies(_ context.Context, userName string) ([]string, error) {
	if _, ok := m.users[userName]; !ok {
		return nil, fmt.Errorf("user not found")
	}

	return m.userPolicies[userName], nil
}

func (m *mockDriver) ListAttachedRolePolicies(_ context.Context, roleName string) ([]string, error) {
	if _, ok := m.roles[roleName]; !ok {
		return nil, fmt.Errorf("role not found")
	}

	return m.rolePolicies[roleName], nil
}

func (m *mockDriver) CheckPermission(_ context.Context, principal, _, _ string) (bool, error) {
	if _, ok := m.users[principal]; !ok {
		if _, ok2 := m.roles[principal]; !ok2 {
			return false, fmt.Errorf("principal not found")
		}
	}

	return true, nil
}

func newTestIAM(opts ...Option) *IAM {
	return NewIAM(newMockDriver(), opts...)
}

func TestNewIAM(t *testing.T) {
	i := newTestIAM()
	require.NotNil(t, i)
	require.NotNil(t, i.driver)
}

func TestCreateUser(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := i.CreateUser(ctx, driver.UserConfig{Name: "alice"})
		require.NoError(t, err)
		assert.Equal(t, "alice", info.Name)
		assert.NotEmpty(t, info.ARN)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := i.CreateUser(ctx, driver.UserConfig{})
		require.Error(t, err)
	})
}

func TestDeleteUser(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "bob"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := i.DeleteUser(ctx, "bob")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := i.DeleteUser(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetUser(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "carol"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := i.GetUser(ctx, "carol")
		require.NoError(t, err)
		assert.Equal(t, "carol", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := i.GetUser(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListUsers(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	users, err := i.ListUsers(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(users))

	_, err = i.CreateUser(ctx, driver.UserConfig{Name: "u1"})
	require.NoError(t, err)

	_, err = i.CreateUser(ctx, driver.UserConfig{Name: "u2"})
	require.NoError(t, err)

	users, err = i.ListUsers(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(users))
}

func TestCreateRole(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := i.CreateRole(ctx, driver.RoleConfig{Name: "admin"})
		require.NoError(t, err)
		assert.Equal(t, "admin", info.Name)
		assert.NotEmpty(t, info.ARN)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := i.CreateRole(ctx, driver.RoleConfig{})
		require.Error(t, err)
	})
}

func TestDeleteRole(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	_, err := i.CreateRole(ctx, driver.RoleConfig{Name: "del-role"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := i.DeleteRole(ctx, "del-role")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := i.DeleteRole(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetRole(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	_, err := i.CreateRole(ctx, driver.RoleConfig{Name: "get-role"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := i.GetRole(ctx, "get-role")
		require.NoError(t, err)
		assert.Equal(t, "get-role", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := i.GetRole(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListRoles(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	roles, err := i.ListRoles(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(roles))

	_, err = i.CreateRole(ctx, driver.RoleConfig{Name: "r1"})
	require.NoError(t, err)

	_, err = i.CreateRole(ctx, driver.RoleConfig{Name: "r2"})
	require.NoError(t, err)

	roles, err = i.ListRoles(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(roles))
}

func TestCreatePolicy(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := i.CreatePolicy(ctx, driver.PolicyConfig{Name: "my-policy", PolicyDocument: "{}"})
		require.NoError(t, err)
		assert.Equal(t, "my-policy", info.Name)
		assert.NotEmpty(t, info.ARN)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := i.CreatePolicy(ctx, driver.PolicyConfig{})
		require.Error(t, err)
	})
}

func TestDeletePolicy(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	pol, err := i.CreatePolicy(ctx, driver.PolicyConfig{Name: "del-policy"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := i.DeletePolicy(ctx, pol.ARN)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := i.DeletePolicy(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetPolicy(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	pol, err := i.CreatePolicy(ctx, driver.PolicyConfig{Name: "get-policy"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := i.GetPolicy(ctx, pol.ARN)
		require.NoError(t, err)
		assert.Equal(t, "get-policy", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := i.GetPolicy(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListPolicies(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	policies, err := i.ListPolicies(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(policies))

	_, err = i.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1"})
	require.NoError(t, err)

	_, err = i.CreatePolicy(ctx, driver.PolicyConfig{Name: "p2"})
	require.NoError(t, err)

	policies, err = i.ListPolicies(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(policies))
}

func TestAttachDetachUserPolicy(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "pol-user"})
	require.NoError(t, err)

	t.Run("attach success", func(t *testing.T) {
		err := i.AttachUserPolicy(ctx, "pol-user", "arn:aws:iam::123456789012:policy/test")
		require.NoError(t, err)
	})

	t.Run("list attached", func(t *testing.T) {
		pols, err := i.ListAttachedUserPolicies(ctx, "pol-user")
		require.NoError(t, err)
		assert.Equal(t, 1, len(pols))
	})

	t.Run("detach success", func(t *testing.T) {
		err := i.DetachUserPolicy(ctx, "pol-user", "arn:aws:iam::123456789012:policy/test")
		require.NoError(t, err)
	})

	t.Run("attach user not found", func(t *testing.T) {
		err := i.AttachUserPolicy(ctx, "nonexistent", "some-arn")
		require.Error(t, err)
	})
}

func TestAttachDetachRolePolicy(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	_, err := i.CreateRole(ctx, driver.RoleConfig{Name: "pol-role"})
	require.NoError(t, err)

	t.Run("attach success", func(t *testing.T) {
		err := i.AttachRolePolicy(ctx, "pol-role", "arn:aws:iam::123456789012:policy/test")
		require.NoError(t, err)
	})

	t.Run("list attached", func(t *testing.T) {
		pols, err := i.ListAttachedRolePolicies(ctx, "pol-role")
		require.NoError(t, err)
		assert.Equal(t, 1, len(pols))
	})

	t.Run("detach success", func(t *testing.T) {
		err := i.DetachRolePolicy(ctx, "pol-role", "arn:aws:iam::123456789012:policy/test")
		require.NoError(t, err)
	})

	t.Run("attach role not found", func(t *testing.T) {
		err := i.AttachRolePolicy(ctx, "nonexistent", "some-arn")
		require.Error(t, err)
	})
}

func TestCheckPermission(t *testing.T) {
	i := newTestIAM()
	ctx := context.Background()

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "perm-user"})
	require.NoError(t, err)

	t.Run("allowed", func(t *testing.T) {
		allowed, err := i.CheckPermission(ctx, "perm-user", "s3:GetObject", "arn:aws:s3:::my-bucket/*")
		require.NoError(t, err)
		assert.True(t, allowed)
	})

	t.Run("principal not found", func(t *testing.T) {
		_, err := i.CheckPermission(ctx, "nonexistent", "s3:GetObject", "arn:aws:s3:::my-bucket/*")
		require.Error(t, err)
	})
}

func TestIAMWithRecorder(t *testing.T) {
	rec := recorder.New()
	i := newTestIAM(WithRecorder(rec))
	ctx := context.Background()

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "rec-user"})
	require.NoError(t, err)

	_, err = i.GetUser(ctx, "rec-user")
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 2)

	createCalls := rec.CallCountFor("iam", "CreateUser")
	assert.Equal(t, 1, createCalls)

	getCalls := rec.CallCountFor("iam", "GetUser")
	assert.Equal(t, 1, getCalls)
}

func TestIAMWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	i := newTestIAM(WithRecorder(rec))
	ctx := context.Background()

	_, _ = i.GetUser(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last)
	assert.NotNil(t, last.Error)
}

func TestIAMWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	i := newTestIAM(WithMetrics(mc))
	ctx := context.Background()

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "met-user"})
	require.NoError(t, err)

	_, err = i.GetUser(ctx, "met-user")
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 2)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 2)
}

func TestIAMWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	i := newTestIAM(WithMetrics(mc))
	ctx := context.Background()

	_, _ = i.GetUser(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestIAMWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	i := newTestIAM(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("iam", "CreateUser", injectedErr, inject.Always{})

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "fail-user"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestIAMWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	i := newTestIAM(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("iam", "GetUser", injectedErr, inject.Always{})

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "inj-user"})
	require.NoError(t, err)

	_, err = i.GetUser(ctx, "inj-user")
	require.Error(t, err)

	getCalls := rec.CallsFor("iam", "GetUser")
	assert.Equal(t, 1, len(getCalls))
	assert.NotNil(t, getCalls[0].Error)
}

func TestIAMWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	i := newTestIAM(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("iam", "CreateUser", injectedErr, inject.Always{})

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "test"})
	require.Error(t, err)

	inj.Remove("iam", "CreateUser")

	_, err = i.CreateUser(ctx, driver.UserConfig{Name: "test"})
	require.NoError(t, err)
}

func TestIAMWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	i := newTestIAM(WithLatency(latency))
	ctx := context.Background()

	info, err := i.CreateUser(ctx, driver.UserConfig{Name: "lat-user"})
	require.NoError(t, err)
	assert.Equal(t, "lat-user", info.Name)
}

func TestIAMAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	i := NewIAM(newMockDriver(),
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := i.CreateUser(ctx, driver.UserConfig{Name: "all-opts"})
	require.NoError(t, err)

	_, err = i.GetUser(ctx, "all-opts")
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}
