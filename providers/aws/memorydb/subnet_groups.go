package memorydb

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func cloneSubnetGroup(in *mdbdriver.SubnetGroup) mdbdriver.SubnetGroup {
	g := *in
	g.Tags = copyTags(g.Tags)
	g.SupportedNetworkTypes = cloneStrings(g.SupportedNetworkTypes)

	subnets := make([]mdbdriver.Subnet, len(g.Subnets))

	for i := range g.Subnets {
		s := g.Subnets[i]
		s.SupportedNetworkTypes = cloneStrings(g.Subnets[i].SupportedNetworkTypes)
		subnets[i] = s
	}

	g.Subnets = subnets

	return g
}

func (m *Mock) buildSubnets(ids []string) []mdbdriver.Subnet {
	out := make([]mdbdriver.Subnet, 0, len(ids))
	for i, id := range ids {
		out = append(out, mdbdriver.Subnet{
			Identifier:            id,
			AvailabilityZone:      m.opts.Region + azSuffix(i+1),
			SupportedNetworkTypes: []string{"ipv4"},
		})
	}

	return out
}

// clusterUsesSubnetGroup reports whether any cluster references the group.
func (m *Mock) clusterUsesSubnetGroup(name string) bool {
	all := m.clusters.SortedValues()
	for i := range all {
		if all[i].SubnetGroupName == name {
			return true
		}
	}

	return false
}

// CreateSubnetGroup creates a subnet group.
func (m *Mock) CreateSubnetGroup(_ context.Context, cfg mdbdriver.CreateSubnetGroupConfig) (*mdbdriver.SubnetGroup, error) {
	if err := validName("subnet group", cfg.Name); err != nil {
		return nil, err
	}

	if len(cfg.SubnetIDs) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "at least one subnet id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.subnetGroups.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "subnet group %q already exists", cfg.Name)
	}

	g := mdbdriver.SubnetGroup{
		Name: cfg.Name, ARN: m.arn("subnetgroup", cfg.Name), Description: cfg.Description,
		VpcID: "vpc-mock", Subnets: m.buildSubnets(cfg.SubnetIDs),
		SupportedNetworkTypes: []string{"ipv4"}, Tags: copyTags(cfg.Tags),
	}
	m.subnetGroups.Set(cfg.Name, g)

	out := cloneSubnetGroup(&g)

	return &out, nil
}

// DescribeSubnetGroups returns all subnet groups, or the named ones.
func (m *Mock) DescribeSubnetGroups(_ context.Context, names []string) ([]mdbdriver.SubnetGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeByName(m.subnetGroups, names, cloneSubnetGroup, func(n string) error {
		return cerrors.Newf(cerrors.NotFound, "subnet group %q not found", n)
	})
}

// UpdateSubnetGroup updates a subnet group's description and/or subnets.
func (m *Mock) UpdateSubnetGroup(_ context.Context, cfg mdbdriver.UpdateSubnetGroupConfig) (*mdbdriver.SubnetGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.subnetGroups.Get(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "subnet group %q not found", cfg.Name)
	}

	if cfg.Description != "" {
		g.Description = cfg.Description
	}

	if len(cfg.SubnetIDs) > 0 {
		g.Subnets = m.buildSubnets(cfg.SubnetIDs)
	}

	m.subnetGroups.Set(cfg.Name, g)

	out := cloneSubnetGroup(&g)

	return &out, nil
}

// DeleteSubnetGroup removes a subnet group; in-use groups cannot be deleted.
func (m *Mock) DeleteSubnetGroup(_ context.Context, name string) (*mdbdriver.SubnetGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.subnetGroups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "subnet group %q not found", name)
	}

	if m.clusterUsesSubnetGroup(name) {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "subnet group %q is in use by a cluster", name)
	}

	m.subnetGroups.Delete(name)

	out := cloneSubnetGroup(&g)

	return &out, nil
}
