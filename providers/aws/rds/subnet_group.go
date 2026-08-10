package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.SubnetGroups = (*Mock)(nil)

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
		ARN:         idgen.AWSARN("rds", m.opts.Region, m.opts.AccountID, "subgrp:"+cfg.Name),
	}
	m.subnetGroups.Set(cfg.Name, sg)

	return &sg, nil
}

// DescribeDBSubnetGroups returns the named groups, or all of them when no
// names are given.
//
//nolint:dupl // mirrors the other describe-by-name-or-all methods by design.
func (m *Mock) DescribeDBSubnetGroups(
	_ context.Context, names []string,
) ([]rdsdriver.SubnetGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		all := m.subnetGroups.SortedValues()
		for i := range all {
			all[i].SubnetIDs = cloneSlice(all[i].SubnetIDs)
		}

		return all, nil
	}

	out := make([]rdsdriver.SubnetGroup, 0, len(names))

	for _, n := range names {
		sg, ok := m.subnetGroups.Get(n)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound,
				"DBSubnetGroupNotFoundFault: db subnet group %q not found", n)
		}

		sg.SubnetIDs = cloneSlice(sg.SubnetIDs)
		out = append(out, sg)
	}

	return out, nil
}

// DeleteDBSubnetGroup deletes a DB subnet group.
func (m *Mock) DeleteDBSubnetGroup(_ context.Context, name string) error {
	// Hold the lock across the in-use scan + delete so a concurrent
	// CreateInstance can't slip an instance into the group between them.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Real RDS refuses while anything is still placed in the group, and a
	// caller tearing a VPC down depends on that: deleting the group out from
	// under a live instance would strand the instance in a group that no
	// longer exists instead of surfacing the ordering mistake.
	if user, ok := m.subnetGroupInUseBy(name); ok {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"InvalidDBSubnetGroupStateFault: db subnet group %q is in use by %q", name, user)
	}

	if !m.subnetGroups.Delete(name) {
		return cerrors.Newf(cerrors.NotFound,
			"DBSubnetGroupNotFoundFault: db subnet group %q not found", name)
	}

	return nil
}

// subnetGroupInUseBy names a resource still placed in the given subnet group.
func (m *Mock) subnetGroupInUseBy(name string) (string, bool) {
	instances := m.instances.All()
	for id := range instances {
		if instances[id].SubnetGroupName == name {
			return id, true
		}
	}

	clusters := m.clusters.All()
	for id := range clusters {
		if clusters[id].SubnetGroupName == name {
			return id, true
		}
	}

	return "", false
}

// resolveVPCID reports the VPC the member subnets belong to. Subnets spanning
// more than one VPC is not a case real RDS accepts, and the first match is the
// answer for every input this can legitimately receive.
func (m *Mock) resolveVPCID(ctx context.Context, subnetIDs []string) string {
	if m.subnetResolver == nil {
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
