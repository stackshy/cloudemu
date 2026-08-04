package vpc

import (
	"context"
	"net"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	resourceTypeVPC    = "vpc"
	resourceTypeSubnet = "subnet"

	mgmtStateManaged   = "managed"
	mgmtStateUnmanaged = "unmanaged"
)

// ipamResourceOverride records the caller-requested scope move / unmonitor for
// a tracked resource CIDR. The base resource-CIDR list is derived fresh from
// VPCs/subnets on every read, so ModifyIpamResourceCidr persists its changes
// here and ipamResourceCidrs applies them.
type ipamResourceOverride struct {
	scopeID   string
	unmanaged bool
}

// ipamResourceCidrs derives IPAM's tracked resource CIDRs from the VPCs and
// subnets held in this mock. IPAM "monitors" existing network resources, so in
// the emulator those are the stored VPCs/subnets. Caller holds at least RLock.
func (m *Mock) ipamResourceCidrs() []driver.IpamResourceCidr {
	out := make([]driver.IpamResourceCidr, 0, m.vpcs.Len()+m.subnets.Len())

	for _, v := range m.vpcs.SortedValues() {
		out = append(out, driver.IpamResourceCidr{
			ResourceCIDR: v.CIDRBlock, ResourceID: v.ID, ResourceType: resourceTypeVPC, VPCID: v.ID,
			ResourceRegion: m.opts.Region, ResourceOwnerID: m.opts.AccountID,
			ComplianceStatus: "compliant", ManagementState: mgmtStateManaged, OverlapStatus: "nonoverlapping",
			IPUsage: m.vpcIPUsage(v.ID, v.CIDRBlock), Tags: copyTags(v.Tags),
		})
	}

	for _, s := range m.subnets.SortedValues() {
		out = append(out, driver.IpamResourceCidr{
			ResourceCIDR: s.CIDRBlock, ResourceID: s.ID, ResourceType: resourceTypeSubnet, VPCID: s.VPCID,
			ResourceRegion: m.opts.Region, ResourceOwnerID: m.opts.AccountID, AvailabilityZone: s.AvailabilityZone,
			ComplianceStatus: "compliant", ManagementState: mgmtStateManaged, OverlapStatus: "nonoverlapping",
			IPUsage: 0, Tags: copyTags(s.Tags),
		})
	}

	// Apply any persisted scope-move / unmonitor overrides from
	// ModifyIpamResourceCidr so Get/Describe/metrics reflect the change.
	for i := range out {
		ov, ok := m.ipamResourceOverrides[out[i].ResourceID]
		if !ok {
			continue
		}

		if ov.scopeID != "" {
			out[i].IpamScopeID = ov.scopeID
		}

		if ov.unmanaged {
			out[i].ManagementState = mgmtStateUnmanaged
		}
	}

	return out
}

// vpcIPUsage returns the fraction of a VPC's IPv4 space covered by its subnets.
func (m *Mock) vpcIPUsage(vpcID, vpcCIDR string) float64 {
	total := cidrSize(vpcCIDR)
	if total == 0 {
		return 0
	}

	var used float64

	for _, s := range m.subnets.SortedValues() {
		if s.VPCID == vpcID {
			used += cidrSize(s.CIDRBlock)
		}
	}

	return used / total
}

// cidrSize returns the number of IPv4 addresses in a CIDR, or 0 if unparseable.
func cidrSize(cidr string) float64 {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || ipnet.IP.To4() == nil {
		return 0
	}

	ones, bits := ipnet.Mask.Size()

	return float64(uint64(1) << uint(bits-ones)) //nolint:gosec // bits-ones is 0..32, no overflow
}

// GetIpamResourceCidrs returns the resource CIDRs IPAM tracks in a scope,
// optionally filtered to a single resource id.
func (m *Mock) GetIpamResourceCidrs(_ context.Context, scopeID, resourceID string) ([]driver.IpamResourceCidr, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if scopeID != "" && !m.ipamScopes.Has(scopeID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam scope %q not found", scopeID)
	}

	all := m.ipamResourceCidrs()
	if resourceID == "" {
		return all, nil
	}

	out := all[:0:0]

	for i := range all {
		if all[i].ResourceID == resourceID {
			out = append(out, all[i])
		}
	}

	return out, nil
}

// ModifyIpamResourceCidr adjusts monitoring/scope of a tracked resource CIDR.
// The emulator derives resource CIDRs from stored resources, so this echoes
// the requested state for the matching resource.
func (m *Mock) ModifyIpamResourceCidr(
	_ context.Context, resourceID, currentScopeID, destScopeID string, monitored bool,
) (*driver.IpamResourceCidr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cidrs := m.ipamResourceCidrs()
	for i := range cidrs {
		if cidrs[i].ResourceID != resourceID {
			continue
		}

		scopeID := destScopeID
		if scopeID == "" {
			scopeID = currentScopeID
		}

		// Persist the change so subsequent Get/Describe/metrics reads reflect
		// it — the base list is re-derived every call, so a purely local edit
		// would silently revert.
		m.ipamResourceOverrides[resourceID] = ipamResourceOverride{
			scopeID:   scopeID,
			unmanaged: !monitored,
		}

		c := cidrs[i]
		if scopeID != "" {
			c.IpamScopeID = scopeID
		}

		if !monitored {
			c.ManagementState = mgmtStateUnmanaged
		}

		out := c

		return &out, nil
	}

	return nil, errors.Newf(errors.NotFound, "resource %q not tracked by ipam", resourceID)
}

// GetIpamAddressHistory returns history records for a CIDR within a scope. The
// emulator has a single sample window per currently-tracked resource.
func (m *Mock) GetIpamAddressHistory(_ context.Context, cidr, scopeID string) ([]driver.IpamAddressHistoryRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if scopeID != "" && !m.ipamScopes.Has(scopeID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam scope %q not found", scopeID)
	}

	cidrs := m.ipamResourceCidrs()
	out := make([]driver.IpamAddressHistoryRecord, 0, len(cidrs))

	for i := range cidrs {
		c := cidrs[i]
		if cidr != "" && c.ResourceCIDR != cidr {
			continue
		}

		out = append(out, driver.IpamAddressHistoryRecord{
			ResourceCIDR: c.ResourceCIDR, ResourceID: c.ResourceID, ResourceType: c.ResourceType,
			ResourceRegion: c.ResourceRegion, ResourceOwnerID: c.ResourceOwnerID, VPCID: c.VPCID,
			ResourceComplianceStatus: c.ComplianceStatus, ResourceOverlapStatus: c.OverlapStatus,
			SampledStartTime: time.Unix(0, 0).UTC(), SampledEndTime: time.Unix(0, 0).UTC(),
		})
	}

	return out, nil
}
