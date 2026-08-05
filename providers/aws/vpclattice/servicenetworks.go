package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func serviceNetworkNotFound(id string) error {
	return errors.Newf(errors.NotFound, "service network %q not found", id)
}

func cloneServiceNetwork(s *driver.ServiceNetwork) driver.ServiceNetwork { return *s }

// applyAssocCounts fills the association counters for a service network from
// the association stores. Caller holds m.mu.
func (m *Mock) applyAssocCounts(s *driver.ServiceNetwork) {
	var svcs, vpcs, res int64

	for _, a := range m.snSvcAssocs.All() {
		if a.ServiceNetworkID == s.ID {
			svcs++
		}
	}

	for _, a := range m.snVpcAssocs.All() {
		if a.ServiceNetworkID == s.ID {
			vpcs++
		}
	}

	for _, a := range m.snResAssocs.All() {
		if a.ServiceNetworkID == s.ID {
			res++
		}
	}

	s.NumberOfAssociatedServices = svcs
	s.NumberOfAssociatedVPCs = vpcs
	s.NumberOfAssociatedResourceConfigurations = res
}

func (m *Mock) CreateServiceNetwork(
	_ context.Context, in *driver.CreateServiceNetworkInput,
) (*driver.ServiceNetwork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	authType := in.AuthType
	if authType == "" {
		authType = authTypeNone
	}

	id := idgen.GenerateID("sn-")
	sn := &driver.ServiceNetwork{
		ID:                   id,
		ARN:                  m.arn("servicenetwork/" + id),
		Name:                 in.Name,
		AuthType:             authType,
		SharingConfigEnabled: in.SharingConfigEnabled,
		CreatedAt:            m.now(),
		LastUpdatedAt:        m.now(),
	}
	m.serviceNetworks.Set(id, sn)

	out := cloneServiceNetwork(sn)

	return &out, nil
}

func (m *Mock) GetServiceNetwork(_ context.Context, identifier string) (*driver.ServiceNetwork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	sn, ok := m.serviceNetworks.Get(id)
	if !ok {
		return nil, serviceNetworkNotFound(id)
	}

	out := cloneServiceNetwork(sn)
	m.applyAssocCounts(&out)

	return &out, nil
}

func (m *Mock) UpdateServiceNetwork(
	_ context.Context, identifier, authType string,
) (*driver.ServiceNetwork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	sn, ok := m.serviceNetworks.Get(id)
	if !ok {
		return nil, serviceNetworkNotFound(id)
	}

	if authType != "" {
		sn.AuthType = authType
	}

	sn.LastUpdatedAt = m.now()

	out := cloneServiceNetwork(sn)

	return &out, nil
}

func (m *Mock) DeleteServiceNetwork(_ context.Context, identifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	if !m.serviceNetworks.Has(id) {
		return serviceNetworkNotFound(id)
	}

	m.serviceNetworks.Delete(id)

	return nil
}

func (m *Mock) ListServiceNetworks(_ context.Context) ([]driver.ServiceNetwork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := sortedValues(m.serviceNetworks.All(), cloneServiceNetwork)
	for i := range out {
		m.applyAssocCounts(&out[i])
	}

	return out, nil
}
