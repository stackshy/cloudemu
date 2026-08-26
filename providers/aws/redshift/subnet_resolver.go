package redshift

import (
	"context"

	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// SubnetResolver is the slice of the networking mock this package needs to
// derive a cluster subnet group's VPC and per-subnet availability zones. Real
// Redshift infers VpcId from the member subnets rather than taking it as input,
// and returns each subnet's availability zone on describe — so both have to be
// resolved, not left blank.
type SubnetResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
}

// SetSubnetResolver wires the networking mock in. Without it a subnet group
// still stores its members, but its VpcId is empty and the Subnets list carries
// no availability zones.
func (m *Mock) SetSubnetResolver(r SubnetResolver) {
	m.subnetResolver = r
}

// resolveSubnets reports the VPC the member subnets belong to and builds the
// per-subnet detail list (id + availability zone). Every input subnet id is
// preserved in the returned list even when the resolver can't see it, so
// subnet_ids always round-trip; the AZ is filled in when known.
func (m *Mock) resolveSubnets(ctx context.Context, subnetIDs []string) (string, []Subnet) {
	details := make([]Subnet, 0, len(subnetIDs))

	if m.subnetResolver == nil {
		for _, id := range subnetIDs {
			details = append(details, Subnet{ID: id})
		}

		return "", details
	}

	azByID, vpcID := m.lookupSubnets(ctx, subnetIDs)

	for _, id := range subnetIDs {
		details = append(details, Subnet{ID: id, AvailabilityZone: azByID[id]})
	}

	return vpcID, details
}

// lookupSubnets resolves the requested subnets into an id→AZ map and the VPC id
// they belong to (the first non-empty one). Subnets spanning more than one VPC
// is not a case real Redshift accepts, so the first match is the answer.
func (m *Mock) lookupSubnets(ctx context.Context, subnetIDs []string) (map[string]string, string) {
	subnets, err := m.subnetResolver.DescribeSubnets(ctx, subnetIDs)
	if err != nil || len(subnets) == 0 {
		return nil, ""
	}

	azByID := make(map[string]string, len(subnets))

	var vpcID string

	for i := range subnets {
		azByID[subnets[i].ID] = subnets[i].AvailabilityZone
		if vpcID == "" && subnets[i].VPCID != "" {
			vpcID = subnets[i].VPCID
		}
	}

	return azByID, vpcID
}
