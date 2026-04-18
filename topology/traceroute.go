package topology

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/errors"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
)

// TraceRoute returns the network path from a source instance to a destination IP.
func (e *Engine) TraceRoute(
	ctx context.Context,
	srcInstanceID, destIP string,
) ([]RouteHop, error) {
	src, err := e.resolveInstance(ctx, srcInstanceID)
	if err != nil {
		return nil, err
	}

	hops := []RouteHop{
		{Type: "instance", ResourceID: src.ID, Detail: src.PrivateIP},
		{Type: "subnet", ResourceID: src.SubnetID},
	}

	rt, err := e.findRouteTableForVPC(ctx, src.VPCID)
	if err != nil {
		return nil, err
	}

	if rt == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no route table found for VPC %s", src.VPCID)
	}

	hops = append(hops, RouteHop{Type: "route-table", ResourceID: rt.ID})

	matchedRoute := findMatchingRoute(rt.Routes, destIP)
	if matchedRoute == nil {
		hops = append(hops, RouteHop{
			Type:   "blocked",
			Detail: fmt.Sprintf("no route to %s", destIP),
		})

		return hops, nil
	}

	hops = append(hops, classifyRouteHop(matchedRoute))

	return hops, nil
}

func classifyRouteHop(r *netdriver.Route) RouteHop {
	switch r.TargetType {
	case "gateway":
		return RouteHop{
			Type:       "gateway",
			ResourceID: r.TargetID,
			Detail:     fmt.Sprintf("%s via internet gateway", r.DestinationCIDR),
		}
	case "nat-gateway":
		return RouteHop{
			Type:       "nat-gateway",
			ResourceID: r.TargetID,
			Detail:     fmt.Sprintf("%s via NAT gateway", r.DestinationCIDR),
		}
	case "peering":
		return RouteHop{
			Type:       "peering",
			ResourceID: r.TargetID,
			Detail:     fmt.Sprintf("%s via VPC peering", r.DestinationCIDR),
		}
	default:
		return RouteHop{
			Type:       "local",
			ResourceID: r.TargetID,
			Detail:     fmt.Sprintf("%s via local route", r.DestinationCIDR),
		}
	}
}
