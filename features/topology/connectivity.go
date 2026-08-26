package topology

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
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
	// Connectivity analysis is an internal/system caller: a managed
	// (service-owned) instance must still be resolvable even when the account
	// hides managed resources from the public Describe API. Opt in so hiding
	// doesn't make a real instance look non-existent.
	instances, err := e.compute.DescribeInstances(ctx, []string{instanceID}, nil,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: true})
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

// findMatchingSGRule looks for a rule allowing the traffic in the rule
// collections attached to an instance. An id that is not a security group is
// tried as a network ACL: OCI attaches its subnet's security lists to an
// instance alongside the VNIC's network security groups, and traffic is
// allowed when either collection permits it.
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

	found := make(map[string]struct{}, len(groups))

	for _, sg := range groups {
		found[sg.ID] = struct{}{}

		rules := sg.EgressRules
		if ingress {
			rules = sg.IngressRules
		}

		match := matchRules(rules, sg.ID, port, protocol, targetIP)
		if match != nil {
			return match
		}
	}

	return e.findMatchingACLRule(ctx, unresolved(sgIDs, found), targetIP, port, protocol, ingress)
}

// findMatchingACLRule looks for an allow rule in the network ACLs attached to
// an instance. Only allow rules match: a deny rule does not permit traffic,
// and the security lists this serves carry allow rules exclusively.
func (e *Engine) findMatchingACLRule(
	ctx context.Context,
	aclIDs []string,
	targetIP string,
	port int,
	protocol string,
	ingress bool,
) *RuleMatch {
	if len(aclIDs) == 0 {
		return nil
	}

	acls, err := e.networking.DescribeNetworkACLs(ctx, aclIDs)
	if err != nil {
		return nil
	}

	for i := range acls {
		rules := aclRulesFor(acls[i].Rules, ingress)

		match := matchRules(rules, acls[i].ID, port, protocol, targetIP)
		if match != nil {
			return match
		}
	}

	return nil
}

// aclRulesFor projects one direction of an ACL's allow rules onto the shape
// matchRules evaluates.
func aclRulesFor(rules []netdriver.NetworkACLRule, ingress bool) []netdriver.SecurityRule {
	out := make([]netdriver.SecurityRule, 0, len(rules))

	for _, rule := range rules {
		if rule.Egress == ingress || rule.Action != actionAllow {
			continue
		}

		out = append(out, netdriver.SecurityRule{
			Protocol: rule.Protocol,
			CIDR:     rule.CIDR,
			FromPort: rule.FromPort,
			ToPort:   rule.ToPort,
		})
	}

	return out
}

// unresolved returns the ids the security group lookup did not account for.
func unresolved(ids []string, found map[string]struct{}) []string {
	out := make([]string, 0, len(ids))

	for _, id := range ids {
		if _, ok := found[id]; !ok {
			out = append(out, id)
		}
	}

	return out
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

// findRouteTableForSubnet resolves the route table that actually governs a
// subnet, the way the cloud does: an explicit association wins, and a subnet
// without one falls back to the VPC's main route table.
//
// Taking whichever table was listed first was only ever right while a VPC had
// exactly one. Once a main route table exists alongside caller-created ones,
// the arbitrary choice can land on a table whose routes do not govern the
// subnet, and every answer derived from it is then wrong — a reachable
// destination reads as blocked.
func (e *Engine) findRouteTableForSubnet(
	ctx context.Context,
	vpcID, subnetID string,
) (*netdriver.RouteTable, error) {
	tables, err := e.networking.DescribeRouteTables(ctx, nil)
	if err != nil {
		return nil, err
	}

	var main, anyInVPC *netdriver.RouteTable

	for i := range tables {
		if tables[i].VPCID != vpcID {
			continue
		}

		if anyInVPC == nil {
			anyInVPC = &tables[i]
		}

		for _, assoc := range tables[i].Associations {
			if assoc.Main {
				main = &tables[i]
			}

			if subnetID != "" && assoc.SubnetID == subnetID {
				return &tables[i], nil
			}
		}
	}

	if main != nil {
		return main, nil
	}

	// Drivers that do not model a main route table still get an answer rather
	// than the subnet reading as unroutable.
	return anyInVPC, nil
}
