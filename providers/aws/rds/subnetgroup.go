package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// SubnetResolver is the slice of the networking mock this package needs to
// derive a subnet group's VPC. Real RDS infers VpcId from the member subnets
// rather than taking it as input, and callers tearing a VPC down list subnet
// groups and match on that field — so it has to be resolved, not left blank.
type SubnetResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
}

// SetSubnetResolver wires the networking mock in. Without it a subnet group
// still stores its members, but its VPCID is empty and a VPC teardown will
// not recognize the group as its own.
func (m *Mock) SetSubnetResolver(r SubnetResolver) {
	m.subnetResolver = r
}

// CreateDBSubnetGroup creates a DB subnet group.
//
// Name collisions are reported as DBSubnetGroupAlreadyExists: callers treat
// that specific code as "already provisioned, carry on", so collapsing it
// into a generic error would turn a re-run into a hard failure.
func (m *Mock) CreateDBSubnetGroup(
	ctx context.Context, cfg rdsdriver.SubnetGroupConfig,
) (*rdsdriver.SubnetGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBSubnetGroupName is required")
	}

	if len(cfg.SubnetIDs) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "at least one subnet is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.subnetGroups.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"DBSubnetGroupAlreadyExists: db subnet group %q already exists", cfg.Name)
	}

	sg := rdsdriver.SubnetGroup{
		Name:        cfg.Name,
		Description: cfg.Description,
		SubnetIDs:   append([]string(nil), cfg.SubnetIDs...),
		VPCID:       m.resolveVPCID(ctx, cfg.SubnetIDs),
		Status:      "Complete",
		ARN:         "arn:aws:rds:" + m.opts.Region + ":" + m.opts.AccountID + ":subgrp:" + cfg.Name,
	}
	m.subnetGroups.Set(cfg.Name, sg)

	return &sg, nil
}

// DescribeDBSubnetGroups returns the named groups, or all of them when no
// names are given.
func (m *Mock) DescribeDBSubnetGroups(
	_ context.Context, names []string,
) ([]rdsdriver.SubnetGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		return m.subnetGroups.SortedValues(), nil
	}

	out := make([]rdsdriver.SubnetGroup, 0, len(names))

	for _, n := range names {
		sg, ok := m.subnetGroups.Get(n)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound,
				"DBSubnetGroupNotFoundFault: db subnet group %q not found", n)
		}

		out = append(out, sg)
	}

	return out, nil
}

// DeleteDBSubnetGroup deletes a DB subnet group.
func (m *Mock) DeleteDBSubnetGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.subnetGroups.Delete(name) {
		return cerrors.Newf(cerrors.NotFound,
			"DBSubnetGroupNotFoundFault: db subnet group %q not found", name)
	}

	return nil
}

// resolveVPCID reports the VPC the member subnets belong to. Subnets spanning
// more than one VPC is not a case real RDS accepts, and the first match is the
// answer for every input this can legitimately receive.
func (m *Mock) resolveVPCID(ctx context.Context, subnetIDs []string) string {
	if m.subnetResolver == nil {
		return ""
	}

	subnets, err := m.subnetResolver.DescribeSubnets(ctx, subnetIDs)
	if err != nil {
		return ""
	}

	for i := range subnets {
		if subnets[i].VPCID != "" {
			return subnets[i].VPCID
		}
	}

	return ""
}
