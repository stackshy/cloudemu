package eks

import (
	"context"

	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// SubnetResolver is the slice of the networking mock EKS needs to derive a
// cluster's VPC from its subnets. Real EKS infers resourcesVpcConfig.vpcId from
// the subnets passed to CreateCluster rather than taking it as input.
type SubnetResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
}

// SetSubnetResolver wires the networking mock in so DescribeCluster can report
// the VPC the cluster's subnets belong to. Without it vpcId is left empty.
func (m *Mock) SetSubnetResolver(r SubnetResolver) {
	m.mu.Lock()
	m.subnetResolver = r
	m.mu.Unlock()
}

// resolveVpcID returns the VPC that the given subnets belong to, or "" when no
// resolver is wired or none of the subnets resolve. Caller holds m.mu.
func (m *Mock) resolveVpcID(ctx context.Context, subnetIDs []string) string {
	if m.subnetResolver == nil || len(subnetIDs) == 0 {
		return ""
	}

	subnets, err := m.subnetResolver.DescribeSubnets(ctx, subnetIDs)
	if err != nil {
		return ""
	}

	for _, s := range subnets {
		if s.VPCID != "" {
			return s.VPCID
		}
	}

	return ""
}
