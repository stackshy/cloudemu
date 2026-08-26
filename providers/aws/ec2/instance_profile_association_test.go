package ec2

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// fakeProfileResolver is an in-memory InstanceProfileResolver keyed by profile
// name, so the association tests resolve a reference to a canonical ARN + id
// exactly as the real IAM mock does.
type fakeProfileResolver struct {
	profiles map[string]*iamdriver.InstanceProfileInfo
}

func (f *fakeProfileResolver) GetInstanceProfile(_ context.Context, name string) (*iamdriver.InstanceProfileInfo, error) {
	if p, ok := f.profiles[name]; ok {
		return p, nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "instance profile %q not found", name)
}

func newMockWithProfiles(names ...string) *Mock {
	m := newTestMock()
	res := &fakeProfileResolver{profiles: make(map[string]*iamdriver.InstanceProfileInfo, len(names))}

	for _, n := range names {
		res.profiles[n] = &iamdriver.InstanceProfileInfo{
			Name: n,
			ID:   "AIPA" + n,
			ARN:  "arn:aws:iam::123456789012:instance-profile/" + n,
		}
	}

	m.SetInstanceProfileResolver(res)

	return m
}

func TestAssociateIamInstanceProfileProvider(t *testing.T) {
	ctx := context.Background()
	m := newMockWithProfiles("web")

	insts, err := m.RunInstances(ctx, defaultConfig(), 1)
	requireNoError(t, err)

	id := insts[0].ID

	assoc, err := m.AssociateIamInstanceProfile(ctx, id, "", "web")
	requireNoError(t, err)
	assertEqual(t, "associating", assoc.State)
	assertEqual(t, "arn:aws:iam::123456789012:instance-profile/web", assoc.Profile.ARN)
	assertEqual(t, "AIPAweb", assoc.Profile.ID)
	assertTrue(t, assoc.AssociationID != "", "association id should be set")

	// The instance now reflects the profile.
	got, err := m.DescribeInstances(ctx, []string{id}, nil, driver.DescribeInstancesOptions{})
	requireNoError(t, err)
	assertTrue(t, got[0].IamInstanceProfile != nil, "instance should reflect the profile")
	assertEqual(t, "AIPAweb", got[0].IamInstanceProfile.ID)

	// It is listed as associated.
	list, err := m.DescribeIamInstanceProfileAssociations(ctx,
		driver.DescribeIamInstanceProfileAssociationsInput{InstanceIDs: []string{id}})
	requireNoError(t, err)
	assertEqual(t, 1, len(list))
	assertEqual(t, "associated", list[0].State)
	assertEqual(t, id, list[0].InstanceID)
}

func TestAssociateIamInstanceProfileAlreadyAssociatedProvider(t *testing.T) {
	ctx := context.Background()
	m := newMockWithProfiles("a", "b")

	insts, err := m.RunInstances(ctx, defaultConfig(), 1)
	requireNoError(t, err)

	_, err = m.AssociateIamInstanceProfile(ctx, insts[0].ID, "", "a")
	requireNoError(t, err)

	_, err = m.AssociateIamInstanceProfile(ctx, insts[0].ID, "", "b")
	assertError(t, err, true)
	assertTrue(t, cerrors.IsFailedPrecondition(err), "already-associated should be FailedPrecondition")
}

func TestReplaceIamInstanceProfileAssociationProvider(t *testing.T) {
	ctx := context.Background()
	m := newMockWithProfiles("old", "new")

	insts, err := m.RunInstances(ctx, defaultConfig(), 1)
	requireNoError(t, err)

	assoc, err := m.AssociateIamInstanceProfile(ctx, insts[0].ID, "", "old")
	requireNoError(t, err)

	replaced, err := m.ReplaceIamInstanceProfileAssociation(ctx, assoc.AssociationID, "", "new")
	requireNoError(t, err)
	assertEqual(t, assoc.AssociationID, replaced.AssociationID)
	assertEqual(t, "AIPAnew", replaced.Profile.ID)

	got, err := m.DescribeInstances(ctx, []string{insts[0].ID}, nil, driver.DescribeInstancesOptions{})
	requireNoError(t, err)
	assertEqual(t, "AIPAnew", got[0].IamInstanceProfile.ID)
}

