package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTripAllStores populates every IAM store and every
// attachment/membership map, snapshots, restores into a fresh mock, and asserts
// each piece of state round-trips under its original identity — a missed store
// would be silent data loss.
func TestSnapshotRestoreRoundTripAllStores(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	const doc = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`

	_, err := src.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	require.NoError(t, err)
	_, err = src.CreateRole(ctx, driver.RoleConfig{Name: "r1", AssumeRolePolicyDoc: doc})
	require.NoError(t, err)

	pol, err := src.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1", PolicyDocument: doc})
	require.NoError(t, err)
	_, err = src.CreatePolicyVersion(ctx, driver.PolicyVersionConfig{
		PolicyARN: pol.ARN, PolicyDocument: doc, SetAsDefault: true,
	})
	require.NoError(t, err)

	_, err = src.CreateGroup(ctx, driver.GroupConfig{Name: "g1"})
	require.NoError(t, err)

	ak, err := src.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})
	require.NoError(t, err)

	_, err = src.CreateInstanceProfile(ctx, driver.InstanceProfileConfig{Name: "prof1"})
	require.NoError(t, err)
	require.NoError(t, src.AddRoleToInstanceProfile(ctx, "prof1", "r1"))

	require.NoError(t, src.AttachUserPolicy(ctx, "alice", pol.ARN))
	require.NoError(t, src.AttachRolePolicy(ctx, "r1", pol.ARN))
	require.NoError(t, src.AddUserToGroup(ctx, "alice", "g1"))

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	// Stores.
	assert.True(t, dst.users.Has("alice"), "user restored")
	assert.True(t, dst.roles.Has("r1"), "role restored")

	p, ok := dst.policies.Get(pol.ARN)
	require.True(t, ok, "policy restored under its ARN")
	assert.Len(t, p.versions, 2)
	assert.Equal(t, 2, p.versionCounter)

	assert.True(t, dst.groups.Has("g1"), "group restored")

	restoredKey, ok := dst.accessKeys.Get(ak.AccessKeyID)
	require.True(t, ok, "access key restored under its id")
	assert.Equal(t, "alice", restoredKey.UserName)

	prof, ok := dst.instanceProfiles.Get("prof1")
	require.True(t, ok, "instance profile restored")
	assert.Equal(t, "r1", prof.RoleName)

	// Maps.
	assert.True(t, dst.userPolicies["alice"][pol.ARN], "user attachment restored")
	assert.True(t, dst.rolePolicies["r1"][pol.ARN], "role attachment restored")
	assert.True(t, dst.groupUsers["g1"]["alice"], "group membership restored")
}

// TestSnapshotRestoreEmptyNilSafe confirms an empty mock round-trips without
// panicking and leaves the attachment maps usable.
func TestSnapshotRestoreEmptyNilSafe(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	_, err = dst.CreateUser(ctx, driver.UserConfig{Name: "bob"})
	require.NoError(t, err)
	pol, err := dst.CreatePolicy(ctx, driver.PolicyConfig{Name: "pp", PolicyDocument: "{}"})
	require.NoError(t, err)
	require.NoError(t, dst.AttachUserPolicy(ctx, "bob", pol.ARN))
}
