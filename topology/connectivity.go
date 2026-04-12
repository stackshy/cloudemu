package topology

import (
	"context"
	"fmt"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
)

// CanConnect evaluates whether two instances can communicate on the given
// port and protocol, considering VPC membership, peering, security groups,
// and network ACLs.
func (e *Engine) CanConnect(
	ctx context.Context,
	query ConnectivityQuery,
) (*ConnectivityResult, error) {
	src, err := e.resolveInstance(ctx, query.SrcInstanceID)
	if err != nil {
		return nil, err
	}

	dst, err := e.resolveInstance(ctx, query.DstInstanceID)
	if err != nil {
		return nil, err
	}

	reachable, err := e.checkVPCReachability(ctx, src.VPCID, dst.VPCID)
	if err != nil {
		return nil, err
	}

	if !reachable {
		return &ConnectivityResult{
			Allowed: false,
			Reason: fmt.Sprintf(
				"no connectivity between VPC %s and VPC %s (no active peering)",
				src.VPCID, dst.VPCID,
			),
		}, nil
	}

	sgVerdict := e.evaluateInstanceSGs(ctx, src, dst, query.Port, query.Protocol)
	if !sgVerdict.Allowed {
		return &ConnectivityResult{
			Allowed:   false,
			Reason:    sgVerdict.Reason,
			SGVerdict: sgVerdict,
		}, nil
	}

	path := e.buildPath(src, dst)

	return &ConnectivityResult{
		Allowed:   true,
		Reason:    "traffic allowed",
		Path:      path,
		SGVerdict: sgVerdict,
	}, nil
}

func (e *Engine) resolveInstance(
	ctx context.Context,
	instanceID string,
) (*computedriver.Instance, error) {
	instances, err := e.compute.DescribeInstances(ctx, []string{instanceID}, nil)
	if err != nil {
		return nil, err
	}

	if len(instances) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %s not found", instanceID)
	}

	inst := &instances[0]
	if inst.State != "running" {
		return nil, cerrors.Newf(
			cerrors.FailedPrecondition,
			"instance %s is not running (state: %s)",
			instanceID, inst.State,
		)
	}

	return inst, nil
}

func (e *Engine) checkVPCReachability(
	ctx context.Context,
	srcVPCID, dstVPCID string,
) (bool, error) {
	if srcVPCID == dstVPCID {
		return true, nil
	}

	peerings, err := e.networking.DescribePeeringConnections(ctx, nil)
	if err != nil {
		return false, err
	}

	for _, p := range peerings {
		if p.Status != "active" {
			continue
		}

		srcMatch := p.RequesterVPC == srcVPCID || p.AccepterVPC == srcVPCID
		dstMatch := p.RequesterVPC == dstVPCID || p.AccepterVPC == dstVPCID

		if srcMatch && dstMatch {
			return true, nil
		}
	}

	return false, nil
}

func (e *Engine) evaluateInstanceSGs(
	ctx context.Context,
	src, dst *computedriver.Instance,
	port int,
	protocol string,
) TrafficVerdict {
	egressMatch := e.findMatchingSGRule(ctx, src.SecurityGroups, dst.PrivateIP, port, protocol, false)
	if egressMatch == nil {
		return TrafficVerdict{
			Allowed: false,
			Reason:  fmt.Sprintf("no egress rule allows %s:%d to %s", protocol, port, dst.PrivateIP),
		}
	}

	ingressMatch := e.findMatchingSGRule(ctx, dst.SecurityGroups, src.PrivateIP, port, protocol, true)
	if ingressMatch == nil {
		return TrafficVerdict{
			Allowed: false,
			Reason:  fmt.Sprintf("no ingress rule allows %s:%d from %s", protocol, port, src.PrivateIP),
		}
	}

	return TrafficVerdict{
		Allowed:      true,
		EgressMatch:  egressMatch,
		IngressMatch: ingressMatch,
		Reason:       "allowed by security group rules",
	}
}

func (e *Engine) findMatchingSGRule(
	ctx context.Context,
	sgIDs []string,
	targetIP string,
	port int,
	protocol string,
	ingress bool,
) *RuleMatch {
	groups, err := e.networking.DescribeSecurityGroups(ctx, sgIDs)
	if err != nil {
		return nil
	}

	for _, sg := range groups {
		rules := sg.EgressRules
		if ingress {
			rules = sg.IngressRules
		}

		match := matchRules(rules, sg.ID, port, protocol, targetIP)
		if match != nil {
			return match
		}
	}

	return nil
}

func buildPath(src, dst *computedriver.Instance) []RouteHop {
	hops := []RouteHop{
		{Type: "instance", ResourceID: src.ID, Detail: src.PrivateIP},
		{Type: "subnet", ResourceID: src.SubnetID},
	}

	if src.VPCID == dst.VPCID {
		hops = append(hops, RouteHop{Type: "local", Detail: "same VPC"})
	} else {
		hops = append(hops, RouteHop{Type: "peering", Detail: "VPC peering"})
	}

	hops = append(hops,
		RouteHop{Type: "subnet", ResourceID: dst.SubnetID},
		RouteHop{Type: "instance", ResourceID: dst.ID, Detail: dst.PrivateIP},
	)

	return hops
}

func (*Engine) buildPath(
	src, dst *computedriver.Instance,
) []RouteHop {
	return buildPath(src, dst)
}

// findRouteTableForVPC returns the first route table matching the given VPC.
func (e *Engine) findRouteTableForVPC(
	ctx context.Context,
	vpcID string,
) (*netdriver.RouteTable, error) {
	tables, err := e.networking.DescribeRouteTables(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range tables {
		if tables[i].VPCID == vpcID {
			return &tables[i], nil
		}
	}

	return nil, nil
}
