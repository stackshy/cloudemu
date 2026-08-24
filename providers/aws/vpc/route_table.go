package vpc

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Route target type constants.
const (
	RouteTargetLocal = "local"
)

type routeTableData struct {
	ID     string
	VPCID  string
	Routes []driver.Route
	Tags   map[string]string
	// IsMain marks the route table EC2 creates alongside the VPC. It cannot be
	// deleted on its own and disappears with the VPC.
	IsMain bool
}

// CreateRouteTable creates a route table for the specified VPC.
func (m *Mock) CreateRouteTable(_ context.Context, cfg driver.RouteTableConfig) (*driver.RouteTable, error) {
	if cfg.VPCID == "" {
		return nil, errors.New(errors.InvalidArgument, "VPC ID is required")
	}

	v, ok := m.vpcs.Get(cfg.VPCID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "vpc %q not found", cfg.VPCID)
	}

	id := idgen.GenerateID("rtb-")
	localRoute := driver.Route{
		DestinationCIDR: v.CIDRBlock,
		TargetID:        RouteTargetLocal,
		TargetType:      RouteTargetLocal,
		State:           "active",
	}

	rt := &routeTableData{
		ID:     id,
		VPCID:  cfg.VPCID,
		Routes: []driver.Route{localRoute},
		Tags:   copyTags(cfg.Tags),
	}
	m.routeTables.Set(id, rt)

	m.mu.RLock()
	info := toRouteTableInfo(rt)
	m.mu.RUnlock()

	return &info, nil
}

// DeleteRouteTable deletes the route table with the given ID.
//
// The VPC's main route table cannot be deleted on its own — real EC2 refuses
// it, and a caller sweeping a VPC's route tables must skip it rather than
// treat the failure as a broken teardown.
func (m *Mock) DeleteRouteTable(_ context.Context, id string) error {
	rt, ok := m.routeTables.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "route table %q not found", id)
	}

	if rt.IsMain {
		return errors.Newf(errors.FailedPrecondition,
			"DependencyViolation: cannot delete the main route table %q of vpc %q", id, rt.VPCID)
	}

	// Real EC2 refuses to delete a route table still associated with a subnet;
	// the caller must DisassociateRouteTable first.
	for _, a := range m.rtAssocs.All() {
		if a.RouteTableID == id && !a.Main {
			return errors.Newf(errors.FailedPrecondition,
				"DependencyViolation: route table %q has a subnet association %q and cannot be deleted", id, a.ID)
		}
	}

	m.routeTables.Delete(id)

	return nil
}

// DescribeRouteTables returns route tables matching the given IDs, or all if empty.
//
// Associations are joined in here rather than kept on the route table itself:
// they live in their own store (a subnet can be re-pointed at another table),
// and Describe is the only channel through which a caller can learn an
// association ID — which it must have before it can disassociate.
func (m *Mock) DescribeRouteTables(_ context.Context, ids []string) ([]driver.RouteTable, error) {
	m.mu.RLock()

	for _, id := range ids {
		if !m.routeTables.Has(id) {
			m.mu.RUnlock()

			return nil, errors.Newf(errors.NotFound, "route table %q not found", id)
		}
	}

	tables := describeResources(m.routeTables, ids, toRouteTableInfo)
	m.mu.RUnlock()

	byTable := make(map[string][]driver.RouteTableAssociation)

	for _, a := range m.rtAssocs.All() {
		byTable[a.RouteTableID] = append(byTable[a.RouteTableID], toRTAssocInfo(a))
	}

	for i := range tables {
		assocs := byTable[tables[i].ID]
		// Stable order: the backing store is a map, and a caller diffing
		// successive Describe calls should not see phantom churn.
		sort.Slice(assocs, func(x, y int) bool { return assocs[x].ID < assocs[y].ID })

		tables[i].Associations = assocs
	}

	return tables, nil
}

// CreateRoute adds a route to the specified route table.
func (m *Mock) CreateRoute(
	_ context.Context, routeTableID, destinationCIDR, targetID, targetType string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rt, ok := m.routeTables.Get(routeTableID)
	if !ok {
		return errors.Newf(errors.NotFound, "route table %q not found", routeTableID)
	}

	for _, r := range rt.Routes {
		if r.DestinationCIDR == destinationCIDR {
			return errors.Newf(errors.AlreadyExists,
				"route for %q already exists in route table %q", destinationCIDR, routeTableID)
		}
	}

	rt.Routes = append(rt.Routes, driver.Route{
		DestinationCIDR: destinationCIDR,
		TargetID:        targetID,
		TargetType:      targetType,
		State:           "active",
	})

	return nil
}

// DeleteRoute removes a route from the specified route table.
func (m *Mock) DeleteRoute(_ context.Context, routeTableID, destinationCIDR string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rt, ok := m.routeTables.Get(routeTableID)
	if !ok {
		return errors.Newf(errors.NotFound, "route table %q not found", routeTableID)
	}

	for i, r := range rt.Routes {
		if r.DestinationCIDR == destinationCIDR {
			rt.Routes = append(rt.Routes[:i], rt.Routes[i+1:]...)
			return nil
		}
	}

	return errors.Newf(errors.NotFound, "route %q not found in route table %q",
		destinationCIDR, routeTableID)
}

func toRouteTableInfo(rt *routeTableData) driver.RouteTable {
	routes := make([]driver.Route, len(rt.Routes))
	copy(routes, rt.Routes)

	return driver.RouteTable{
		ID:     rt.ID,
		VPCID:  rt.VPCID,
		Routes: routes,
		Tags:   copyTags(rt.Tags),
	}
}
