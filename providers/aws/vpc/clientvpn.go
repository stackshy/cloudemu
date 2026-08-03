package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// CreateClientVPNEndpoint creates a Client VPN endpoint.
func (m *Mock) CreateClientVPNEndpoint(_ context.Context, cfg driver.ClientVPNEndpointConfig) (*driver.ClientVPNEndpoint, error) {
	if cfg.ClientCIDRBlock == "" {
		return nil, errors.New(errors.InvalidArgument, "clientCidrBlock is required")
	}

	if cfg.ServerCertificateARN == "" {
		return nil, errors.New(errors.InvalidArgument, "serverCertificateArn is required")
	}

	ep := &driver.ClientVPNEndpoint{
		ID:                   idgen.GenerateID("cvpn-endpoint-"),
		Description:          cfg.Description,
		ClientCIDRBlock:      cfg.ClientCIDRBlock,
		ServerCertificateARN: cfg.ServerCertificateARN,
		State:                "pending-associate",
		SplitTunnel:          cfg.SplitTunnel,
		Tags:                 copyTags(cfg.Tags),
	}
	m.clientVPNEndpoints.Set(ep.ID, ep)

	out := cloneClientVPNEndpoint(ep)

	return &out, nil
}

// DeleteClientVPNEndpoint deletes a Client VPN endpoint.
func (m *Mock) DeleteClientVPNEndpoint(_ context.Context, id string) error {
	if !m.clientVPNEndpoints.Delete(id) {
		return errors.Newf(errors.NotFound, "client vpn endpoint %q not found", id)
	}

	return nil
}

// DescribeClientVPNEndpoints returns Client VPN endpoints matching ids.
func (m *Mock) DescribeClientVPNEndpoints(_ context.Context, ids []string) ([]driver.ClientVPNEndpoint, error) {
	return describeResources(m.clientVPNEndpoints, ids, cloneClientVPNEndpoint), nil
}

// AssociateClientVPNTargetNetwork associates a subnet with a Client VPN endpoint,
// moving the endpoint to the available state.
func (m *Mock) AssociateClientVPNTargetNetwork(
	_ context.Context, endpointID, subnetID string,
) (*driver.ClientVPNTargetNetwork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ep, ok := m.clientVPNEndpoints.Get(endpointID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "client vpn endpoint %q not found", endpointID)
	}

	subnet, ok := m.subnets.Get(subnetID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "subnet %q not found", subnetID)
	}

	assoc := &driver.ClientVPNTargetNetwork{
		AssociationID: idgen.GenerateID("cvpn-assoc-"),
		EndpointID:    endpointID,
		SubnetID:      subnetID,
		VPCID:         subnet.VPCID,
		State:         "associated",
	}
	m.clientVPNAssocs.Set(assoc.AssociationID, assoc)

	ep.State = "available"
	ep.VPCID = subnet.VPCID

	out := *assoc

	return &out, nil
}

// DisassociateClientVPNTargetNetwork removes a target-network association.
func (m *Mock) DisassociateClientVPNTargetNetwork(_ context.Context, endpointID, associationID string) error {
	assoc, ok := m.clientVPNAssocs.Get(associationID)
	if !ok || assoc.EndpointID != endpointID {
		return errors.Newf(errors.NotFound, "association %q not found on endpoint %q", associationID, endpointID)
	}

	m.clientVPNAssocs.Delete(associationID)

	return nil
}

func cloneClientVPNEndpoint(e *driver.ClientVPNEndpoint) driver.ClientVPNEndpoint {
	out := *e
	out.Tags = copyTags(e.Tags)

	return out
}
