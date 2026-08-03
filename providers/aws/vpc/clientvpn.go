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

// DescribeClientVPNTargetNetworks returns the target-network associations.
func (m *Mock) DescribeClientVPNTargetNetworks(_ context.Context, endpointID string) ([]driver.ClientVPNTargetNetwork, error) {
	if !m.clientVPNEndpoints.Has(endpointID) {
		return nil, errors.Newf(errors.NotFound, "client vpn endpoint %q not found", endpointID)
	}

	var out []driver.ClientVPNTargetNetwork

	for _, a := range m.clientVPNAssocs.SortedValues() {
		if a.EndpointID == endpointID {
			out = append(out, *a)
		}
	}

	return out, nil
}

// AuthorizeClientVPNIngress authorizes a client CIDR to reach a target network.
func (m *Mock) AuthorizeClientVPNIngress(
	_ context.Context, endpointID, targetCIDR, groupID string, accessAll bool,
) (*driver.ClientVPNAuthorizationRule, error) {
	if !m.clientVPNEndpoints.Has(endpointID) {
		return nil, errors.Newf(errors.NotFound, "client vpn endpoint %q not found", endpointID)
	}

	rule := &driver.ClientVPNAuthorizationRule{
		EndpointID: endpointID, TargetCIDR: targetCIDR, GroupID: groupID,
		AccessAll: accessAll, Status: "active",
	}
	m.clientVPNAuthRules.Set(endpointID+"|"+targetCIDR, rule)

	out := *rule

	return &out, nil
}

// RevokeClientVPNIngress revokes an authorization rule.
func (m *Mock) RevokeClientVPNIngress(_ context.Context, endpointID, targetCIDR string) error {
	if !m.clientVPNAuthRules.Delete(endpointID + "|" + targetCIDR) {
		return errors.Newf(errors.NotFound, "authorization rule for %q not found", targetCIDR)
	}

	return nil
}

// DescribeClientVPNAuthorizationRules returns the endpoint's authorization rules.
func (m *Mock) DescribeClientVPNAuthorizationRules(_ context.Context, endpointID string) ([]driver.ClientVPNAuthorizationRule, error) {
	if !m.clientVPNEndpoints.Has(endpointID) {
		return nil, errors.Newf(errors.NotFound, "client vpn endpoint %q not found", endpointID)
	}

	var out []driver.ClientVPNAuthorizationRule

	for _, rule := range m.clientVPNAuthRules.SortedValues() {
		if rule.EndpointID == endpointID {
			out = append(out, *rule)
		}
	}

	return out, nil
}

// CreateClientVPNRoute adds a route to a Client VPN endpoint.
func (m *Mock) CreateClientVPNRoute(
	_ context.Context, endpointID, destinationCIDR, targetSubnetID string,
) (*driver.ClientVPNRoute, error) {
	if !m.clientVPNEndpoints.Has(endpointID) {
		return nil, errors.Newf(errors.NotFound, "client vpn endpoint %q not found", endpointID)
	}

	route := &driver.ClientVPNRoute{
		EndpointID: endpointID, DestinationCIDR: destinationCIDR,
		TargetSubnetID: targetSubnetID, Status: "active",
	}
	m.clientVPNRoutes.Set(endpointID+"|"+destinationCIDR+"|"+targetSubnetID, route)

	out := *route

	return &out, nil
}

// DeleteClientVPNRoute removes a route from a Client VPN endpoint.
func (m *Mock) DeleteClientVPNRoute(_ context.Context, endpointID, destinationCIDR, targetSubnetID string) error {
	if !m.clientVPNRoutes.Delete(endpointID + "|" + destinationCIDR + "|" + targetSubnetID) {
		return errors.Newf(errors.NotFound, "client vpn route %q not found", destinationCIDR)
	}

	return nil
}

// DescribeClientVPNRoutes returns the endpoint's routes.
func (m *Mock) DescribeClientVPNRoutes(_ context.Context, endpointID string) ([]driver.ClientVPNRoute, error) {
	if !m.clientVPNEndpoints.Has(endpointID) {
		return nil, errors.Newf(errors.NotFound, "client vpn endpoint %q not found", endpointID)
	}

	var out []driver.ClientVPNRoute

	for _, route := range m.clientVPNRoutes.SortedValues() {
		if route.EndpointID == endpointID {
			out = append(out, *route)
		}
	}

	return out, nil
}
