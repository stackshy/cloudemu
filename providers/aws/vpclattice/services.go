package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

const (
	serviceStatusActive   = "ACTIVE"
	serviceHostedZone     = "Z1VPCLATTICE"
	defaultIdleTimeoutSec = 60
)

func serviceNotFound(id string) error {
	return errors.Newf(errors.NotFound, "service %q not found", id)
}

func cloneService(s *driver.Service) driver.Service { return *s }

func (m *Mock) CreateService(_ context.Context, in *driver.CreateServiceInput) (*driver.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	authType := in.AuthType
	if authType == "" {
		authType = authTypeNone
	}

	idle := in.IdleTimeoutSeconds
	if idle == 0 {
		idle = defaultIdleTimeoutSec
	}

	id := idgen.GenerateID("svc-")
	svc := &driver.Service{
		ID:                 id,
		ARN:                m.arn("service/" + id),
		Name:               in.Name,
		AuthType:           authType,
		CertificateARN:     in.CertificateARN,
		CustomDomainName:   in.CustomDomainName,
		DNSName:            id + ".vpc-lattice-svcs." + m.opts.Region + ".on.aws",
		HostedZoneID:       serviceHostedZone,
		IdleTimeoutSeconds: idle,
		Status:             serviceStatusActive,
		CreatedAt:          m.now(),
		LastUpdatedAt:      m.now(),
	}
	m.services.Set(id, svc)
	m.writeTags(svc.ARN, in.Tags)

	out := cloneService(svc)

	return &out, nil
}

func (m *Mock) GetService(_ context.Context, identifier string) (*driver.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	svc, ok := m.services.Get(id)
	if !ok {
		return nil, serviceNotFound(id)
	}

	out := cloneService(svc)

	return &out, nil
}

func (m *Mock) UpdateService(_ context.Context, in *driver.UpdateServiceInput) (*driver.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(in.ID)

	svc, ok := m.services.Get(id)
	if !ok {
		return nil, serviceNotFound(id)
	}

	if in.AuthType != "" {
		svc.AuthType = in.AuthType
	}

	if in.CertificateARN != "" {
		svc.CertificateARN = in.CertificateARN
	}

	if in.IdleTimeoutSeconds != 0 {
		svc.IdleTimeoutSeconds = in.IdleTimeoutSeconds
	}

	svc.LastUpdatedAt = m.now()

	out := cloneService(svc)

	return &out, nil
}

func (m *Mock) DeleteService(_ context.Context, identifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	if !m.services.Has(id) {
		return serviceNotFound(id)
	}

	// Real AWS refuses to delete a service still associated with a network.
	for _, a := range m.snSvcAssocs.All() {
		if a.ServiceID == id {
			return errors.Newf(errors.FailedPrecondition,
				"service %q is still associated with a service network", id)
		}
	}

	// Cascade the service's contained listeners and their rules.
	for lid, l := range m.listeners.All() {
		if l.ServiceID != id {
			continue
		}

		for rid, r := range m.rules.All() {
			if r.ServiceID == id && r.ListenerID == lid {
				m.rules.Delete(rid)
			}
		}

		m.listeners.Delete(lid)
	}

	m.services.Delete(id)

	return nil
}

func (m *Mock) ListServices(_ context.Context) ([]driver.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.services.All(), cloneService), nil
}
