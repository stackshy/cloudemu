package vpc

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const defaultAmazonSideASN = 64512

// CreateTransitGateway creates a transit gateway.
func (m *Mock) CreateTransitGateway(_ context.Context, cfg driver.TransitGatewayConfig) (*driver.TransitGateway, error) {
	asn := cfg.ASN
	if asn == 0 {
		asn = defaultAmazonSideASN
	}

	tgw := &driver.TransitGateway{
		ID:          idgen.GenerateID("tgw-"),
		State:       "available",
		ASN:         asn,
		Description: cfg.Description,
		OwnerID:     m.opts.AccountID,
		Tags:        copyTags(cfg.Tags),
	}
	m.transitGateways.Set(tgw.ID, tgw)

	out := cloneTGW(tgw)

	return &out, nil
}

// DeleteTransitGateway deletes a transit gateway.
func (m *Mock) DeleteTransitGateway(_ context.Context, id string) (*driver.TransitGateway, error) {
	tgw, ok := m.transitGateways.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "transit gateway %q not found", id)
	}

	tgw.State = NATStateDeleted

	m.transitGateways.Delete(id)

	out := cloneTGW(tgw)

	return &out, nil
}

// DescribeTransitGateways returns transit gateways matching ids (all if empty).
func (m *Mock) DescribeTransitGateways(_ context.Context, ids []string) ([]driver.TransitGateway, error) {
	return describeResources(m.transitGateways, ids, cloneTGW), nil
}

// CreateTransitGatewayVPCAttachment attaches a VPC to a transit gateway.
func (m *Mock) CreateTransitGatewayVPCAttachment(
	_ context.Context, cfg driver.TransitGatewayVPCAttachmentConfig,
) (*driver.TransitGatewayVPCAttachment, error) {
	if !m.transitGateways.Has(cfg.TransitGatewayID) {
		return nil, errors.Newf(errors.InvalidArgument, "transit gateway %q not found", cfg.TransitGatewayID)
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return nil, errors.Newf(errors.InvalidArgument, "vpc %q not found", cfg.VPCID)
	}

	att := &driver.TransitGatewayVPCAttachment{
		ID:               idgen.GenerateID("tgw-attach-"),
		TransitGatewayID: cfg.TransitGatewayID,
		VPCID:            cfg.VPCID,
		SubnetIDs:        append([]string(nil), cfg.SubnetIDs...),
		State:            "available",
		Tags:             copyTags(cfg.Tags),
	}
	m.tgwAttachments.Set(att.ID, att)

	out := cloneTGWAttachment(att)

	return &out, nil
}

// DeleteTransitGatewayVPCAttachment deletes a transit gateway VPC attachment.
func (m *Mock) DeleteTransitGatewayVPCAttachment(_ context.Context, id string) (*driver.TransitGatewayVPCAttachment, error) {
	att, ok := m.tgwAttachments.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "transit gateway attachment %q not found", id)
	}

	att.State = NATStateDeleted

	m.tgwAttachments.Delete(id)

	out := cloneTGWAttachment(att)

	return &out, nil
}

// DescribeTransitGatewayVPCAttachments returns attachments matching ids.
func (m *Mock) DescribeTransitGatewayVPCAttachments(_ context.Context, ids []string) ([]driver.TransitGatewayVPCAttachment, error) {
	return describeResources(m.tgwAttachments, ids, cloneTGWAttachment), nil
}

// CreateTransitGatewayRouteTable creates a route table on a transit gateway.
func (m *Mock) CreateTransitGatewayRouteTable(
	_ context.Context, transitGatewayID string, tags map[string]string,
) (*driver.TransitGatewayRouteTable, error) {
	if !m.transitGateways.Has(transitGatewayID) {
		return nil, errors.Newf(errors.InvalidArgument, "transit gateway %q not found", transitGatewayID)
	}

	rt := &driver.TransitGatewayRouteTable{
		ID:               idgen.GenerateID("tgw-rtb-"),
		TransitGatewayID: transitGatewayID,
		State:            "available",
		Tags:             copyTags(tags),
	}
	m.tgwRouteTables.Set(rt.ID, rt)

	out := cloneTGWRouteTable(rt)

	return &out, nil
}

// DeleteTransitGatewayRouteTable deletes a transit gateway route table.
func (m *Mock) DeleteTransitGatewayRouteTable(_ context.Context, id string) (*driver.TransitGatewayRouteTable, error) {
	rt, ok := m.tgwRouteTables.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "transit gateway route table %q not found", id)
	}

	rt.State = NATStateDeleted

	m.tgwRouteTables.Delete(id)

	out := cloneTGWRouteTable(rt)

	return &out, nil
}

// DescribeTransitGatewayRouteTables returns route tables matching ids.
func (m *Mock) DescribeTransitGatewayRouteTables(_ context.Context, ids []string) ([]driver.TransitGatewayRouteTable, error) {
	return describeResources(m.tgwRouteTables, ids, cloneTGWRouteTable), nil
}

