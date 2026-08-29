package elasticache

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// SubnetResolver is the slice of the networking mock this package needs to
// derive a subnet group's VPC. Real ElastiCache infers VpcId from the member
// subnets, and callers tearing a VPC down list groups and match on that field
// — so it has to be resolved rather than left blank.
type SubnetResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
}

// SetSubnetResolver wires the networking mock in.
func (m *Mock) SetSubnetResolver(r SubnetResolver) {
	m.subnetResolver = r
}

// CreateCacheSubnetGroup creates a cache subnet group.
func (m *Mock) CreateCacheSubnetGroup(
	ctx context.Context, cfg cachedriver.SubnetGroupConfig,
) (*cachedriver.SubnetGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "CacheSubnetGroupName is required")
	}

	if len(cfg.SubnetIDs) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "at least one subnet is required")
	}

	if m.subnetGroups.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"CacheSubnetGroupAlreadyExists: cache subnet group %q already exists", cfg.Name)
	}

	sg := cachedriver.SubnetGroup{
		Name:        cfg.Name,
		Description: cfg.Description,
		SubnetIDs:   append([]string(nil), cfg.SubnetIDs...),
		VPCID:       m.resolveVPCID(ctx, cfg.SubnetIDs),
		Status:      "Complete",
		ARN: "arn:aws:elasticache:" + regionctx.RegionOr(ctx, m.opts.Region) + ":" + m.opts.AccountID +
			":subnetgroup:" + cfg.Name,
	}
	m.subnetGroups.Set(cfg.Name, sg)

	return &sg, nil
}

// DescribeCacheSubnetGroups returns the named groups, or all when none given.
func (m *Mock) DescribeCacheSubnetGroups(
	_ context.Context, names []string,
) ([]cachedriver.SubnetGroup, error) {
	if len(names) == 0 {
		return m.subnetGroups.SortedValues(), nil
	}

	out := make([]cachedriver.SubnetGroup, 0, len(names))

	for _, n := range names {
		sg, ok := m.subnetGroups.Get(n)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound,
				"CacheSubnetGroupNotFoundFault: cache subnet group %q not found", n)
		}

		out = append(out, sg)
	}

	return out, nil
}

// DeleteCacheSubnetGroup deletes a cache subnet group.
func (m *Mock) DeleteCacheSubnetGroup(_ context.Context, name string) error {
	// Real ElastiCache refuses to delete a subnet group associated with any
	// clusters — standalone cache clusters count, not only replication groups.
	// A teardown that skipped this would leave the group deleted and a live
	// cluster pointing at nothing.
	for _, cd := range m.caches.All() {
		if cd.info.SubnetGroupName == name {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"CacheSubnetGroupInUse: cache subnet group %q is in use by %q", name, cd.info.Name)
		}
	}

	for _, rg := range m.replicationGroups.All() {
		if rg.SubnetGroupName == name {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"CacheSubnetGroupInUse: cache subnet group %q is in use by %q", name, rg.ID)
		}
	}

	if !m.subnetGroups.Delete(name) {
		return cerrors.Newf(cerrors.NotFound,
			"CacheSubnetGroupNotFoundFault: cache subnet group %q not found", name)
	}

	return nil
}

// resolveVPCID reports the VPC the member subnets belong to.
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
