package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

const assocStatusActive = "ACTIVE"

func assocNotFound(id string) error {
	return errors.Newf(errors.NotFound, "association %q not found", id)
}

// snRef holds the resolved identity of a service network for embedding in an
// association record.
type snRef struct {
	id, arn, name string
}

// resolveSN resolves a service-network identifier, erroring if unknown. Caller
// holds m.mu.
func (m *Mock) resolveSN(identifier string) (snRef, error) {
	id := idFromIdentifier(identifier)

	sn, ok := m.serviceNetworks.Get(id)
	if !ok {
		return snRef{}, serviceNetworkNotFound(id)
	}

	return snRef{id: sn.ID, arn: sn.ARN, name: sn.Name}, nil
}

// ---- SN ↔ VPC ----

func cloneSNVpc(a *driver.SNVpcAssociation) driver.SNVpcAssociation {
	out := *a
	out.SecurityGroupIDs = append([]string(nil), a.SecurityGroupIDs...)

	return out
}

func (m *Mock) CreateSNVpcAssociation(
	_ context.Context, in *driver.CreateSNVpcAssociationInput,
) (*driver.SNVpcAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sn, err := m.resolveSN(in.ServiceNetworkID)
	if err != nil {
		return nil, err
	}

	id := idgen.GenerateID("snva-")
	a := &driver.SNVpcAssociation{
		ID:                 id,
		ARN:                m.arn("servicenetworkvpcassociation/" + id),
		CreatedBy:          m.opts.AccountID,
		ServiceNetworkID:   sn.id,
		ServiceNetworkARN:  sn.arn,
		ServiceNetworkName: sn.name,
		VpcID:              in.VpcID,
		SecurityGroupIDs:   append([]string(nil), in.SecurityGroupIDs...),
		PrivateDNSEnabled:  in.PrivateDNSEnabled,
		Status:             assocStatusActive,
		CreatedAt:          m.now(),
		LastUpdatedAt:      m.now(),
	}
	m.snVpcAssocs.Set(id, a)

	out := cloneSNVpc(a)

	return &out, nil
}

func (m *Mock) GetSNVpcAssociation(_ context.Context, id string) (*driver.SNVpcAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.snVpcAssocs.Get(idFromIdentifier(id))
	if !ok {
		return nil, assocNotFound(idFromIdentifier(id))
	}

	out := cloneSNVpc(a)

	return &out, nil
}

func (m *Mock) UpdateSNVpcAssociation(
	_ context.Context, id string, securityGroupIDs []string,
) (*driver.SNVpcAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.snVpcAssocs.Get(idFromIdentifier(id))
	if !ok {
		return nil, assocNotFound(idFromIdentifier(id))
	}

	if securityGroupIDs != nil {
		a.SecurityGroupIDs = append([]string(nil), securityGroupIDs...)
	}

	a.LastUpdatedAt = m.now()

	out := cloneSNVpc(a)

	return &out, nil
}

func (m *Mock) DeleteSNVpcAssociation(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rid := idFromIdentifier(id)
	if !m.snVpcAssocs.Has(rid) {
		return assocNotFound(rid)
	}

	m.snVpcAssocs.Delete(rid)

	return nil
}

func (m *Mock) ListSNVpcAssociations(_ context.Context) ([]driver.SNVpcAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.snVpcAssocs.All(), cloneSNVpc), nil
}

func (*Mock) ListSNVpcEndpointAssociations(
	_ context.Context, _ string,
) ([]driver.SNVpcAssociation, error) {
	// VPC-endpoint associations are a distinct managed surface not modeled here;
	// return an empty set so the operation succeeds.
	return []driver.SNVpcAssociation{}, nil
}

// ---- SN ↔ Service ----

func cloneSNSvc(a *driver.SNServiceAssociation) driver.SNServiceAssociation { return *a }

