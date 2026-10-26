package networkfirewall

import (
	"context"

	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// SubnetResolver is the slice of the networking mock this package needs to put a
// firewall's subnet in the Availability Zone the subnet really sits in.
type SubnetResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
}

// SetSubnetResolver wires the networking mock in. Without it, or for a subnet id
// the networking mock does not know, a subnet gets a zone derived from its id.
func (m *Mock) SetSubnetResolver(r SubnetResolver) {
	m.subnetResolver = r
}

// resolveZone returns the real Availability Zone of a subnet, or "" when no
// resolver is wired or the subnet is unknown.
func (m *Mock) resolveZone(ctx context.Context, subnetID string) string {
	if m.subnetResolver == nil || subnetID == "" {
		return ""
	}

	subnets, err := m.subnetResolver.DescribeSubnets(ctx, []string{subnetID})
	if err != nil || len(subnets) == 0 {
		return ""
	}

	return subnets[0].AvailabilityZone
}
