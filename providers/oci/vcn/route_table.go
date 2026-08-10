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
	m.mu.Lock()
	defer m.mu.Unlock()

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
			DestinationCIDR: primaryCIDR(v),
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
	m.mu.Lock()
	defer m.mu.Unlock()

	rt, ok := m.routeTables.Get(id)
	if !ok {
		return routeTableNotFound(id)
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.routeTables, ids, m.toRouteTableInfo), nil
}

// CreateRoute adds a route rule to a route table.
func (m *Mock) CreateRoute(_ context.Context, routeTableID, destinationCIDR, targetID, targetType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if targetType == "" {
		targetType = TargetTypeOf(targetID)
	}

	return mutate(m.routeTables, routeTableID, routeTableNotFound(routeTableID), func(rt *routeTableData) error {
		if err := m.validateRoute(rt.VCNID, destinationCIDR, targetID); err != nil {
			return err
		}

		for _, r := range rt.Routes {
			if r.DestinationCIDR == destinationCIDR {
				return cerrors.Newf(cerrors.AlreadyExists,
					"route for %q already exists in route table %q", destinationCIDR, routeTableID)
			}
		}

		rt.Routes = appendItem(rt.Routes, driver.Route{
			DestinationCIDR: destinationCIDR,
			TargetID:        targetID,
			TargetType:      targetType,
			State:           routeStateActive,
		})

		return nil
	})
}

// DeleteRoute removes a route rule from a route table.
func (m *Mock) DeleteRoute(_ context.Context, routeTableID, destinationCIDR string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return mutate(m.routeTables, routeTableID, routeTableNotFound(routeTableID), func(rt *routeTableData) error {
		for i, r := range rt.Routes {
			if r.DestinationCIDR == destinationCIDR {
				rt.Routes = removeAt(rt.Routes, i)

				return nil
			}
		}

		return cerrors.Newf(cerrors.NotFound, "route %q not found in route table %q", destinationCIDR, routeTableID)
	})
}

func routeTableNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "route table %q not found", id)
}

// ReplaceRoutes swaps a route table's whole rule set, which is how OCI's
// UpdateRouteTable behaves. Every rule is checked before any is stored, so a
// rejected set leaves the table as it was.
func (m *Mock) ReplaceRoutes(_ context.Context, routeTableID string, routes []driver.Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return mutate(m.routeTables, routeTableID, routeTableNotFound(routeTableID), func(rt *routeTableData) error {
		for i := range routes {
			if err := m.validateRoute(rt.VCNID, routes[i].DestinationCIDR, routes[i].TargetID); err != nil {
				return err
			}
		}

		rt.Routes = append([]driver.Route(nil), routes...)

		return nil
	})
}

// validateRoute rejects a rule real OCI would not accept: an unparseable
// destination, or a target that is missing, detached, or attached to another
// VCN. TraceRoute would otherwise walk a dead gateway as a reachable hop.
func (m *Mock) validateRoute(vcnID, destinationCIDR, targetID string) error {
	if err := validateCIDR(destinationCIDR, "route destination"); err != nil {
		return err
	}

	if targetID == "" || targetID == targetLocal {
		return nil
	}

	served, known := m.routeTargetVCNs(targetID)
	if !known {
		return cerrors.Newf(cerrors.NotFound, "route target %q not found", targetID)
	}

	for _, id := range served {
		if id == vcnID {
			return nil
		}
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"route target %q is not attached to VCN %q", targetID, vcnID)
}

// routeTargetVCNs returns the VCNs a network entity carries traffic for, and
// whether the OCID names an entity at all. A detached internet gateway is
// known but serves none, so no route table can point at it.
func (m *Mock) routeTargetVCNs(targetID string) (vcnIDs []string, known bool) {
	if igw, ok := m.igws.Get(targetID); ok {
		if igw.State != StateAttached {
			return nil, true
		}

		return []string{igw.VCNID}, true
	}

	if nat, ok := m.natGateways.Get(targetID); ok {
		return []string{nat.VCNID}, true
	}

	if sg, ok := m.serviceGWs.Get(targetID); ok {
		return []string{sg.VCNID}, true
	}

	if p, ok := m.peerings.Get(targetID); ok {
		return []string{p.RequesterVCN, p.AccepterVCN}, true
	}

	// OCI route rules name the local peering gateway on this side, not the
	// connection, so a rule may point at one whether or not it is peered yet.
	if g, ok := m.lpgs.Get(targetID); ok {
		return []string{g.VCNID}, true
	}

	return nil, false
}

// AssociateRouteTable attaches a route table to a subnet. A subnet has
// exactly one route table in OCI, so an existing attachment is replaced.
func (m *Mock) AssociateRouteTable(_ context.Context, routeTableID, subnetID string) (*driver.RouteTableAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.routeTables.Has(routeTableID) {
		return nil, routeTableNotFound(routeTableID)
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
	m.mu.Lock()
	defer m.mu.Unlock()

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

	// A subnet naming no route table is governed by the VCN's default one, and
	// the portable model spells that implicit attachment as the main
	// association. Without it a reader falls back to whichever table it saw
	// first once a VCN has more than one.
	if rt.IsDefault {
		assocs = append(assocs, driver.RouteTableAssociation{
			ID:           assocHandle(rt.ID, ""),
			RouteTableID: rt.ID,
			Main:         true,
		})
	}

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
