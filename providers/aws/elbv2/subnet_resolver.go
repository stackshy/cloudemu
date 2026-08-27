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
