package ec2

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// iamAssocPrefix is the AWS association-id prefix for an IAM instance-profile
// association (iip-assoc-...).
const iamAssocPrefix = "iip-assoc-"

// IAM instance-profile association lifecycle states. An association is stored in
// its steady "associated" state; the transient "associating"/"disassociating"
// states are reported only on the action that triggers the transition, matching
// real EC2.
const (
	assocStateAssociating    = "associating"
	assocStateAssociated     = "associated"
	assocStateDisassociating = "disassociating"
)

// iamProfileAssociation is one association of an IAM instance profile with an
// instance, keyed by ID in m.iamProfileAssociations. Fields are exported so the
// association survives the JSON snapshot/restore round-trip.
type iamProfileAssociation struct {
	ID         string                     `json:"id"`
	InstanceID string                     `json:"instanceId"`
	Profile    *driver.IamInstanceProfile `json:"iamInstanceProfile,omitempty"`
	State      string                     `json:"state,omitempty"`
}

// toDriver renders the association in the driver shape, reporting the supplied
// state (which may be a transient action-state that differs from the stored one).
func (a *iamProfileAssociation) toDriver(state string) *driver.IamInstanceProfileAssociation {
	return &driver.IamInstanceProfileAssociation{
		AssociationID: a.ID,
		InstanceID:    a.InstanceID,
		Profile:       cloneProfile(a.Profile),
		State:         state,
	}
}

// cloneProfile returns a defensive copy so a stored association never shares its
// profile pointer with a caller or with an instance's stored profile.
func cloneProfile(p *driver.IamInstanceProfile) *driver.IamInstanceProfile {
	if p == nil {
		return nil
	}

	c := *p

	return &c
}

// AssociateIamInstanceProfile attaches an IAM instance profile to an already
// running (or stopped) instance, the post-launch counterpart to
// RunInstances{IamInstanceProfile}. The profile reference is resolved exactly as
// launch-time does; an instance that already has an association is rejected
// (real EC2 answers IncorrectState).
func (m *Mock) AssociateIamInstanceProfile(
	ctx context.Context, instanceID, profileARN, profileName string,
) (*driver.IamInstanceProfileAssociation, error) {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	profile, err := m.resolveProfileRef(ctx, profileARN, profileName)
	if err != nil {
		return nil, err
	}

	if profile == nil {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"IamInstanceProfile.Arn or IamInstanceProfile.Name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.findAssociationByInstanceLocked(instanceID) != nil {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"There is an existing association for instance %s", instanceID)
	}

	assoc := &iamProfileAssociation{
		ID:         idgen.GenerateID(iamAssocPrefix),
		InstanceID: instanceID,
		Profile:    cloneProfile(profile),
		State:      assocStateAssociated,
	}
	m.iamProfileAssociations.Set(assoc.ID, assoc)
	m.setInstanceProfile(inst, profile)

	return assoc.toDriver(assocStateAssociating), nil
}

// DescribeIamInstanceProfileAssociations lists associations matching the supplied
// association-id / instance-id / state filters (all optional), sorted by
// association id so pagination is stable.
func (m *Mock) DescribeIamInstanceProfileAssociations(
	_ context.Context, in driver.DescribeIamInstanceProfileAssociationsInput,
) ([]driver.IamInstanceProfileAssociation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.iamProfileAssociations.All()
	out := make([]driver.IamInstanceProfileAssociation, 0, len(all))

	for _, a := range all {
		if matchAssociation(a, in) {
			out = append(out, *a.toDriver(a.State))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].AssociationID < out[j].AssociationID })

	return out, nil
}

// ReplaceIamInstanceProfileAssociation swaps the profile on an existing
// association, reflecting the new profile on the instance. The association keeps
// its id.
func (m *Mock) ReplaceIamInstanceProfileAssociation(
	ctx context.Context, associationID, profileARN, profileName string,
) (*driver.IamInstanceProfileAssociation, error) {
	profile, err := m.resolveProfileRef(ctx, profileARN, profileName)
	if err != nil {
		return nil, err
	}

	if profile == nil {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"IamInstanceProfile.Arn or IamInstanceProfile.Name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	assoc, ok := m.iamProfileAssociations.Get(associationID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "association %q not found", associationID)
	}

	assoc.Profile = cloneProfile(profile)
	m.iamProfileAssociations.Set(assoc.ID, assoc)

	if inst, found := m.instances.Get(assoc.InstanceID); found {
		m.setInstanceProfile(inst, profile)
	}

	return assoc.toDriver(assocStateAssociating), nil
}

// DisassociateIamInstanceProfile removes an association, clearing the instance's
// profile.
func (m *Mock) DisassociateIamInstanceProfile(
	_ context.Context, associationID string,
) (*driver.IamInstanceProfileAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	assoc, ok := m.iamProfileAssociations.Get(associationID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "association %q not found", associationID)
	}

	m.iamProfileAssociations.Delete(associationID)

	if inst, found := m.instances.Get(assoc.InstanceID); found {
		m.setInstanceProfile(inst, nil)
	}

	return assoc.toDriver(assocStateDisassociating), nil
}

// recordProfileAssociation stores a backing association for an instance launched
// with an IamInstanceProfile, so DescribeIamInstanceProfileAssociations lists it
// (matching real EC2). The instance's own profile field is already set at launch.
func (m *Mock) recordProfileAssociation(instanceID string, profile *driver.IamInstanceProfile) {
	m.mu.Lock()
	defer m.mu.Unlock()

	assoc := &iamProfileAssociation{
		ID:         idgen.GenerateID(iamAssocPrefix),
		InstanceID: instanceID,
		Profile:    cloneProfile(profile),
		State:      assocStateAssociated,
	}
	m.iamProfileAssociations.Set(assoc.ID, assoc)
}

// deleteAssociationsForInstance removes every association bound to an instance,
// used when the instance is terminated or a launch is rolled back.
func (m *Mock) deleteAssociationsForInstance(instanceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, a := range m.iamProfileAssociations.All() {
		if a.InstanceID == instanceID {
			m.iamProfileAssociations.Delete(id)
		}
	}
}

// findAssociationByInstanceLocked returns the instance's current association, or
// nil. The caller must hold m.mu.
func (m *Mock) findAssociationByInstanceLocked(instanceID string) *iamProfileAssociation {
	for _, a := range m.iamProfileAssociations.All() {
		if a.InstanceID == instanceID {
			return a
		}
	}

	return nil
}

// setInstanceProfile reflects a profile (or nil to clear) on the stored instance
// under its own lock, so DescribeInstances renders the current association.
func (*Mock) setInstanceProfile(inst *instanceData, profile *driver.IamInstanceProfile) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	inst.iamInstanceProfile = cloneProfile(profile)
}

// matchAssociation reports whether a satisfies every populated filter in in.
func matchAssociation(a *iamProfileAssociation, in driver.DescribeIamInstanceProfileAssociationsInput) bool {
	return containsOrEmpty(in.AssociationIDs, a.ID) &&
		containsOrEmpty(in.InstanceIDs, a.InstanceID) &&
		containsOrEmpty(in.States, a.State)
}

// containsOrEmpty reports whether want is empty (no filter) or contains value.
func containsOrEmpty(want []string, value string) bool {
	if len(want) == 0 {
		return true
	}

	for _, w := range want {
		if w == value {
			return true
		}
	}

	return false
}
