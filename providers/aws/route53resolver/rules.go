package route53resolver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

const (
	ruleStatusComplete   = "COMPLETE"
	ruleStatusDeleting   = "DELETING"
	shareStatusNotShared = "NOT_SHARED"
	assocStatusComplete  = "COMPLETE"
	assocStatusDeleting  = "DELETING"
)

func ruleNotFound(id string) error {
	return errors.Newf(errors.NotFound, "resolver rule %q not found", id)
}

func (m *Mock) CreateResolverRule(
	_ context.Context, in *driver.CreateResolverRuleInput,
) (*driver.ResolverRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if prior, ok := m.idempotentID("rule", in.CreatorRequestID); ok {
		if r, found := m.rules.Get(prior); found {
			out := cloneRule(r)

			return &out, nil
		}
	}

	id := idgen.GenerateID("rslvr-rr-")
	now := m.now()

	r := &driver.ResolverRule{
		ID:                 id,
		ARN:                m.arn("resolver-rule/" + id),
		CreatorRequestID:   in.CreatorRequestID,
		DomainName:         in.DomainName,
		Name:               in.Name,
		OwnerID:            m.opts.AccountID,
		ResolverEndpointID: in.ResolverEndpointID,
		RuleType:           in.RuleType,
		ShareStatus:        shareStatusNotShared,
		Status:             ruleStatusComplete,
		StatusMessage:      "Successfully created Resolver Rule.",
		TargetIPs:          append([]driver.TargetAddress(nil), in.TargetIPs...),
		CreatedAt:          now,
		ModifiedAt:         now,
	}
	m.rules.Set(id, r)
	m.rememberIdempotent("rule", in.CreatorRequestID, id)

	if len(in.Tags) > 0 {
		m.tags.Set(r.ARN, copyTags(in.Tags))
	}

	out := cloneRule(r)

	return &out, nil
}

func (m *Mock) GetResolverRule(_ context.Context, id string) (*driver.ResolverRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.rules.Get(id)
	if !ok {
		return nil, ruleNotFound(id)
	}

	out := cloneRule(r)

	return &out, nil
}

func (m *Mock) UpdateResolverRule(
	_ context.Context, id string, in driver.UpdateResolverRuleInput,
) (*driver.ResolverRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.rules.Get(id)
	if !ok {
		return nil, ruleNotFound(id)
	}

	updated := cloneRule(r)
	if in.Name != nil {
		updated.Name = *in.Name
	}

	if in.ResolverEndpointID != nil {
		updated.ResolverEndpointID = *in.ResolverEndpointID
	}

	if in.TargetIPs != nil {
		updated.TargetIPs = append([]driver.TargetAddress(nil), in.TargetIPs...)
	}

	updated.ModifiedAt = m.now()
	m.rules.Set(id, &updated)

	out := cloneRule(&updated)

	return &out, nil
}

func (m *Mock) DeleteResolverRule(_ context.Context, id string) (*driver.ResolverRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.rules.Get(id)
	if !ok {
		return nil, ruleNotFound(id)
	}

	for _, a := range m.ruleAssocs.All() {
		if a.ResolverRuleID == id {
			return nil, errors.Newf(errors.FailedPrecondition,
				"resolver rule %q still has VPC associations", id)
		}
	}

	m.rules.Delete(id)
	m.rulePolicies.Delete(r.ARN)
	m.tags.Delete(r.ARN)

	out := cloneRule(r)
	out.Status = ruleStatusDeleting
	out.ModifiedAt = m.now()

	return &out, nil
}

func (m *Mock) ListResolverRules(_ context.Context) ([]driver.ResolverRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.rules.All(), cloneRule), nil
}

func (m *Mock) AssociateResolverRule(
	_ context.Context, ruleID, vpcID, name string,
) (*driver.ResolverRuleAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.rules.Has(ruleID) {
		return nil, ruleNotFound(ruleID)
	}

	for _, a := range m.ruleAssocs.All() {
		if a.ResolverRuleID == ruleID && a.VPCID == vpcID {
			return nil, errors.Newf(errors.AlreadyExists,
				"resolver rule %q is already associated with vpc %q", ruleID, vpcID)
		}
	}

	id := idgen.GenerateID("rslvr-rrassoc-")
	a := &driver.ResolverRuleAssociation{
		ID:             id,
		Name:           name,
		ResolverRuleID: ruleID,
		VPCID:          vpcID,
		Status:         assocStatusComplete,
	}
	m.ruleAssocs.Set(id, a)

	out := *a

	return &out, nil
}

func (m *Mock) DisassociateResolverRule(
	_ context.Context, ruleID, vpcID string,
) (*driver.ResolverRuleAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var found *driver.ResolverRuleAssociation

	for _, a := range m.ruleAssocs.All() {
		if a.ResolverRuleID == ruleID && a.VPCID == vpcID {
			found = a

			break
		}
	}

	if found == nil {
		return nil, errors.Newf(errors.NotFound, "no association between rule %q and vpc %q", ruleID, vpcID)
	}

	m.ruleAssocs.Delete(found.ID)

	out := *found
	out.Status = assocStatusDeleting

	return &out, nil
}

func (m *Mock) GetResolverRuleAssociation(_ context.Context, assocID string) (*driver.ResolverRuleAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.ruleAssocs.Get(assocID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "resolver rule association %q not found", assocID)
	}

	out := *a

	return &out, nil
}

func (m *Mock) ListResolverRuleAssociations(_ context.Context) ([]driver.ResolverRuleAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.ruleAssocs.All(), cloneAssoc), nil
}

func (m *Mock) PutResolverRulePolicy(_ context.Context, arn, policy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rulePolicies.Set(arn, policy)

	return nil
}

func (m *Mock) GetResolverRulePolicy(_ context.Context, arn string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	policy, _ := m.rulePolicies.Get(arn)

	return policy, nil
}
