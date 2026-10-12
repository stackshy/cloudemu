package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func resourceGatewayNotFound(id string) error {
	return errors.Newf(errors.NotFound, "resource gateway %q not found", id)
}

func (m *Mock) cloneResourceGateway(g *driver.ResourceGateway) driver.ResourceGateway {
	out := *g
	out.Status = m.observe(g.ID, g.Status)
	out.SecurityGroupIDs = append([]string(nil), g.SecurityGroupIDs...)
	out.SubnetIDs = append([]string(nil), g.SubnetIDs...)

	return out
}

func (m *Mock) CreateResourceGateway(
	_ context.Context, in *driver.CreateResourceGatewayInput,
) (*driver.ResourceGateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id, err := idgen.GenerateLongID("rg-")
	if err != nil {
		return nil, err
	}

	g := &driver.ResourceGateway{
		ID:                          id,
		ARN:                         m.arn("resourcegateway/" + id),
		Name:                        in.Name,
		Status:                      resourceStatusActive,
		IPAddressType:               in.IPAddressType,
		Ipv4AddressesPerEni:         in.Ipv4AddressesPerEni,
		ResourceConfigDNSResolution: in.ResourceConfigDNSResolution,
		SecurityGroupIDs:            append([]string(nil), in.SecurityGroupIDs...),
		SubnetIDs:                   append([]string(nil), in.SubnetIDs...),
		VpcID:                       idFromIdentifier(in.VpcID),
		CreatedAt:                   m.now(),
		LastUpdatedAt:               m.now(),
	}
	m.resourceGws.Set(id, g)
	m.writeTags(g.ARN, in.Tags)
	m.beginSettle(id)

	out := m.cloneResourceGateway(g)

	return &out, nil
}

func (m *Mock) GetResourceGateway(_ context.Context, identifier string) (*driver.ResourceGateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	g, ok := m.resourceGws.Get(id)
	if !ok {
		return nil, resourceGatewayNotFound(id)
	}

	out := m.cloneResourceGateway(g)

	return &out, nil
}

func (m *Mock) UpdateResourceGateway(
	_ context.Context, identifier string, securityGroupIDs []string,
) (*driver.ResourceGateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	g, ok := m.resourceGws.Get(id)
	if !ok {
		return nil, resourceGatewayNotFound(id)
	}

	if securityGroupIDs != nil {
		g.SecurityGroupIDs = append([]string(nil), securityGroupIDs...)
	}

	g.LastUpdatedAt = m.now()

	out := m.cloneResourceGateway(g)

	return &out, nil
}

func (m *Mock) DeleteResourceGateway(_ context.Context, identifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	if !m.resourceGws.Has(id) {
		return resourceGatewayNotFound(id)
	}

	// A gateway a resource configuration still references cannot be deleted.
	if m.resourceGatewayInUse(id) {
		return inUsef("resource gateway %q is in use by a resource configuration", id)
	}

	m.statusSettle.Clear(id)
	m.resourceGws.Delete(id)

	return nil
}

func (m *Mock) ListResourceGateways(_ context.Context) ([]driver.ResourceGateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.resourceGws.All(), m.cloneResourceGateway), nil
}

// ---- Resource Endpoint Associations (not modeled; empty/no-op) ----

func (*Mock) ListResourceEndpointAssociations(
	_ context.Context,
) ([]driver.ResourceEndpointAssociation, error) {
	return []driver.ResourceEndpointAssociation{}, nil
}

func (*Mock) DeleteResourceEndpointAssociation(_ context.Context, id string) error {
	return errors.Newf(errors.NotFound, "resource endpoint association %q not found", id)
}
