package vcn

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Route target types, derived from the OCID of the network entity a route
// rule points at.
const (
	targetLocal          = "local"
	targetGateway        = "gateway"
	targetNATGateway     = "nat-gateway"
	targetServiceGateway = "service-gateway"
	targetPeering        = "peering"
	routeStateActive     = "active"
)

type routeTableData struct {
	ID        string
	VCNID     string
	Routes    []driver.Route
	Tags      map[string]string
	IsDefault bool
}

type rtAssocData struct {
	ID           string
	RouteTableID string
	SubnetID     string
}

// CreateRouteTable creates a route table in a VCN.
func (m *Mock) CreateRouteTable(_ context.Context, cfg driver.RouteTableConfig) (*driver.RouteTable, error) {
	if cfg.VPCID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VCN OCID is required")
	}

	v, ok := m.vcns.Get(cfg.VPCID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "VCN %q not found", cfg.VPCID)
	}

	return m.addRouteTable(v, cfg.Tags, false), nil
}

// newDefaultRouteTable creates the route table OCI attaches to a new VCN.
func (m *Mock) newDefaultRouteTable(v *vcnData) string {
	rt := m.addRouteTable(v, nil, true)

	return rt.ID
}

// addRouteTable stores a route table seeded with the VCN's local route.
func (m *Mock) addRouteTable(v *vcnData, tags map[string]string, isDefault bool) *driver.RouteTable {
	id := m.newOCID(typeRouteTable)
	rt := &routeTableData{
		ID:    id,
		VCNID: v.ID,
		Routes: []driver.Route{{
			DestinationCIDR: v.CIDRBlock,
			TargetID:        targetLocal,
			TargetType:      targetLocal,
			State:           routeStateActive,
		}},
		Tags:      copyTags(tags),
		IsDefault: isDefault,
	}

	m.routeTables.Set(id, rt)
	m.record(id)

	info := m.toRouteTableInfo(rt)

	return &info
}

// DeleteRouteTable deletes a route table. The VCN's default table can only be
// removed with the VCN itself, and an attached table has to be detached first.
func (m *Mock) DeleteRouteTable(_ context.Context, id string) error {
	rt, ok := m.routeTables.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "route table %q not found", id)
	}

	if rt.IsDefault {
		return cerrors.Newf(cerrors.FailedPrecondition, "cannot delete default route table %q", id)
	}

	for _, a := range m.rtAssocs.All() {
		if a.RouteTableID == id {
			return cerrors.Newf(cerrors.FailedPrecondition, "route table %q is still attached to a subnet", id)
		}
	}

	m.routeTables.Delete(id)
	m.forget(id)

	return nil
}

// DescribeRouteTables returns route tables matching the given OCIDs, or all
// if empty.
func (m *Mock) DescribeRouteTables(_ context.Context, ids []string) ([]driver.RouteTable, error) {
	return describeResources(m.routeTables, ids, m.toRouteTableInfo), nil
}

// CreateRoute adds a route rule to a route table.
func (m *Mock) CreateRoute(_ context.Context, routeTableID, destinationCIDR, targetID, targetType string) error {
	rt, ok := m.routeTables.Get(routeTableID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "route table %q not found", routeTableID)
	}

	for _, r := range rt.Routes {
		if r.DestinationCIDR == destinationCIDR {
			return cerrors.Newf(cerrors.AlreadyExists,
				"route for %q already exists in route table %q", destinationCIDR, routeTableID)
		}
	}

	if targetType == "" {
		targetType = TargetTypeOf(targetID)
	}

	rt.Routes = append(rt.Routes, driver.Route{
		DestinationCIDR: destinationCIDR,
		TargetID:        targetID,
		TargetType:      targetType,
		State:           routeStateActive,
	})

	return nil
}

