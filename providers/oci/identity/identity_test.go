package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	tenancy   = config.DefaultTenancyOCID
	devName   = "dev"
	adminName = "Admins"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC))),
		config.WithRegion("us-ashburn-1"),
	))
}

// newCompartment creates a child compartment and returns its OCID.
func newCompartment(t *testing.T, m *Mock, parent, name string) string {
	t.Helper()

	c, err := m.CreateCompartment(t.Context(), driver.CompartmentSpec{ParentID: parent, Name: name})
	require.NoError(t, err)

	return c.ID
}

func TestGlobalOCIDShape(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	user, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: "alice"})
	require.NoError(t, err)

	group, err := m.CreateOCIGroup(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: adminName})
	require.NoError(t, err)

	mem, err := m.CreateOCIGroupMembership(ctx, user.ID, group.ID)
	require.NoError(t, err)

	pol, err := m.CreateStatementPolicy(ctx, &driver.PolicySpec{
		CompartmentID: tenancy,
		Name:          "p1",
		Statements:    []string{"Allow group Admins to manage all-resources in tenancy"},
	})
	require.NoError(t, err)

	comp, err := m.CreateCompartment(ctx, driver.CompartmentSpec{ParentID: tenancy, Name: devName})
	require.NoError(t, err)

	role, err := m.CreateRole(ctx, driver.RoleConfig{Name: "instances", AssumeRolePolicyDoc: "ALL {instance.id = 'x'}"})
	require.NoError(t, err)

	tests := []struct {
		name string
		id   string
		kind string
	}{
		{name: "user", id: user.ID, kind: kindUser},
		{name: "group", id: group.ID, kind: kindGroup},
		{name: "membership", id: mem.ID, kind: kindMembership},
		{name: "policy", id: pol.ID, kind: kindPolicy},
		{name: "compartment", id: comp.ID, kind: kindCompartment},
		{name: "dynamic group", id: role.ID, kind: kindDynamicGroup},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts := strings.Split(tc.id, ".")

			// Identity OCIDs are region-agnostic: five segments with an empty
			// region, which is the doubled dot real OCI emits.
			require.Len(t, parts, 5, "got %q", tc.id)
			assert.Equal(t, "ocid1", parts[0])
			assert.Equal(t, tc.kind, parts[1])
			assert.Equal(t, config.DefaultRealm, parts[2])
			assert.Empty(t, parts[3], "identity OCIDs carry no region")
			assert.True(t, strings.HasPrefix(tc.id, "ocid1."+tc.kind+"."+config.DefaultRealm+".."), "got %q", tc.id)
		})
	}
}

func TestOCIDHonoursRealm(t *testing.T) {
	m := New(config.NewOptions(config.WithRealm("oc2")))

	u, err := m.CreateOCIUser(t.Context(), driver.PrincipalSpec{CompartmentID: tenancy, Name: "alice"})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(u.ID, "ocid1.user.oc2.."), "got %q", u.ID)
}

func TestUserCRUD(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	created, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{
		CompartmentID: tenancy,
		Name:          "alice",
		Description:   "first user",
		FreeformTags:  map[string]string{"team": "core"},
	})
	require.NoError(t, err)
	assert.Equal(t, "alice", created.Name)
	assert.Equal(t, tenancy, created.CompartmentID)
	assert.Equal(t, lifecycleActive, created.LifecycleState)
	assert.Equal(t, "2026-08-08T12:00:00Z", created.TimeCreated)

	got, err := m.GetOCIUser(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)

	updated, err := m.UpdateOCIUser(ctx, created.ID, driver.IdentityUpdate{Description: "renamed"})
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.Description)

	require.NoError(t, m.DeleteOCIUser(ctx, created.ID))

	_, err = m.GetOCIUser(ctx, created.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestUserErrors(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	_, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: "alice"})
	require.NoError(t, err)

	tests := []struct {
		name string
		call func() error
		code cerrors.Code
	}{
		{
			name: "duplicate name",
			call: func() error {
				_, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: "alice"})
				return err
			},
			code: cerrors.AlreadyExists,
		},
		{
			name: "missing name",
			call: func() error {
				_, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: tenancy})
				return err
			},
			code: cerrors.InvalidArgument,
		},
		{
			name: "get unknown",
			call: func() error {
				_, err := m.GetOCIUser(ctx, "ocid1.user.oc1..missing")
				return err
			},
			code: cerrors.NotFound,
		},
		{
			name: "update unknown",
			call: func() error {
				_, err := m.UpdateOCIUser(ctx, "ocid1.user.oc1..missing", driver.IdentityUpdate{})
				return err
			},
			code: cerrors.NotFound,
		},
		{
			name: "delete unknown",
			call: func() error { return m.DeleteOCIUser(ctx, "ocid1.user.oc1..missing") },
			code: cerrors.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.code, cerrors.GetCode(tc.call()))
		})
	}
}