func (m *Mock) CreateSNServiceAssociation(
	_ context.Context, serviceNetworkID, serviceID string, _ map[string]string,
) (*driver.SNServiceAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sn, err := m.resolveSN(serviceNetworkID)
	if err != nil {
		return nil, err
	}

	svc, ok := m.services.Get(idFromIdentifier(serviceID))
	if !ok {
		return nil, serviceNotFound(idFromIdentifier(serviceID))
	}

	id := idgen.GenerateID("snsa-")
	a := &driver.SNServiceAssociation{
		ID:                 id,
		ARN:                m.arn("servicenetworkserviceassociation/" + id),
		CreatedBy:          m.opts.AccountID,
		CustomDomainName:   svc.CustomDomainName,
		DNSName:            svc.DNSName,
		HostedZoneID:       svc.HostedZoneID,
		ServiceID:          svc.ID,
		ServiceARN:         svc.ARN,
		ServiceName:        svc.Name,
		ServiceNetworkID:   sn.id,
		ServiceNetworkARN:  sn.arn,
		ServiceNetworkName: sn.name,
		Status:             assocStatusActive,
		CreatedAt:          m.now(),
	}
	m.snSvcAssocs.Set(id, a)

	out := cloneSNSvc(a)

	return &out, nil
}

func (m *Mock) GetSNServiceAssociation(_ context.Context, id string) (*driver.SNServiceAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.snSvcAssocs.Get(idFromIdentifier(id))
	if !ok {
		return nil, assocNotFound(idFromIdentifier(id))
	}

	out := cloneSNSvc(a)

	return &out, nil
}

func (m *Mock) DeleteSNServiceAssociation(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rid := idFromIdentifier(id)
	if !m.snSvcAssocs.Has(rid) {
		return assocNotFound(rid)
	}

	m.snSvcAssocs.Delete(rid)

	return nil
}

func (m *Mock) ListSNServiceAssociations(_ context.Context) ([]driver.SNServiceAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.snSvcAssocs.All(), cloneSNSvc), nil
}

// ---- SN ↔ Resource ----

func cloneSNRes(a *driver.SNResourceAssociation) driver.SNResourceAssociation { return *a }

func (m *Mock) CreateSNResourceAssociation(
	_ context.Context, serviceNetworkID, resourceConfigID string, privateDNS bool, _ map[string]string,
) (*driver.SNResourceAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sn, err := m.resolveSN(serviceNetworkID)
	if err != nil {
		return nil, err
	}

	rcID := idFromIdentifier(resourceConfigID)
	id := idgen.GenerateID("snra-")
	a := &driver.SNResourceAssociation{
		ID:                       id,
		ARN:                      m.arn("servicenetworkresourceassociation/" + id),
		CreatedBy:                m.opts.AccountID,
		ResourceConfigurationID:  rcID,
		ResourceConfigurationARN: m.arn("resourceconfiguration/" + rcID),
		ServiceNetworkID:         sn.id,
		ServiceNetworkARN:        sn.arn,
		ServiceNetworkName:       sn.name,
		PrivateDNSEnabled:        privateDNS,
		Status:                   assocStatusActive,
		CreatedAt:                m.now(),
		LastUpdatedAt:            m.now(),
	}
	m.snResAssocs.Set(id, a)

	out := cloneSNRes(a)

	return &out, nil
}

func (m *Mock) GetSNResourceAssociation(_ context.Context, id string) (*driver.SNResourceAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.snResAssocs.Get(idFromIdentifier(id))
	if !ok {
		return nil, assocNotFound(idFromIdentifier(id))
	}

	out := cloneSNRes(a)

	return &out, nil
}

func (m *Mock) DeleteSNResourceAssociation(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rid := idFromIdentifier(id)
	if !m.snResAssocs.Has(rid) {
		return assocNotFound(rid)
	}

	m.snResAssocs.Delete(rid)

	return nil
}

func (m *Mock) ListSNResourceAssociations(_ context.Context) ([]driver.SNResourceAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.snResAssocs.All(), cloneSNRes), nil
}
