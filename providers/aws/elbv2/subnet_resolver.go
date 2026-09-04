package elbv2

import (
	"context"

	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// SubnetResolver is the slice of the networking mock this package needs to
// derive a load balancer's VPC. Real ELBv2 infers VpcId from the member subnets
// rather than taking it as input, and callers wiring a load balancer to a
// target group or security group by VpcId read that field — so it has to be
// resolved, not left blank.
type SubnetResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
}

// SetSubnetResolver wires the networking mock in. Without it a load balancer
// still stores its subnets, but its VpcId is empty.
func (m *Mock) SetSubnetResolver(r SubnetResolver) {
	m.subnetResolver = r
}

// resolveVPCID reports the VPC the member subnets belong to, or "" when no
// resolver is wired or the subnets cannot be resolved.
func (m *Mock) resolveVPCID(ctx context.Context, subnetIDs []string) string {
	if m.subnetResolver == nil || len(subnetIDs) == 0 {
		return ""
	}

	subnets, err := m.subnetResolver.DescribeSubnets(ctx, subnetIDs)
	if err != nil || len(subnets) == 0 {
		return ""
	}

	for i := range subnets {
		if subnets[i].VPCID != "" {
			return subnets[i].VPCID
		}
	}

	return ""
}

// resolveAZs maps each of the given subnet IDs to its availability zone, real
// ELBv2 reports the AZ each member subnet actually sits in — a multi-AZ load
// balancer's AvailabilityZones must reflect the real placement, not a single
// stand-in zone repeated for every subnet. A subnet the resolver can't place
// (no resolver wired, or the subnet is unknown) is simply absent from the
// result; the caller falls back to a placeholder zone for it.
func (m *Mock) resolveAZs(ctx context.Context, subnetIDs []string) map[string]string {
	if m.subnetResolver == nil || len(subnetIDs) == 0 {
		return nil
	}

	subnets, err := m.subnetResolver.DescribeSubnets(ctx, subnetIDs)
	if err != nil || len(subnets) == 0 {
		return nil
	}

	azs := make(map[string]string, len(subnets))

	for i := range subnets {
		if subnets[i].AvailabilityZone != "" {
			azs[subnets[i].ID] = subnets[i].AvailabilityZone
		}
	}

	return azs
}