// DeleteRoute removes a route rule from a route table.
func (m *Mock) DeleteRoute(_ context.Context, routeTableID, destinationCIDR string) error {
	rt, ok := m.routeTables.Get(routeTableID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "route table %q not found", routeTableID)
	}

	for i, r := range rt.Routes {
		if r.DestinationCIDR == destinationCIDR {
			rt.Routes = append(rt.Routes[:i], rt.Routes[i+1:]...)
			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "route %q not found in route table %q", destinationCIDR, routeTableID)
}

// ReplaceRoutes swaps a route table's whole rule set, which is how OCI's
// UpdateRouteTable behaves.
func (m *Mock) ReplaceRoutes(_ context.Context, routeTableID string, routes []driver.Route) error {
	if !m.routeTables.Update(routeTableID, func(rt *routeTableData) *routeTableData {
		rt.Routes = append([]driver.Route(nil), routes...)
		return rt
	}) {
		return cerrors.Newf(cerrors.NotFound, "route table %q not found", routeTableID)
	}

	return nil
}

// AssociateRouteTable attaches a route table to a subnet. A subnet has
// exactly one route table in OCI, so an existing attachment is replaced.
func (m *Mock) AssociateRouteTable(_ context.Context, routeTableID, subnetID string) (*driver.RouteTableAssociation, error) {
	if !m.routeTables.Has(routeTableID) {
		return nil, cerrors.Newf(cerrors.NotFound, "route table %q not found", routeTableID)
	}

	if !m.subnets.Has(subnetID) {
		return nil, cerrors.Newf(cerrors.NotFound, "subnet %q not found", subnetID)
	}

	for _, a := range m.rtAssocs.All() {
		if a.SubnetID == subnetID {
			m.rtAssocs.Delete(a.ID)
		}
	}

	id := assocHandle(routeTableID, subnetID)
	assoc := &rtAssocData{ID: id, RouteTableID: routeTableID, SubnetID: subnetID}

	m.rtAssocs.Set(id, assoc)

	info := toRTAssocInfo(assoc)

	return &info, nil
}

// DisassociateRouteTable detaches a route table from its subnet.
func (m *Mock) DisassociateRouteTable(_ context.Context, associationID string) error {
	if !m.rtAssocs.Delete(associationID) {
		return cerrors.Newf(cerrors.NotFound, "route table association %q not found", associationID)
	}

	return nil
}

// TargetTypeOf classifies the network entity a route rule points at from its
// OCID. OCI names the entity but not its kind, and the portable route carries
// the kind separately.
func TargetTypeOf(targetID string) string {
	switch {
	case strings.Contains(targetID, "."+typeInternetGW+"."):
		return targetGateway
	case strings.Contains(targetID, "."+typeNATGateway+"."):
		return targetNATGateway
	case strings.Contains(targetID, "."+typeServiceGateway+"."):
		return targetServiceGateway
	case strings.Contains(targetID, "."+typeLocalPeering+"."):
		return targetPeering
	default:
		return targetLocal
	}
}

// assocHandle names an attachment from its two ends. OCI has no association
// resource — a subnet simply carries a routeTableId — so this is a handle,
// not an OCID.
func assocHandle(routeTableID, subnetID string) string {
	return routeTableID + "|" + subnetID
}

func (m *Mock) toRouteTableInfo(rt *routeTableData) driver.RouteTable {
	routes := make([]driver.Route, len(rt.Routes))
	copy(routes, rt.Routes)

	var assocs []driver.RouteTableAssociation

	for _, a := range m.rtAssocs.SortedValues() {
		if a.RouteTableID == rt.ID {
			assocs = append(assocs, toRTAssocInfo(a))
		}
	}

	return driver.RouteTable{
		ID:           rt.ID,
		VPCID:        rt.VCNID,
		Routes:       routes,
		Tags:         copyTags(rt.Tags),
		Associations: assocs,
	}
}

func toRTAssocInfo(a *rtAssocData) driver.RouteTableAssociation {
	return driver.RouteTableAssociation{
		ID:           a.ID,
		RouteTableID: a.RouteTableID,
		SubnetID:     a.SubnetID,
	}
}
