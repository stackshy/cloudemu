package aoss

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

// validSecurityTypes is the set of security policy types the API accepts.
//
//nolint:gochecknoglobals // static validation set
var validSecurityTypes = map[string]bool{
	driver.SecurityPolicyEncryption: true,
	driver.SecurityPolicyNetwork:    true,
}

// createPolicy is the shared create path for security and access policies. The
// store and the set of valid types distinguish the two surfaces; everything else
// is identical.
func (m *Mock) createPolicy(
	store *memstore.Store[driver.Policy], validTypes map[string]bool, in *driver.CreatePolicyInput,
) (*driver.Policy, error) {
	if in.Name == "" {
		return nil, validation("name is required")
	}

	if !validTypes[in.Type] {
		return nil, validation("invalid policy type: %q", in.Type)
	}

	if len(in.Policy) == 0 {
		return nil, validation("policy is required")
	}

	key := policyKey(in.Type, in.Name)
	if store.Has(key) {
		return nil, conflict("policy with name %q already exists", in.Name)
	}

	now := m.now()
	p := driver.Policy{
		Type:             in.Type,
		Name:             in.Name,
		Description:      in.Description,
		Policy:           copyRaw(in.Policy),
		PolicyVersion:    newPolicyVersion(),
		CreatedDate:      now,
		LastModifiedDate: now,
	}

	store.Set(key, p)

	out := copyPolicy(&p)

	return &out, nil
}

// getPolicy is the shared read path for security and access policies.
func (*Mock) getPolicy(
	store *memstore.Store[driver.Policy], policyType, name string,
) (*driver.Policy, error) {
	p, ok := store.Get(policyKey(policyType, name))
	if !ok {
		return nil, notFound("policy %q of type %q not found", name, policyType)
	}

	out := copyPolicy(&p)

	return &out, nil
}

// listPolicies is the shared list path, filtered to the requested type and
// ordered by store key.
func (*Mock) listPolicies(
	store *memstore.Store[driver.Policy], policyType string, page driver.Page,
) (policies []driver.Policy, nextToken string, err error) {
	stored := store.SortedValues()

	filtered := make([]driver.Policy, 0, len(stored))

	for i := range stored {
		if policyType != "" && stored[i].Type != policyType {
			continue
		}

		filtered = append(filtered, stored[i])
	}

	start, end, next := paginate(len(filtered), page)

	out := make([]driver.Policy, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyPolicy(&filtered[i]))
	}

	return out, next, nil
}

// updatePolicy is the shared update path. A nil Description or Policy leaves the
// stored value unchanged; the policyVersion is always re-minted so IaC can
// detect the change, and lastModifiedDate is bumped.
func (m *Mock) updatePolicy(
	store *memstore.Store[driver.Policy], in *driver.UpdatePolicyInput,
) (*driver.Policy, error) {
	var updated driver.Policy

	ok := store.Update(policyKey(in.Type, in.Name), func(p driver.Policy) driver.Policy {
		if in.Description != nil {
			p.Description = *in.Description
		}

		if len(in.Policy) > 0 {
			p.Policy = copyRaw(in.Policy)
		}

		p.PolicyVersion = newPolicyVersion()
		p.LastModifiedDate = m.now()
		updated = p

		return p
	})
	if !ok {
		return nil, notFound("policy %q of type %q not found", in.Name, in.Type)
	}

	out := copyPolicy(&updated)

	return &out, nil
}

// deletePolicy is the shared delete path.
func (*Mock) deletePolicy(store *memstore.Store[driver.Policy], policyType, name string) error {
	if !store.Delete(policyKey(policyType, name)) {
		return notFound("policy %q of type %q not found", name, policyType)
	}

	return nil
}

// --- security policies (encryption | network) ---

func (m *Mock) CreateSecurityPolicy(_ context.Context, in *driver.CreatePolicyInput) (*driver.Policy, error) {
	return m.createPolicy(m.securityPolicies, validSecurityTypes, in)
}

func (m *Mock) GetSecurityPolicy(_ context.Context, policyType, name string) (*driver.Policy, error) {
	return m.getPolicy(m.securityPolicies, policyType, name)
}

func (m *Mock) ListSecurityPolicies(
	_ context.Context, policyType string, page driver.Page,
) (policies []driver.Policy, nextToken string, err error) {
	return m.listPolicies(m.securityPolicies, policyType, page)
}

func (m *Mock) UpdateSecurityPolicy(_ context.Context, in *driver.UpdatePolicyInput) (*driver.Policy, error) {
	return m.updatePolicy(m.securityPolicies, in)
}

func (m *Mock) DeleteSecurityPolicy(_ context.Context, policyType, name string) error {
	return m.deletePolicy(m.securityPolicies, policyType, name)
}

// --- data access policies (data) ---

func (m *Mock) CreateAccessPolicy(_ context.Context, in *driver.CreatePolicyInput) (*driver.Policy, error) {
	return m.createPolicy(m.accessPolicies, validAccessTypes, in)
}

func (m *Mock) GetAccessPolicy(_ context.Context, policyType, name string) (*driver.Policy, error) {
	return m.getPolicy(m.accessPolicies, policyType, name)
}

func (m *Mock) ListAccessPolicies(
	_ context.Context, policyType string, page driver.Page,
) (policies []driver.Policy, nextToken string, err error) {
	return m.listPolicies(m.accessPolicies, policyType, page)
}

func (m *Mock) UpdateAccessPolicy(_ context.Context, in *driver.UpdatePolicyInput) (*driver.Policy, error) {
	return m.updatePolicy(m.accessPolicies, in)
}

func (m *Mock) DeleteAccessPolicy(_ context.Context, policyType, name string) error {
	return m.deletePolicy(m.accessPolicies, policyType, name)
}

// validAccessTypes is the set of access policy types the API accepts.
//
//nolint:gochecknoglobals // static validation set
var validAccessTypes = map[string]bool{
	driver.AccessPolicyData: true,
}