func TestGroupCRUD(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	created, err := m.CreateOCIGroup(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: adminName})
	require.NoError(t, err)

	got, err := m.GetOCIGroup(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, adminName, got.Name)

	_, err = m.CreateOCIGroup(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: adminName})
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	require.NoError(t, m.DeleteOCIGroup(ctx, created.ID))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteOCIGroup(ctx, created.ID)))
}

func TestListsFilterByCompartment(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	_, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: "root-user"})
	require.NoError(t, err)

	_, err = m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: dev, Name: "dev-user"})
	require.NoError(t, err)

	_, err = m.CreateOCIGroup(ctx, driver.PrincipalSpec{CompartmentID: dev, Name: "dev-group"})
	require.NoError(t, err)

	tests := []struct {
		name        string
		compartment string
		wantUsers   []string
		wantGroups  int
	}{
		{name: "tenancy", compartment: tenancy, wantUsers: []string{"root-user"}},
		{name: "dev compartment", compartment: dev, wantUsers: []string{"dev-user"}, wantGroups: 1},
		{name: "unrelated compartment", compartment: "ocid1.compartment.oc1..other", wantUsers: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			users, err := m.ListOCIUsers(ctx, tc.compartment)
			require.NoError(t, err)

			names := make([]string, 0, len(users))
			for _, u := range users {
				names = append(names, u.Name)
			}

			assert.Equal(t, tc.wantUsers, nilIfEmpty(names))

			groups, err := m.ListOCIGroups(ctx, tc.compartment)
			require.NoError(t, err)
			assert.Len(t, groups, tc.wantGroups)
		})
	}
}

func nilIfEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	return in
}

func TestMembershipLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	user, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: "alice"})
	require.NoError(t, err)

	group, err := m.CreateOCIGroup(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: adminName})
	require.NoError(t, err)

	mem, err := m.CreateOCIGroupMembership(ctx, user.ID, group.ID)
	require.NoError(t, err)
	assert.Equal(t, user.ID, mem.UserID)
	assert.Equal(t, tenancy, mem.CompartmentID)

	_, err = m.CreateOCIGroupMembership(ctx, user.ID, group.ID)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	_, err = m.CreateOCIGroupMembership(ctx, "ocid1.user.oc1..missing", group.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	byUser, err := m.ListOCIGroupMemberships(ctx, tenancy, user.ID, "")
	require.NoError(t, err)
	assert.Len(t, byUser, 1)

	byOtherGroup, err := m.ListOCIGroupMemberships(ctx, tenancy, "", "ocid1.group.oc1..other")
	require.NoError(t, err)
	assert.Empty(t, byOtherGroup)

	// Deleting the user cascades to its memberships.
	require.NoError(t, m.DeleteOCIUser(ctx, user.ID))

	remaining, err := m.ListOCIGroupMemberships(ctx, tenancy, "", "")
	require.NoError(t, err)
	assert.Empty(t, remaining)

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteOCIGroupMembership(ctx, mem.ID)))
}

