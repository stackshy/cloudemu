package route53resolver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

const (
	fwStatusComplete = "COMPLETE"
	fwStatusDeleting = "DELETING"
	fwStatusUpdating = "UPDATING"

	domainOpAdd     = "ADD"
	domainOpRemove  = "REMOVE"
	domainOpReplace = "REPLACE"
)

func fwDomainListNotFound(id string) error {
	return errors.Newf(errors.NotFound, "firewall domain list %q not found", id)
}

func cloneFWDomainList(d *driver.FirewallDomainList) driver.FirewallDomainList { return *d }

func (m *Mock) CreateFirewallDomainList(
	_ context.Context, creatorRequestID, name string, tags []driver.Tag,
) (*driver.FirewallDomainList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if prior, ok := m.idempotentID("fwdomainlist", creatorRequestID); ok {
		if d, found := m.fwDomLists.Get(prior); found {
			out := cloneFWDomainList(d)

			return &out, nil
		}
	}

	id := idgen.GenerateID("rslvr-fdl-")
	d := &driver.FirewallDomainList{
		ID:               id,
		ARN:              m.arn("firewall-domain-list/" + id),
		Name:             name,
		CreatorRequestID: creatorRequestID,
		DomainCount:      0,
		Status:           fwStatusComplete,
		CreatedAt:        m.now(),
		ModifiedAt:       m.now(),
	}
	m.fwDomLists.Set(id, d)
	m.fwDomains.Set(id, nil)
	m.rememberIdempotent("fwdomainlist", creatorRequestID, id)

	if len(tags) > 0 {
		m.tags.Set(d.ARN, copyTags(tags))
	}

	out := cloneFWDomainList(d)

	return &out, nil
}

func (m *Mock) GetFirewallDomainList(_ context.Context, id string) (*driver.FirewallDomainList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.fwDomLists.Get(id)
	if !ok {
		return nil, fwDomainListNotFound(id)
	}

	out := cloneFWDomainList(d)

	return &out, nil
}

func (m *Mock) DeleteFirewallDomainList(_ context.Context, id string) (*driver.FirewallDomainList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.fwDomLists.Get(id)
	if !ok {
		return nil, fwDomainListNotFound(id)
	}

	for _, r := range m.fwRules.All() {
		if r.FirewallDomainListID == id {
			return nil, errors.Newf(errors.FailedPrecondition,
				"firewall domain list %q is still referenced by firewall rules", id)
		}
	}

	m.fwDomLists.Delete(id)
	m.fwDomains.Delete(id)
	m.tags.Delete(d.ARN)

	out := cloneFWDomainList(d)
	out.Status = fwStatusDeleting

	return &out, nil
}

func (m *Mock) ListFirewallDomainLists(_ context.Context) ([]driver.FirewallDomainList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.fwDomLists.All(), cloneFWDomainList), nil
}

func (m *Mock) UpdateFirewallDomains(
	_ context.Context, id, operation string, domains []string,
) (*driver.FirewallDomainList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.fwDomLists.Get(id)
	if !ok {
		return nil, fwDomainListNotFound(id)
	}

	cur, _ := m.fwDomains.Get(id)
	m.fwDomains.Set(id, applyDomainOp(cur, operation, domains))

	updated, _ := m.fwDomains.Get(id)
	d.DomainCount = i32(len(updated))
	d.ModifiedAt = m.now()

	out := cloneFWDomainList(d)
	out.Status = fwStatusUpdating

	return &out, nil
}

// applyDomainOp returns the new domain set after applying ADD/REMOVE/REPLACE.
func applyDomainOp(cur []string, operation string, domains []string) []string {
	switch operation {
	case domainOpReplace:
		return append([]string(nil), domains...)
	case domainOpRemove:
		drop := make(map[string]struct{}, len(domains))
		for _, d := range domains {
			drop[d] = struct{}{}
		}

		out := make([]string, 0, len(cur))

		for _, d := range cur {
			if _, ok := drop[d]; !ok {
				out = append(out, d)
			}
		}

		return out
	default: // ADD
		seen := make(map[string]struct{}, len(cur))
		for _, d := range cur {
			seen[d] = struct{}{}
		}

		out := append([]string(nil), cur...)

		for _, d := range domains {
			if _, ok := seen[d]; !ok {
				out = append(out, d)
				seen[d] = struct{}{}
			}
		}

		return out
	}
}

func (m *Mock) ImportFirewallDomains(
	_ context.Context, id, _, _ string,
) (*driver.FirewallDomainList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.fwDomLists.Get(id)
	if !ok {
		return nil, fwDomainListNotFound(id)
	}

	d.ModifiedAt = m.now()

	out := cloneFWDomainList(d)
	out.Status = fwStatusUpdating

	return &out, nil
}

func (m *Mock) ListFirewallDomains(_ context.Context, id string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.fwDomLists.Has(id) {
		return nil, fwDomainListNotFound(id)
	}

	cur, _ := m.fwDomains.Get(id)

	return append([]string(nil), cur...), nil
}