func tgwRouteKey(routeTableID, cidr string) string { return routeTableID + "|" + cidr }

func tgwAssocKey(routeTableID, attachmentID string) string { return routeTableID + "|" + attachmentID }

// CreateTransitGatewayRoute adds a static route to a TGW route table.
func (m *Mock) CreateTransitGatewayRoute(
	_ context.Context, routeTableID, destinationCIDR, attachmentID string,
) (*driver.TransitGatewayRoute, error) {
	if !m.tgwRouteTables.Has(routeTableID) {
		return nil, errors.Newf(errors.InvalidArgument, "transit gateway route table %q not found", routeTableID)
	}

	route := &driver.TransitGatewayRoute{
		DestinationCIDR: destinationCIDR,
		AttachmentID:    attachmentID,
		Type:            "static",
		State:           "active",
	}
	m.tgwRoutes.Set(tgwRouteKey(routeTableID, destinationCIDR), route)

	out := *route

	return &out, nil
}

// DeleteTransitGatewayRoute removes a route from a TGW route table.
func (m *Mock) DeleteTransitGatewayRoute(
	_ context.Context, routeTableID, destinationCIDR string,
) (*driver.TransitGatewayRoute, error) {
	key := tgwRouteKey(routeTableID, destinationCIDR)

	route, ok := m.tgwRoutes.Get(key)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "transit gateway route %q not found", destinationCIDR)
	}

	route.State = NATStateDeleted
	m.tgwRoutes.Delete(key)

	out := *route

	return &out, nil
}

// SearchTransitGatewayRoutes returns the routes of a TGW route table.
func (m *Mock) SearchTransitGatewayRoutes(_ context.Context, routeTableID string) ([]driver.TransitGatewayRoute, error) {
	if !m.tgwRouteTables.Has(routeTableID) {
		return nil, errors.Newf(errors.InvalidArgument, "transit gateway route table %q not found", routeTableID)
	}

	prefix := routeTableID + "|"

	var out []driver.TransitGatewayRoute

	for _, k := range m.tgwRoutes.Keys() {
		if strings.HasPrefix(k, prefix) {
			if r, ok := m.tgwRoutes.Get(k); ok {
				out = append(out, *r)
			}
		}
	}

	return out, nil
}

// AssociateTransitGatewayRouteTable associates an attachment with a route table.
func (m *Mock) AssociateTransitGatewayRouteTable(
	_ context.Context, routeTableID, attachmentID string,
) (*driver.TransitGatewayRouteTableAssociation, error) {
	if !m.tgwRouteTables.Has(routeTableID) {
		return nil, errors.Newf(errors.InvalidArgument, "transit gateway route table %q not found", routeTableID)
	}

	att, ok := m.tgwAttachments.Get(attachmentID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "transit gateway attachment %q not found", attachmentID)
	}

	assoc := &driver.TransitGatewayRouteTableAssociation{
		RouteTableID: routeTableID,
		AttachmentID: attachmentID,
		ResourceID:   att.VPCID,
		ResourceType: "vpc",
		State:        "associated",
	}
	m.tgwAssociations.Set(tgwAssocKey(routeTableID, attachmentID), assoc)

	out := *assoc

	return &out, nil
}

// EnableTransitGatewayRouteTablePropagation enables propagation from an
// attachment into a route table.
func (m *Mock) EnableTransitGatewayRouteTablePropagation(_ context.Context, routeTableID, attachmentID string) error {
	return m.requireRouteTableAndAttachment(routeTableID, attachmentID)
}

// DisableTransitGatewayRouteTablePropagation disables propagation.
func (m *Mock) DisableTransitGatewayRouteTablePropagation(_ context.Context, routeTableID, attachmentID string) error {
	return m.requireRouteTableAndAttachment(routeTableID, attachmentID)
}

func (m *Mock) requireRouteTableAndAttachment(routeTableID, attachmentID string) error {
	if !m.tgwRouteTables.Has(routeTableID) || !m.tgwAttachments.Has(attachmentID) {
		return errors.New(errors.InvalidArgument, "route table or attachment not found")
	}

	return nil
}

func cloneTGW(t *driver.TransitGateway) driver.TransitGateway {
	c := *t
	c.Tags = copyTags(t.Tags)

	return c
}

func cloneTGWAttachment(a *driver.TransitGatewayVPCAttachment) driver.TransitGatewayVPCAttachment {
	c := *a
	c.SubnetIDs = append([]string(nil), a.SubnetIDs...)
	c.Tags = copyTags(a.Tags)

	return c
}

func cloneTGWRouteTable(t *driver.TransitGatewayRouteTable) driver.TransitGatewayRouteTable {
	c := *t
	c.Tags = copyTags(t.Tags)

	return c
}
