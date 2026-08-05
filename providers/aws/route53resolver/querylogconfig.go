package route53resolver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

const (
	qlcStatusCreated       = "CREATED"
	qlcStatusDeleting      = "DELETING"
	qlcAssocStatusActive   = "ACTIVE"
	qlcAssocStatusDeleting = "DELETING"
)

func qlcNotFound(id string) error {
	return errors.Newf(errors.NotFound, "resolver query log config %q not found", id)
}

func cloneQLC(c *driver.QueryLogConfig) driver.QueryLogConfig { return *c }

func cloneQLCAssoc(a *driver.QueryLogConfigAssociation) driver.QueryLogConfigAssociation { return *a }

// countQLCAssocs counts associations for a config. Caller holds m.mu.
func (m *Mock) countQLCAssocs(configID string) int32 {
	var n int

	for _, a := range m.qlcAssocs.All() {
		if a.ResolverQueryLogConfigID == configID {
			n++
		}
	}

	return i32(n)
}

func (m *Mock) CreateResolverQueryLogConfig(
	_ context.Context, in *driver.CreateQueryLogConfigInput,
) (*driver.QueryLogConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idgen.GenerateID("rqlc-")
	c := &driver.QueryLogConfig{
		ID:               id,
		ARN:              m.arn("resolver-query-log-config/" + id),
		CreatorRequestID: in.CreatorRequestID,
		DestinationARN:   in.DestinationARN,
		Name:             in.Name,
		OwnerID:          m.opts.AccountID,
		ShareStatus:      shareStatusNotShared,
		Status:           qlcStatusCreated,
		CreatedAt:        m.now(),
	}
	m.qlcs.Set(id, c)

	if len(in.Tags) > 0 {
		m.tags.Set(c.ARN, copyTags(in.Tags))
	}

	out := cloneQLC(c)

	return &out, nil
}

func (m *Mock) GetResolverQueryLogConfig(_ context.Context, id string) (*driver.QueryLogConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.qlcs.Get(id)
	if !ok {
		return nil, qlcNotFound(id)
	}

	out := cloneQLC(c)
	out.AssociationCount = m.countQLCAssocs(id)

	return &out, nil
}

func (m *Mock) DeleteResolverQueryLogConfig(_ context.Context, id string) (*driver.QueryLogConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.qlcs.Get(id)
	if !ok {
		return nil, qlcNotFound(id)
	}

	m.qlcs.Delete(id)
	m.qlcPolicies.Delete(c.ARN)
	m.tags.Delete(c.ARN)

	out := cloneQLC(c)
	out.Status = qlcStatusDeleting

	return &out, nil
}

func (m *Mock) ListResolverQueryLogConfigs(_ context.Context) ([]driver.QueryLogConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := sortedValues(m.qlcs.All(), cloneQLC)
	for i := range out {
		out[i].AssociationCount = m.countQLCAssocs(out[i].ID)
	}

	return out, nil
}

func (m *Mock) AssociateResolverQueryLogConfig(
	_ context.Context, configID, resourceID string,
) (*driver.QueryLogConfigAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.qlcs.Has(configID) {
		return nil, qlcNotFound(configID)
	}

	id := idgen.GenerateID("rqlca-")
	a := &driver.QueryLogConfigAssociation{
		ID:                       id,
		ResolverQueryLogConfigID: configID,
		ResourceID:               resourceID,
		Status:                   qlcAssocStatusActive,
		CreatedAt:                m.now(),
	}
	m.qlcAssocs.Set(id, a)

	out := cloneQLCAssoc(a)

	return &out, nil
}

func (m *Mock) DisassociateResolverQueryLogConfig(
	_ context.Context, configID, resourceID string,
) (*driver.QueryLogConfigAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var found *driver.QueryLogConfigAssociation

	for _, a := range m.qlcAssocs.All() {
		if a.ResolverQueryLogConfigID == configID && a.ResourceID == resourceID {
			found = a

			break
		}
	}

	if found == nil {
		return nil, errors.Newf(errors.NotFound,
			"no query log config association for config %q and resource %q", configID, resourceID)
	}

	m.qlcAssocs.Delete(found.ID)

	out := cloneQLCAssoc(found)
	out.Status = qlcAssocStatusDeleting

	return &out, nil
}

func (m *Mock) GetResolverQueryLogConfigAssociation(
	_ context.Context, assocID string,
) (*driver.QueryLogConfigAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.qlcAssocs.Get(assocID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "resolver query log config association %q not found", assocID)
	}

	out := cloneQLCAssoc(a)

	return &out, nil
}

func (m *Mock) ListResolverQueryLogConfigAssociations(
	_ context.Context,
) ([]driver.QueryLogConfigAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.qlcAssocs.All(), cloneQLCAssoc), nil
}

func (m *Mock) PutResolverQueryLogConfigPolicy(_ context.Context, arn, policy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.qlcPolicies.Set(arn, policy)

	return nil
}

func (m *Mock) GetResolverQueryLogConfigPolicy(_ context.Context, arn string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	policy, _ := m.qlcPolicies.Get(arn)

	return policy, nil
}
