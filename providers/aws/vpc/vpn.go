package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func orDefaultStr(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// CreateCustomerGateway creates a customer gateway (the on-prem VPN endpoint).
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateCustomerGateway(_ context.Context, cfg driver.CustomerGatewayConfig) (*driver.CustomerGateway, error) {
	if cfg.IPAddress == "" {
		return nil, errors.New(errors.InvalidArgument, "customer gateway IP address is required")
	}

	cgw := &driver.CustomerGateway{
		ID:        idgen.GenerateID("cgw-"),
		IPAddress: cfg.IPAddress,
		BGPASN:    cfg.BGPASN,
		Type:      orDefaultStr(cfg.Type, "ipsec.1"),
		State:     "available",
		Tags:      copyTags(cfg.Tags),
	}
	m.customerGateways.Set(cgw.ID, cgw)

	out := cloneCustomerGateway(cgw)

	return &out, nil
}

// DeleteCustomerGateway deletes a customer gateway.
func (m *Mock) DeleteCustomerGateway(_ context.Context, id string) error {
	if !m.customerGateways.Delete(id) {
		return errors.Newf(errors.NotFound, "customer gateway %q not found", id)
	}

	return nil
}

// DescribeCustomerGateways returns customer gateways matching ids.
func (m *Mock) DescribeCustomerGateways(_ context.Context, ids []string) ([]driver.CustomerGateway, error) {
	return describeResources(m.customerGateways, ids, cloneCustomerGateway), nil
}

// CreateVPNGateway creates a virtual private gateway.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateVPNGateway(_ context.Context, cfg driver.VPNGatewayConfig) (*driver.VPNGateway, error) {
	asn := cfg.AmazonSideASN
	if asn == 0 {
		asn = defaultAmazonSideASN
	}

	vgw := &driver.VPNGateway{
		ID:            idgen.GenerateID("vgw-"),
		Type:          orDefaultStr(cfg.Type, "ipsec.1"),
		State:         "available",
		AmazonSideASN: asn,
		Tags:          copyTags(cfg.Tags),
	}
	m.vpnGateways.Set(vgw.ID, vgw)

	out := cloneVPNGateway(vgw)

	return &out, nil
}

// DeleteVPNGateway deletes a virtual private gateway.
func (m *Mock) DeleteVPNGateway(_ context.Context, id string) error {
	if !m.vpnGateways.Delete(id) {
		return errors.Newf(errors.NotFound, "vpn gateway %q not found", id)
	}

	return nil
}

// DescribeVPNGateways returns VPN gateways matching ids.
func (m *Mock) DescribeVPNGateways(_ context.Context, ids []string) ([]driver.VPNGateway, error) {
	return describeResources(m.vpnGateways, ids, cloneVPNGateway), nil
}

// AttachVPNGateway attaches a VPN gateway to a VPC.
func (m *Mock) AttachVPNGateway(_ context.Context, vpnGatewayID, vpcID string) (*driver.VPNGateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vgw, ok := m.vpnGateways.Get(vpnGatewayID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "vpn gateway %q not found", vpnGatewayID)
	}

	if !m.vpcs.Has(vpcID) {
		return nil, errors.Newf(errors.InvalidArgument, "vpc %q not found", vpcID)
	}

	vgw.AttachedVPCID = vpcID
	vgw.AttachmentState = "attached"

	out := cloneVPNGateway(vgw)

	return &out, nil
}

// DetachVPNGateway detaches a VPN gateway from a VPC.
func (m *Mock) DetachVPNGateway(_ context.Context, vpnGatewayID, vpcID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	vgw, ok := m.vpnGateways.Get(vpnGatewayID)
	if !ok {
		return errors.Newf(errors.NotFound, "vpn gateway %q not found", vpnGatewayID)
	}

	if vgw.AttachedVPCID != vpcID {
		return errors.Newf(errors.InvalidArgument, "vpn gateway %q is not attached to vpc %q", vpnGatewayID, vpcID)
	}

	vgw.AttachedVPCID = ""
	vgw.AttachmentState = "detached"

	return nil
}

// CreateVPNConnection creates a site-to-site VPN connection.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateVPNConnection(_ context.Context, cfg driver.VPNConnectionConfig) (*driver.VPNConnection, error) {
	if !m.customerGateways.Has(cfg.CustomerGatewayID) {
		return nil, errors.Newf(errors.InvalidArgument, "customer gateway %q not found", cfg.CustomerGatewayID)
	}

	if cfg.VPNGatewayID == "" && cfg.TransitGatewayID == "" {
		return nil, errors.New(errors.InvalidArgument, "a vpn gateway or transit gateway is required")
	}

	vpn := &driver.VPNConnection{
		ID:                idgen.GenerateID("vpn-"),
		CustomerGatewayID: cfg.CustomerGatewayID,
		VPNGatewayID:      cfg.VPNGatewayID,
		TransitGatewayID:  cfg.TransitGatewayID,
		Type:              orDefaultStr(cfg.Type, "ipsec.1"),
		State:             "available",
		StaticRoutesOnly:  cfg.StaticRoutesOnly,
		Tags:              copyTags(cfg.Tags),
	}
	m.vpnConnections.Set(vpn.ID, vpn)

	out := cloneVPNConnection(vpn)

	return &out, nil
}

// DeleteVPNConnection deletes a VPN connection.
func (m *Mock) DeleteVPNConnection(_ context.Context, id string) error {
	if !m.vpnConnections.Delete(id) {
		return errors.Newf(errors.NotFound, "vpn connection %q not found", id)
	}

	return nil
}

// DescribeVPNConnections returns VPN connections matching ids.
func (m *Mock) DescribeVPNConnections(_ context.Context, ids []string) ([]driver.VPNConnection, error) {
	return describeResources(m.vpnConnections, ids, cloneVPNConnection), nil
}

func cloneCustomerGateway(c *driver.CustomerGateway) driver.CustomerGateway {
	out := *c
	out.Tags = copyTags(c.Tags)

	return out
}

func cloneVPNGateway(v *driver.VPNGateway) driver.VPNGateway {
	out := *v
	out.Tags = copyTags(v.Tags)

	return out
}

func cloneVPNConnection(v *driver.VPNConnection) driver.VPNConnection {
	out := *v
	out.Tags = copyTags(v.Tags)

	return out
}
