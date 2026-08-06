package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

const domainVerificationStatusPending = "PENDING"

func domainVerificationNotFound(id string) error {
	return errors.Newf(errors.NotFound, "domain verification %q not found", id)
}

func cloneDomainVerification(d *driver.DomainVerification) driver.DomainVerification { return *d }

func (m *Mock) StartDomainVerification(
	_ context.Context, domainName string, tags map[string]string,
) (*driver.DomainVerification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idgen.GenerateID("dv-")
	d := &driver.DomainVerification{
		ID:         id,
		ARN:        m.arn("domainverification/" + id),
		DomainName: domainName,
		Status:     domainVerificationStatusPending,
		CreatedAt:  m.now(),
	}
	m.domainVerifs.Set(id, d)
	m.writeTags(d.ARN, tags)

	out := cloneDomainVerification(d)

	return &out, nil
}

func (m *Mock) GetDomainVerification(_ context.Context, identifier string) (*driver.DomainVerification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	d, ok := m.domainVerifs.Get(id)
	if !ok {
		return nil, domainVerificationNotFound(id)
	}

	out := cloneDomainVerification(d)

	return &out, nil
}

func (m *Mock) DeleteDomainVerification(_ context.Context, identifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	if !m.domainVerifs.Has(id) {
		return domainVerificationNotFound(id)
	}

	m.domainVerifs.Delete(id)

	return nil
}

func (m *Mock) ListDomainVerifications(_ context.Context) ([]driver.DomainVerification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.domainVerifs.All(), cloneDomainVerification), nil
}
