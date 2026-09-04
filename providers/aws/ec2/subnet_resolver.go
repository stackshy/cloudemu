package ec2

import (
	"context"

	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// SubnetResolver is the slice of the networking mock EC2 needs to derive an
// instance's VPC from its subnet at launch. Real EC2 infers VpcId from the
// subnet rather than taking it as input, and connectivity analysis / VPC
// teardown match on that field — so it has to be resolved, not left blank.
type SubnetResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
}

// SetSubnetResolver wires the networking mock in. Without it an instance
// launched with a subnet still records the subnet, but its VPCID is empty.
func (m *Mock) SetSubnetResolver(r SubnetResolver) {
	m.subnetResolver = r
}

// defaultSubnet returns the account/region's default subnet — the one a
// RunInstances call with no SubnetId lands in, matching real EC2. Real EC2
// picks a default subnet deterministically (its own internal ordering); this
// picks the lowest subnet id among the default VPC's default subnets, which is
// stable across calls for the same seeded state.
func (m *Mock) defaultSubnet(ctx context.Context) (netdriver.SubnetInfo, bool) {
	subs, err := m.subnetResolver.DescribeSubnets(ctx, nil)
	if err != nil {
		return netdriver.SubnetInfo{}, false
	}

	var best *netdriver.SubnetInfo

	for i := range subs {
		if !subs[i].IsDefault {
			continue
		}

		if best == nil || subs[i].ID < best.ID {
			best = &subs[i]
		}
	}

	if best == nil {
		return netdriver.SubnetInfo{}, false
	}

	return *best, true
}
