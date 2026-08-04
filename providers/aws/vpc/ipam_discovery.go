package vpc

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// newResourceDiscovery creates and stores a resource discovery. Caller holds mu.
func (m *Mock) newResourceDiscovery(
	isDefault bool, description string, tags map[string]string,
) *driver.IpamResourceDiscovery {
	id := idgen.GenerateID("ipam-res-disco-")
	rd := &driver.IpamResourceDiscovery{
		ID:               id,
		ARN:              m.ipamARN("ipam-resource-discovery/" + id),
		Region:           m.opts.Region,
		OwnerID:          m.opts.AccountID,
		OperatingRegions: []string{m.opts.Region},
		Description:      description,
		State:            "create-complete",
		IsDefault:        isDefault,
		Tags:             copyTags(tags),
	}
	m.ipamDiscoveries.Set(id, rd)

	return rd
}

// newRDAssociation associates a resource discovery with an IPAM. Caller holds mu.
func (m *Mock) newRDAssociation(
	ipam *driver.Ipam, rdID string, isDefault bool, tags map[string]string,
) *driver.IpamResourceDiscoveryAssociation {
	id := idgen.GenerateID("ipam-res-disco-assoc-")
	assoc := &driver.IpamResourceDiscoveryAssociation{
		ID:                      id,
		ARN:                     m.ipamARN("ipam-resource-discovery-association/" + id),
		IpamID:                  ipam.ID,
		IpamARN:                 ipam.ARN,
		IpamRegion:              ipam.Region,
		ResourceDiscoveryID:     rdID,
		OwnerID:                 m.opts.AccountID,
		State:                   "associate-complete",
		IsDefault:               isDefault,
		ResourceDiscoveryStatus: "active",
		Tags:                    copyTags(tags),
	}
	m.ipamRDAssociations.Set(id, assoc)

	return assoc
}

// CreateIpamResourceDiscovery creates a non-default resource discovery.
func (m *Mock) CreateIpamResourceDiscovery(
	_ context.Context, cfg driver.IpamResourceDiscoveryConfig,
) (*driver.IpamResourceDiscovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd := m.newResourceDiscovery(false, cfg.Description, cfg.Tags)

	if len(cfg.OperatingRegions) > 0 {
		rd.OperatingRegions = append([]string(nil), cfg.OperatingRegions...)
	}

	out := cloneResourceDiscovery(rd)

	return &out, nil
}

// DescribeIpamResourceDiscoveries returns resource discoveries matching ids.
func (m *Mock) DescribeIpamResourceDiscoveries(_ context.Context, ids []string) ([]driver.IpamResourceDiscovery, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipamDiscoveries, ids, cloneResourceDiscovery), nil
}

// ModifyIpamResourceDiscovery updates a resource discovery's description/regions.
func (m *Mock) ModifyIpamResourceDiscovery(
	_ context.Context, id, description string, operatingRegions []string,
) (*driver.IpamResourceDiscovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.ipamDiscoveries.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam resource discovery %q not found", id)
	}

	if rd.IsDefault {
		return nil, errors.Newf(errors.FailedPrecondition, "cannot modify default resource discovery %q", id)
	}

	rd.Description = description
	if len(operatingRegions) > 0 {
		rd.OperatingRegions = append([]string(nil), operatingRegions...)
	}

	out := cloneResourceDiscovery(rd)

	return &out, nil
}

// DeleteIpamResourceDiscovery deletes a non-default, unassociated resource discovery.
func (m *Mock) DeleteIpamResourceDiscovery(_ context.Context, id string) (*driver.IpamResourceDiscovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.ipamDiscoveries.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam resource discovery %q not found", id)
	}

	if rd.IsDefault {
		return nil, errors.Newf(errors.FailedPrecondition, "cannot delete default resource discovery %q", id)
	}

	for _, a := range m.ipamRDAssociations.SortedValues() {
		if a.ResourceDiscoveryID == id {
			return nil, errors.Newf(errors.FailedPrecondition, "resource discovery %q is associated", id)
		}
	}

	rd.State = ipamStateDeleteComplete

	m.ipamDiscoveries.Delete(id)

	out := cloneResourceDiscovery(rd)

	return &out, nil
}

// AssociateIpamResourceDiscovery associates a resource discovery with an IPAM.
func (m *Mock) AssociateIpamResourceDiscovery(
	_ context.Context, ipamID, resourceDiscoveryID string, tags map[string]string,
) (*driver.IpamResourceDiscoveryAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ipam, ok := m.ipams.Get(ipamID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "ipam %q not found", ipamID)
	}

	if !m.ipamDiscoveries.Has(resourceDiscoveryID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam resource discovery %q not found", resourceDiscoveryID)
	}

	assoc := m.newRDAssociation(ipam, resourceDiscoveryID, false, tags)

	ipam.ResourceDiscoveryAssociationCount++

	out := cloneRDAssociation(assoc)

	return &out, nil
}

