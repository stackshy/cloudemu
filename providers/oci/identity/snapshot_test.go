package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// TestSnapshotRestoreRoundTripAllStores seeds every identity store, snapshots,
// restores into a fresh mock and asserts each piece of state comes back under
// its original OCID — a missed store would be silent data loss. The
// user->group membership cross-reference and the policy version history (an
// unexported field that needs promotion) are both exercised.
func TestSnapshotRestoreRoundTripAllStores(t *testing.T) {
	ctx := t.Context()
	src := newMock(t)

	comp := newCompartment(t, src, tenancy, "team")

	user, err := src.CreateOCIUser(ctx, PrincipalSpec{CompartmentID: comp, Name: "alice"})
	require.NoError(t, err)

	group, err := src.CreateOCIGroup(ctx, PrincipalSpec{CompartmentID: comp, Name: adminName})
	require.NoError(t, err)

	mem, err := src.CreateOCIGroupMembership(ctx, user.ID, group.ID)
	require.NoError(t, err)

	pol, err := src.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: comp,
		Name:          "p1",
		Statements:    []string{"Allow group Admins to manage all-resources in tenancy"},
	})
	require.NoError(t, err)

	// A second revision so the version history + counter must round-trip.
	_, err = src.UpdateStatementPolicy(ctx, pol.ID, PolicyUpdate{
		Statements: []string{"Allow group Admins to read all-resources in tenancy"},
	})
	require.NoError(t, err)

	dg, err := src.CreateRole(ctx, driver.RoleConfig{
		Name: "builders", AssumeRolePolicyDoc: "instance.compartment.id = 'x'",
	})
	require.NoError(t, err)

	key, err := src.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	// Compartment restored under its OCID.
	gotComp, err := dst.GetCompartment(ctx, comp)
	require.NoError(t, err)
	assert.Equal(t, "team", gotComp.Name)

	// User + group restored, and the membership still binds the same OCIDs.
	gotUser, err := dst.GetOCIUser(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, comp, gotUser.CompartmentID)

	gotMem, err := dst.GetOCIGroupMembership(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, user.ID, gotMem.UserID)
	assert.Equal(t, group.ID, gotMem.GroupID)

	// Policy restored with its version history and parsed statements (Evaluate
	// only works when parsed is rebuilt on restore).
	rp, ok := dst.policies.Get(pol.ID)
	require.True(t, ok, "policy restored under its OCID")
	assert.Len(t, rp.versions, 2)
	assert.Equal(t, 2, rp.versionCounter)
	assert.NotEmpty(t, rp.parsed, "parsed statements rebuilt on restore")

	granted, err := dst.Evaluate(ctx, &AccessRequest{
		Groups: []string{adminName}, Verb: "read", ResourceType: "all-resources", CompartmentID: comp,
	})
	require.NoError(t, err)
	assert.True(t, granted, "restored policy still grants access")

	// Dynamic group + auth token restored.
	gotDG, err := dst.GetRole(ctx, dg.Name)
	require.NoError(t, err)
	assert.Equal(t, dg.ID, gotDG.ID)

	keys, err := dst.ListAccessKeys(ctx, "alice")
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, key.AccessKeyID, keys[0].AccessKeyID)
}

// TestSnapshotRestoreEmptyNilSafe confirms an empty mock round-trips without
// panicking and stays usable afterwards.
func TestSnapshotRestoreEmptyNilSafe(t *testing.T) {
	ctx := t.Context()
	src := newMock(t)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	_, err = dst.CreateOCIUser(ctx, PrincipalSpec{CompartmentID: tenancy, Name: "bob"})
	require.NoError(t, err)
}