func TestDisassociateIamInstanceProfileProvider(t *testing.T) {
	ctx := context.Background()
	m := newMockWithProfiles("web")

	insts, err := m.RunInstances(ctx, defaultConfig(), 1)
	requireNoError(t, err)

	assoc, err := m.AssociateIamInstanceProfile(ctx, insts[0].ID, "", "web")
	requireNoError(t, err)

	dis, err := m.DisassociateIamInstanceProfile(ctx, assoc.AssociationID)
	requireNoError(t, err)
	assertEqual(t, "disassociating", dis.State)

	got, err := m.DescribeInstances(ctx, []string{insts[0].ID}, nil, driver.DescribeInstancesOptions{})
	requireNoError(t, err)
	assertTrue(t, got[0].IamInstanceProfile == nil, "profile should be cleared after disassociate")

	list, err := m.DescribeIamInstanceProfileAssociations(ctx,
		driver.DescribeIamInstanceProfileAssociationsInput{})
	requireNoError(t, err)
	assertEqual(t, 0, len(list))
}

func TestAssociateIamInstanceProfileMissingInstanceProvider(t *testing.T) {
	ctx := context.Background()
	m := newMockWithProfiles("web")

	_, err := m.AssociateIamInstanceProfile(ctx, "i-missing", "", "web")
	assertError(t, err, true)
	assertTrue(t, cerrors.IsNotFound(err), "missing instance should be NotFound")
}

func TestAssociateIamInstanceProfileInvalidProfileProvider(t *testing.T) {
	ctx := context.Background()
	m := newMockWithProfiles("web")

	insts, err := m.RunInstances(ctx, defaultConfig(), 1)
	requireNoError(t, err)

	_, err = m.AssociateIamInstanceProfile(ctx, insts[0].ID, "", "nope")
	assertError(t, err, true)
	assertTrue(t, cerrors.IsInvalidArgument(err), "unresolvable profile should be InvalidArgument")
}

// TestLaunchTimeProfileRecordsAssociation confirms an instance launched WITH a
// profile also gets a backing association that DescribeIamInstanceProfileAssociations
// lists, matching real EC2.
func TestLaunchTimeProfileRecordsAssociation(t *testing.T) {
	ctx := context.Background()
	m := newMockWithProfiles("web")

	cfg := defaultConfig()
	cfg.IamInstanceProfileName = "web"

	insts, err := m.RunInstances(ctx, cfg, 2)
	requireNoError(t, err)

	list, err := m.DescribeIamInstanceProfileAssociations(ctx,
		driver.DescribeIamInstanceProfileAssociationsInput{})
	requireNoError(t, err)
	assertEqual(t, 2, len(list)) // one per launched instance

	// Terminating an instance removes its association.
	requireNoError(t, m.TerminateInstances(ctx, []string{insts[0].ID}))

	after, err := m.DescribeIamInstanceProfileAssociations(ctx,
		driver.DescribeIamInstanceProfileAssociationsInput{})
	requireNoError(t, err)
	assertEqual(t, 1, len(after))
	assertEqual(t, insts[1].ID, after[0].InstanceID)
}

// TestProfileAssociationSnapshotRoundTrip confirms associations survive a
// Snapshot/Restore cycle.
func TestProfileAssociationSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newMockWithProfiles("web")

	insts, err := m.RunInstances(ctx, defaultConfig(), 1)
	requireNoError(t, err)

	_, err = m.AssociateIamInstanceProfile(ctx, insts[0].ID, "", "web")
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMockWithProfiles("web")
	requireNoError(t, restored.Restore(ctx, data))

	list, err := restored.DescribeIamInstanceProfileAssociations(ctx,
		driver.DescribeIamInstanceProfileAssociationsInput{})
	requireNoError(t, err)
	assertEqual(t, 1, len(list))
	assertEqual(t, insts[0].ID, list[0].InstanceID)
	assertEqual(t, "AIPAweb", list[0].Profile.ID)
}
