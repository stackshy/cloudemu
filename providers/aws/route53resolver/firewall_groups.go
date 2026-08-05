package route53resolver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

const (
	failOpenEnabled  = "ENABLED"
	failOpenDisabled = "DISABLED"
	failOpenLocal    = "USE_LOCAL_RESOURCE_SETTING"

	mutationProtectionDisabled = "DISABLED"
)

func fwRuleGroupNotFound(id string) error {
	return errors.Newf(errors.NotFound, "firewall rule group %q not found", id)
}

func cloneFWRuleGroup(g *driver.FirewallRuleGroup) driver.FirewallRuleGroup { return *g }

func cloneFWAssoc(a *driver.FirewallRuleGroupAssociation) driver.FirewallRuleGroupAssociation {
	return *a
}

func cloneFWConfig(c *driver.FirewallConfig) driver.FirewallConfig { return *c }

// ---- rule groups ----

func (m *Mock) CreateFirewallRuleGroup(
	_ context.Context, creatorRequestID, name string, tags []driver.Tag,
) (*driver.FirewallRuleGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idgen.GenerateID("rslvr-frg-")
	g := &driver.FirewallRuleGroup{
		ID:               id,
		ARN:              m.arn("firewall-rule-group/" + id),
		Name:             name,
		CreatorRequestID: creatorRequestID,
		OwnerID:          m.opts.AccountID,
		ShareStatus:      shareStatusNotShared,
		Status:           fwStatusComplete,
		CreatedAt:        m.now(),
		ModifiedAt:       m.now(),
	}
	m.fwRuleGroups.Set(id, g)

	if len(tags) > 0 {
		m.tags.Set(g.ARN, copyTags(tags))
	}

	out := cloneFWRuleGroup(g)

	return &out, nil
}

func (m *Mock) GetFirewallRuleGroup(_ context.Context, id string) (*driver.FirewallRuleGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.fwRuleGroups.Get(id)
	if !ok {
		return nil, fwRuleGroupNotFound(id)
	}

	out := cloneFWRuleGroup(g)

	return &out, nil
}

func (m *Mock) DeleteFirewallRuleGroup(_ context.Context, id string) (*driver.FirewallRuleGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.fwRuleGroups.Get(id)
	if !ok {
		return nil, fwRuleGroupNotFound(id)
	}

	for key, rule := range m.fwRules.All() {
		if rule.FirewallRuleGroupID == id {
			m.fwRules.Delete(key)
		}
	}

	m.fwRuleGroups.Delete(id)
	m.fwPolicies.Delete(g.ARN)
	m.tags.Delete(g.ARN)

	out := cloneFWRuleGroup(g)
	out.Status = fwStatusDeleting

	return &out, nil
}

func (m *Mock) ListFirewallRuleGroups(_ context.Context) ([]driver.FirewallRuleGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.fwRuleGroups.All(), cloneFWRuleGroup), nil
}

func (m *Mock) PutFirewallRuleGroupPolicy(_ context.Context, arn, policy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fwPolicies.Set(arn, policy)

	return nil
}

func (m *Mock) GetFirewallRuleGroupPolicy(_ context.Context, arn string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	policy, _ := m.fwPolicies.Get(arn)

	return policy, nil
}

// ---- associations ----

func (m *Mock) AssociateFirewallRuleGroup(
	_ context.Context, in *driver.AssociateFirewallRuleGroupInput,
) (*driver.FirewallRuleGroupAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.fwRuleGroups.Has(in.FirewallRuleGroupID) {
		return nil, fwRuleGroupNotFound(in.FirewallRuleGroupID)
	}

	id := idgen.GenerateID("rslvr-frgassoc-")

	mp := in.MutationProtection
	if mp == "" {
		mp = mutationProtectionDisabled
	}

	a := &driver.FirewallRuleGroupAssociation{
		ID:                  id,
		ARN:                 m.arn("firewall-rule-group-association/" + id),
		Name:                in.Name,
		CreatorRequestID:    in.CreatorRequestID,
		FirewallRuleGroupID: in.FirewallRuleGroupID,
		VPCID:               in.VPCID,
		Priority:            in.Priority,
		MutationProtection:  mp,
		Status:              fwStatusComplete,
		CreatedAt:           m.now(),
		ModifiedAt:          m.now(),
	}
	m.fwAssocs.Set(id, a)

	if len(in.Tags) > 0 {
		m.tags.Set(a.ARN, copyTags(in.Tags))
	}

	out := cloneFWAssoc(a)

	return &out, nil
}

