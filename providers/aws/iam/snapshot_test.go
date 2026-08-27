package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// TestSnapshotRestoreRoundTripAllStores populates every IAM store and every
// attachment/membership map, snapshots, restores into a fresh mock, and asserts
// each piece of state round-trips under its original identity — a missed store
// would be silent data loss. The EC2->instance-profile cross-reference (profile
// keyed by name, carrying its role name) is included.
func TestSnapshotRestoreRoundTripAllStores(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	doc := makePolicyDoc([]map[string]any{{"Effect": "Allow", "Action": "s3:*", "Resource": "*"}})

	requireNoError(t, mustCreateUser(src, "alice"))
	requireNoError(t, mustCreateRole(src, "r1", doc))

	pol, err := src.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1", PolicyDocument: doc})
	requireNoError(t, err)
	// Add a second version so the version history + counter must round-trip.
	_, err = src.CreatePolicyVersion(ctx, driver.PolicyVersionConfig{
		PolicyARN: pol.ARN, PolicyDocument: doc, SetAsDefault: true,
	})
	requireNoError(t, err)

	_, err = src.CreateGroup(ctx, driver.GroupConfig{Name: "g1"})
	requireNoError(t, err)

	ak, err := src.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "alice"})
	requireNoError(t, err)

	_, err = src.CreateInstanceProfile(ctx, driver.InstanceProfileConfig{Name: "prof1"})
	requireNoError(t, err)
	requireNoError(t, src.AddRoleToInstanceProfile(ctx, "prof1", "r1"))

	mfa, err := src.CreateVirtualMFADevice(ctx, "dev1", "/")
	requireNoError(t, err)

	// Attachment / membership maps.
	requireNoError(t, src.AttachUserPolicy(ctx, "alice", pol.ARN))
	requireNoError(t, src.AttachRolePolicy(ctx, "r1", pol.ARN))
	requireNoError(t, src.AttachGroupPolicy(ctx, "g1", pol.ARN))
	requireNoError(t, src.AddUserToGroup(ctx, "alice", "g1"))

	// Inline policies (user + group), role inline policy, permissions boundaries.
	requireNoError(t, src.PutUserPolicy(ctx, "alice", "inlineU", doc))
	requireNoError(t, src.PutGroupPolicy(ctx, "g1", "inlineG", doc))
	requireNoError(t, src.PutRolePolicy(ctx, "r1", "inlineR", doc))
	requireNoError(t, src.PutUserPermissionsBoundary(ctx, "alice", pol.ARN))
	requireNoError(t, src.PutRolePermissionsBoundary(ctx, "r1", pol.ARN))

	// Account password policy.
	requireNoError(t, src.UpdateAccountPasswordPolicy(ctx, driver.PasswordPolicy{
		MinimumPasswordLength: 14, RequireSymbols: true,
	}))

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	// Stores.
	u, ok := dst.users.Get("alice")
	assertTrue(t, ok, "user alice restored")
	assertEqual(t, pol.ARN, u.permissionsBoundary)

	r, ok := dst.roles.Get("r1")
	assertTrue(t, ok, "role r1 restored")
	assertEqual(t, pol.ARN, r.permissionsBoundary)
	assertEqual(t, doc, r.inlinePolicies["inlineR"])

	p, ok := dst.policies.Get(pol.ARN)
	assertTrue(t, ok, "policy restored under its ARN")
	assertEqual(t, 2, len(p.versions))
	assertEqual(t, 2, p.versionCounter)

	assertTrue(t, dst.groups.Has("g1"), "group g1 restored")

	restoredKey, ok := dst.accessKeys.Get(ak.AccessKeyID)
	assertTrue(t, ok, "access key restored under its id")
	assertEqual(t, "alice", restoredKey.UserName)

	prof, ok := dst.instanceProfiles.Get("prof1")
	assertTrue(t, ok, "instance profile restored")
	assertEqual(t, "r1", prof.RoleName) // EC2 cross-ref preserved

	assertTrue(t, dst.mfaDevices.Has(mfa.SerialNumber), "mfa device restored under its serial")

	// Maps.
	assertTrue(t, dst.userPolicies["alice"][pol.ARN], "user policy attachment restored")
	assertTrue(t, dst.rolePolicies["r1"][pol.ARN], "role policy attachment restored")
	assertTrue(t, dst.groupPolicies["g1"][pol.ARN], "group policy attachment restored")
	assertTrue(t, dst.groupUsers["g1"]["alice"], "group membership restored")
	assertEqual(t, doc, dst.userInlinePolicies["alice"]["inlineU"])
	assertEqual(t, doc, dst.groupInlinePolicies["g1"]["inlineG"])

	if dst.passwordPolicy == nil {
		t.Fatal("password policy not restored")
	}
	assertEqual(t, 14, dst.passwordPolicy.MinimumPasswordLength)
	assertTrue(t, dst.passwordPolicy.RequireSymbols, "password policy fields restored")
}

// TestSnapshotRestoreEmptyNilSafe confirms an empty mock round-trips without
// panicking (nil maps must not blow up) and leaves the attachment maps usable.
func TestSnapshotRestoreEmptyNilSafe(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	// The maps stay non-nil so subsequent attaches don't panic.
	requireNoError(t, mustCreateUser(dst, "bob"))
	pol, err := dst.CreatePolicy(ctx, driver.PolicyConfig{
		Name:           "pp",
		PolicyDocument: makePolicyDoc([]map[string]any{{"Effect": "Allow", "Action": "*", "Resource": "*"}}),
	})
	requireNoError(t, err)
	requireNoError(t, dst.AttachUserPolicy(ctx, "bob", pol.ARN))
}

func mustCreateUser(m *Mock, name string) error {
	_, err := m.CreateUser(context.Background(), driver.UserConfig{Name: name})
	return err
}

func mustCreateRole(m *Mock, name, doc string) error {
	_, err := m.CreateRole(context.Background(), driver.RoleConfig{Name: name, AssumeRolePolicyDoc: doc})
	return err
}

func assertTrue(t *testing.T, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Errorf("expected true: %s", msg)
	}
}