// DisassociateIpamResourceDiscovery removes a resource-discovery association.
func (m *Mock) DisassociateIpamResourceDiscovery(
	_ context.Context, associationID string,
) (*driver.IpamResourceDiscoveryAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	assoc, ok := m.ipamRDAssociations.Get(associationID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "resource discovery association %q not found", associationID)
	}

	if assoc.IsDefault {
		return nil, errors.Newf(errors.FailedPrecondition, "cannot disassociate default association %q", associationID)
	}

	assoc.State = "disassociate-complete"

	m.ipamRDAssociations.Delete(associationID)

	if ipam, ok := m.ipams.Get(assoc.IpamID); ok {
		ipam.ResourceDiscoveryAssociationCount--
	}

	out := cloneRDAssociation(assoc)

	return &out, nil
}

// DescribeIpamResourceDiscoveryAssociations returns associations matching ids.
func (m *Mock) DescribeIpamResourceDiscoveryAssociations(
	_ context.Context, ids []string,
) ([]driver.IpamResourceDiscoveryAssociation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipamRDAssociations, ids, cloneRDAssociation), nil
}

// GetIpamDiscoveredAccounts returns the accounts a resource discovery monitors.
// The emulator is single-account, so it reports the configured account.
func (m *Mock) GetIpamDiscoveredAccounts(_ context.Context, resourceDiscoveryID, region string) ([]driver.IpamDiscoveredAccount, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.ipamDiscoveries.Has(resourceDiscoveryID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam resource discovery %q not found", resourceDiscoveryID)
	}

	return []driver.IpamDiscoveredAccount{{
		AccountID:                   m.opts.AccountID,
		DiscoveryRegion:             orDefaultStr(region, m.opts.Region),
		LastAttemptedDiscoveryTime:  time.Unix(0, 0).UTC(),
		LastSuccessfulDiscoveryTime: time.Unix(0, 0).UTC(),
	}}, nil
}

// GetIpamDiscoveredResourceCidrs returns the resource CIDRs a discovery found,
// derived from the stored VPCs/subnets.
func (m *Mock) GetIpamDiscoveredResourceCidrs(
	_ context.Context, resourceDiscoveryID, region string,
) ([]driver.IpamDiscoveredResourceCidr, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.ipamDiscoveries.Has(resourceDiscoveryID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam resource discovery %q not found", resourceDiscoveryID)
	}

	cidrs := m.ipamResourceCidrs()
	out := make([]driver.IpamDiscoveredResourceCidr, 0, len(cidrs))

	for i := range cidrs {
		c := cidrs[i]
		out = append(out, driver.IpamDiscoveredResourceCidr{
			ResourceDiscoveryID: resourceDiscoveryID, ResourceCIDR: c.ResourceCIDR, ResourceID: c.ResourceID,
			ResourceType: c.ResourceType, ResourceRegion: orDefaultStr(region, m.opts.Region),
			ResourceOwnerID: c.ResourceOwnerID, VPCID: c.VPCID, IPSource: "amazon", IPUsage: c.IPUsage,
			NetworkInterfaceAttachmentStatus: "available", SampleTime: time.Unix(0, 0).UTC(),
		})
	}

	return out, nil
}

// GetIpamDiscoveredPublicAddresses returns the public IPs a discovery found,
// derived from the stored Elastic IPs.
func (m *Mock) GetIpamDiscoveredPublicAddresses(
	_ context.Context, resourceDiscoveryID, region string,
) ([]driver.IpamDiscoveredPublicAddress, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.ipamDiscoveries.Has(resourceDiscoveryID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam resource discovery %q not found", resourceDiscoveryID)
	}

	eips := m.eips.SortedValues()
	out := make([]driver.IpamDiscoveredPublicAddress, 0, len(eips))

	for _, e := range eips {
		status := "disassociated"
		if e.AssociationID != "" {
			status = "associated"
		}

		out = append(out, driver.IpamDiscoveredPublicAddress{
			ResourceDiscoveryID: resourceDiscoveryID, Address: e.PublicIP, AddressAllocationID: e.AllocationID,
			AddressOwnerID: m.opts.AccountID, AddressRegion: orDefaultStr(region, m.opts.Region),
			AddressType: "amazon-owned-eip", AssociationStatus: status, Service: "ec2",
			SampleTime: time.Unix(0, 0).UTC(),
		})
	}

	return out, nil
}

func cloneResourceDiscovery(rd *driver.IpamResourceDiscovery) driver.IpamResourceDiscovery {
	out := *rd
	out.OperatingRegions = append([]string(nil), rd.OperatingRegions...)
	out.Tags = copyTags(rd.Tags)

	return out
}

func cloneRDAssociation(a *driver.IpamResourceDiscoveryAssociation) driver.IpamResourceDiscoveryAssociation {
	out := *a
	out.Tags = copyTags(a.Tags)

	return out
}
