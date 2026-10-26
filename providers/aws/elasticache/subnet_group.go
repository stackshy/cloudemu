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
// subnets, and callers tearing a VPC down list groups and match on that field,
// so it has to be resolved rather than left blank.
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

	// The VPC lookup is a call into the networking mock, so it runs before mu
	// is taken.
	sg := cachedriver.SubnetGroup{
		Name:        cfg.Name,
		Description: cfg.Description,
		SubnetIDs:   append([]string(nil), cfg.SubnetIDs...),
		VPCID:       m.resolveVPCID(ctx, cfg.SubnetIDs),
		Status:      "Complete",
		ARN: "arn:aws:elasticache:" + regionctx.RegionOr(ctx, m.opts.Region) + ":" + m.opts.AccountID +
			":subnetgroup:" + cfg.Name,
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.subnetGroups.SetIfAbsent(cfg.Name, sg) {
		return nil, newFault(cerrors.AlreadyExists, FaultCacheSubnetGroupAlreadyExists,
			"cache subnet group %q already exists", cfg.Name)
	}

	out := sg
	out.SubnetIDs = append([]string(nil), sg.SubnetIDs...)

	return &out, nil
}

// DescribeCacheSubnetGroups returns the named groups, or all when none given.
func (m *Mock) DescribeCacheSubnetGroups(
	_ context.Context, names []string,
) ([]cachedriver.SubnetGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(names) == 0 {
		return m.subnetGroups.SortedValues(), nil
	}

	out := make([]cachedriver.SubnetGroup, 0, len(names))

	for _, n := range names {
		sg, ok := m.subnetGroups.Get(n)
		if !ok {
			return nil, subnetGroupNotFound(n)
		}

		out = append(out, sg)
	}

	return out, nil
}

// DeleteCacheSubnetGroup deletes a cache subnet group.
//
// Real ElastiCache refuses to delete a subnet group associated with any
// clusters: standalone cache clusters count, not only replication groups. A
// teardown that skipped this would leave the group deleted and a live cluster
// pointing at nothing. The scan and the delete run under mu, and clusters still
// being created count as users, so a concurrent create cannot slip in between.
func (m *Mock) DeleteCacheSubnetGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if user, inUse := m.subnetGroupUser(name); inUse {
		return newFault(cerrors.FailedPrecondition, FaultCacheSubnetGroupInUse,
			"cache subnet group %q is in use by %q", name, user)
	}

	if !m.subnetGroups.Delete(name) {
		return subnetGroupNotFound(name)
	}

	return nil
}

// subnetGroupUser returns a cluster or replication group (stored or reserved)
// placed in the named subnet group. The caller holds mu.
func (m *Mock) subnetGroupUser(name string) (string, bool) {
	return m.firstUser(func(info *cachedriver.CacheInfo) bool {
		return info.SubnetGroupName == name
	}, func(rg *cachedriver.ReplicationGroup) bool {
		return rg.SubnetGroupName == name
	})
}

// firstUser returns the id of the first cluster or replication group, stored or
// reserved, matching the predicates. The caller holds mu.
func (m *Mock) firstUser(
	cacheUses func(*cachedriver.CacheInfo) bool, rgUses func(*cachedriver.ReplicationGroup) bool,
) (string, bool) {
	for _, cd := range m.caches.All() {
		if cacheUses(&cd.info) {
			return cd.info.Name, true
		}
	}

	for _, r := range m.cacheRes {
		if cacheUses(&r.record) {
			return r.record.Name, true
		}
	}

	groups := m.replicationGroups.All()
	for id := range groups {
		rg := groups[id]
		if rgUses(&rg) {
			return id, true
		}
	}

	for _, r := range m.rgRes {
		if rgUses(&r.record) {
			return r.record.ID, true
		}
	}

	return "", false
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
