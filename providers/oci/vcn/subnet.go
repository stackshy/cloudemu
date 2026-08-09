package vcn

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type subnetData struct {
	ID                 string
	VCNID              string
	CIDRBlock          string
	AvailabilityDomain string
	State              string
	Tags               map[string]string
}

// CreateSubnet creates a subnet inside a VCN. Real OCI requires the block to
// sit inside the VCN's and not overlap a sibling subnet.
func (m *Mock) CreateSubnet(_ context.Context, cfg driver.SubnetConfig) (*driver.SubnetInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.VPCID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VCN OCID is required")
	}

	if err := validateCIDR(cfg.CIDRBlock, "subnet"); err != nil {
		return nil, err
	}

	v, ok := m.vcns.Get(cfg.VPCID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "VCN %q not found", cfg.VPCID)
	}

	if err := m.validateSubnetCIDR(v, cfg.CIDRBlock); err != nil {
		return nil, err
	}

	id := m.newOCID(typeSubnet)
	s := &subnetData{
		ID:                 id,
		VCNID:              cfg.VPCID,
		CIDRBlock:          cfg.CIDRBlock,
		AvailabilityDomain: cfg.AvailabilityZone,
		State:              StateAvailable,
		Tags:               copyTags(cfg.Tags),
	}

	m.subnets.Set(id, s)
	m.record(id)

	info := toSubnetInfo(s)

	return &info, nil
}

// validateSubnetCIDR checks containment in the VCN and overlap with siblings.
func (m *Mock) validateSubnetCIDR(v *vcnData, cidr string) error {
	if !cidrContains(v.CIDRBlock, cidr) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"subnet CIDR block %q is not within VCN CIDR block %q", cidr, v.CIDRBlock)
	}

	for _, s := range m.subnets.All() {
		if s.VCNID == v.ID && cidrsOverlap(s.CIDRBlock, cidr) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"subnet CIDR block %q overlaps subnet %q", cidr, s.ID)
		}
	}

	return nil
}

// DeleteSubnet deletes a subnet, refusing while VNICs remain attached to it.
func (m *Mock) DeleteSubnet(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.subnets.Has(id) {
		return cerrors.Newf(cerrors.NotFound, "subnet %q not found", id)
	}

	for _, v := range m.vnics.All() {
		if v.SubnetID == id {
			return cerrors.Newf(cerrors.FailedPrecondition, "subnet %q still has VNICs attached", id)
		}
	}

	for _, a := range m.rtAssocs.All() {
		if a.SubnetID == id {
			m.rtAssocs.Delete(a.ID)
		}
	}

	m.subnets.Delete(id)
	m.forget(id)

	return nil
}

// DescribeSubnets returns subnets matching the given OCIDs, or all if empty.
func (m *Mock) DescribeSubnets(_ context.Context, ids []string) ([]driver.SubnetInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.subnets, ids, toSubnetInfo), nil
}

// UpdateSubnetTags merges freeform tags into the subnet's tag map.
func (m *Mock) UpdateSubnetTags(_ context.Context, id string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.Tags = mergeTagMap(s.Tags, tags)
		return s
	}) {
		return cerrors.Newf(cerrors.NotFound, "subnet %q not found", id)
	}

	return nil
}

// RemoveSubnetTags removes the given freeform tag keys from a subnet.
func (m *Mock) RemoveSubnetTags(_ context.Context, id string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.Tags = removeTagMapKeys(s.Tags, keys)
		return s
	}) {
		return cerrors.Newf(cerrors.NotFound, "subnet %q not found", id)
	}

	return nil
}

func toSubnetInfo(s *subnetData) driver.SubnetInfo {
	return driver.SubnetInfo{
		ID:               s.ID,
		VPCID:            s.VCNID,
		CIDRBlock:        s.CIDRBlock,
		AvailabilityZone: s.AvailabilityDomain,
		State:            s.State,
		Tags:             copyTags(s.Tags),
	}
}
