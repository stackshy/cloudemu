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
