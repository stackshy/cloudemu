package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
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

	info := toRouteTableInfo(rt)

	return &info, nil
}

// DeleteRouteTable deletes the route table with the given ID.
func (m *Mock) DeleteRouteTable(_ context.Context, id string) error {
	if !m.routeTables.Delete(id) {
		return errors.Newf(errors.NotFound, "route table %q not found", id)
	}

	return nil
}

// DescribeRouteTables returns route tables matching the given IDs, or all if empty.
func (m *Mock) DescribeRouteTables(_ context.Context, ids []string) ([]driver.RouteTable, error) {
	return describeResources(m.routeTables, ids, toRouteTableInfo), nil
}

// CreateRoute adds a route to the specified route table.
func (m *Mock) CreateRoute(
	_ context.Context, routeTableID, destinationCIDR, targetID, targetType string,
) error {
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