func TestPortableUsersAndGroups(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	u, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice", Tags: map[string]string{"a": "b"}})
	require.NoError(t, err)
	assert.Equal(t, u.ID, u.ARN, "the portable ARN field carries the OCID")

	_, err = m.CreateGroup(ctx, driver.GroupConfig{Name: adminName})
	require.NoError(t, err)

	require.NoError(t, m.AddUserToGroup(ctx, "alice", adminName))

	groups, err := m.ListGroupsForUser(ctx, "alice")
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, adminName, groups[0].Name)

	require.NoError(t, m.RemoveUserFromGroup(ctx, "alice", adminName))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.RemoveUserFromGroup(ctx, "alice", adminName)))

	got, err := m.GetUser(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, "/", got.Path)

	all, err := m.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1)

	require.NoError(t, m.DeleteUser(ctx, "alice"))

	_, err = m.GetUser(ctx, "alice")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPortableRolesAreDynamicGroups(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	rule := "ALL {instance.compartment.id = 'ocid1.compartment.oc1..dev'}"

	role, err := m.CreateRole(ctx, driver.RoleConfig{Name: "fleet", AssumeRolePolicyDoc: rule})
	require.NoError(t, err)
	assert.Equal(t, rule, role.AssumeRolePolicyDoc)

	_, err = m.CreateRole(ctx, driver.RoleConfig{Name: "fleet"})
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	got, err := m.GetRole(ctx, "fleet")
	require.NoError(t, err)
	assert.Equal(t, role.ID, got.ID)

	roles, err := m.ListRoles(ctx)
	require.NoError(t, err)
	assert.Len(t, roles, 1)

	require.NoError(t, m.DeleteRole(ctx, "fleet"))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteRole(ctx, "fleet")))
}

func TestPortableAccessKeysAreAuthTokens(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)

	key, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(key.AccessKeyID, "ocid1.credential."), "got %q", key.AccessKeyID)
	assert.Equal(t, lifecycleActive, key.Status)

	keys, err := m.ListAccessKeys(ctx, "alice")
	require.NoError(t, err)
	assert.Len(t, keys, 1)

	_, err = m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "bob"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	require.NoError(t, m.DeleteAccessKey(ctx, "alice", key.AccessKeyID))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteAccessKey(ctx, "alice", key.AccessKeyID)))
}

func TestOperationsWithoutAnOCIEquivalent(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	tests := []struct {
		name string
		call func() error
	}{
		{name: "attach user policy", call: func() error { return m.AttachUserPolicy(ctx, "alice", "p") }},
		{name: "detach user policy", call: func() error { return m.DetachUserPolicy(ctx, "alice", "p") }},
		{name: "attach role policy", call: func() error { return m.AttachRolePolicy(ctx, "fleet", "p") }},
		{name: "detach role policy", call: func() error { return m.DetachRolePolicy(ctx, "fleet", "p") }},
		{
			name: "list attached user policies",
			call: func() error { _, err := m.ListAttachedUserPolicies(ctx, "alice"); return err },
		},
		{
			name: "list attached role policies",
			call: func() error { _, err := m.ListAttachedRolePolicies(ctx, "fleet"); return err },
		},
		{
			name: "create instance profile",
			call: func() error {
				_, err := m.CreateInstanceProfile(ctx, driver.InstanceProfileConfig{Name: "p"})
				return err
			},
		},
		{name: "delete instance profile", call: func() error { return m.DeleteInstanceProfile(ctx, "p") }},
		{name: "get instance profile", call: func() error { _, err := m.GetInstanceProfile(ctx, "p"); return err }},
		{name: "list instance profiles", call: func() error { _, err := m.ListInstanceProfiles(ctx); return err }},
		{name: "add role to profile", call: func() error { return m.AddRoleToInstanceProfile(ctx, "p", "r") }},
		{name: "remove role from profile", call: func() error { return m.RemoveRoleFromInstanceProfile(ctx, "p", "r") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(tc.call()))
		})
	}
}

func TestContextIsAccepted(t *testing.T) {
	// The driver ignores the context, but every call must still take one.
	m := newMock(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, err := m.CreateOCIUser(ctx, driver.PrincipalSpec{CompartmentID: tenancy, Name: "alice"})
	require.NoError(t, err)
}