func (m *Mock) DisassociateFirewallRuleGroup(
	_ context.Context, assocID string,
) (*driver.FirewallRuleGroupAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.fwAssocs.Get(assocID)
	if !ok {
		return nil, fwAssocNotFound(assocID)
	}

	m.fwAssocs.Delete(assocID)
	m.tags.Delete(a.ARN)

	out := cloneFWAssoc(a)
	out.Status = fwStatusDeleting

	return &out, nil
}

func (m *Mock) GetFirewallRuleGroupAssociation(
	_ context.Context, assocID string,
) (*driver.FirewallRuleGroupAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.fwAssocs.Get(assocID)
	if !ok {
		return nil, fwAssocNotFound(assocID)
	}

	out := cloneFWAssoc(a)

	return &out, nil
}

func (m *Mock) ListFirewallRuleGroupAssociations(
	_ context.Context,
) ([]driver.FirewallRuleGroupAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.fwAssocs.All(), cloneFWAssoc), nil
}

func (m *Mock) UpdateFirewallRuleGroupAssociation(
	_ context.Context, in *driver.UpdateFirewallRuleGroupAssociationInput,
) (*driver.FirewallRuleGroupAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.fwAssocs.Get(in.ID)
	if !ok {
		return nil, fwAssocNotFound(in.ID)
	}

	if in.Name != "" {
		a.Name = in.Name
	}

	if in.Priority != 0 {
		a.Priority = in.Priority
	}

	if in.MutationProtection != "" {
		a.MutationProtection = in.MutationProtection
	}

	a.ModifiedAt = m.now()

	out := cloneFWAssoc(a)
	out.Status = fwStatusUpdating

	return &out, nil
}

func fwAssocNotFound(id string) error {
	return errors.Newf(errors.NotFound, "firewall rule group association %q not found", id)
}

// ---- firewall configs ----

// firewallConfigFor materializes a default per-VPC config (fail-open disabled).
// Caller holds m.mu.
func (m *Mock) firewallConfigFor(resourceID string) *driver.FirewallConfig {
	if c, ok := m.fwConfigs.Get(resourceID); ok {
		return c
	}

	c := &driver.FirewallConfig{
		ID:               idgen.GenerateID("rslvr-fc-"),
		OwnerID:          m.opts.AccountID,
		ResourceID:       resourceID,
		FirewallFailOpen: failOpenDisabled,
	}
	m.fwConfigs.Set(resourceID, c)

	return c
}

func (m *Mock) GetFirewallConfig(_ context.Context, resourceID string) (*driver.FirewallConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := cloneFWConfig(m.firewallConfigFor(resourceID))

	return &out, nil
}

func (m *Mock) UpdateFirewallConfig(
	_ context.Context, resourceID, failOpen string,
) (*driver.FirewallConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.firewallConfigFor(resourceID)
	c.FirewallFailOpen = failOpenValue(failOpen)

	out := cloneFWConfig(c)

	return &out, nil
}

// failOpenValue normalizes the request fail-open value to a stored status.
func failOpenValue(v string) string {
	switch v {
	case failOpenEnabled:
		return failOpenEnabled
	case failOpenLocal:
		return failOpenLocal
	default:
		return failOpenDisabled
	}
}

func (m *Mock) ListFirewallConfigs(_ context.Context) ([]driver.FirewallConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.fwConfigs.All(), cloneFWConfig), nil
}

func (*Mock) ListFirewallRuleTypes(_ context.Context) ([]driver.FirewallRuleType, error) {
	return []driver.FirewallRuleType{}, nil
}
