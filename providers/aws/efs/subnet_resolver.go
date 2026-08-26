package efs

import (
	"context"

	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// SubnetResolver is the slice of the networking mock EFS needs to derive a mount
// target's VPC and Availability Zone from its subnet. Real EFS infers both from
// the subnet rather than taking them as input, so mount targets of one file
// system always report the same VpcId and each reports its subnet's AZ.
type SubnetResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
}

// SetSubnetResolver wires the networking mock in. Without it a mount target
// still resolves a deterministic per-file-system VpcId and the region's first
// AZ, but it can't reflect the subnet's real VPC or zone.
func (m *Mock) SetSubnetResolver(r SubnetResolver) {
	m.subnetResolver = r
}

// resolveSubnet returns the subnet's info, or nil when no resolver is wired or
// the subnet cannot be resolved (unknown id, resolver error).
func (m *Mock) resolveSubnet(ctx context.Context, subnetID string) *netdriver.SubnetInfo {
	if m.subnetResolver == nil || subnetID == "" {
		return nil
	}

	subnets, err := m.subnetResolver.DescribeSubnets(ctx, []string{subnetID})
	if err != nil || len(subnets) == 0 {
		return nil
	}

	return &subnets[0]
}
