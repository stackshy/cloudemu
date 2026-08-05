package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const hundredPercent = 100.0

// IpamMetrics derives the AWS/IPAM CloudWatch metrics from current state:
// per-IPAM TotalActiveIpCount, per-scope resource-CIDR counts, per-pool
// allocation percentages, public-IP insight counts, and per-VPC/subnet IP
// utilization. Values are computed live so they track the emulator's state.
func (m *Mock) IpamMetrics(_ context.Context) []driver.IpamMetric {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []driver.IpamMetric

	resourceCidrs := m.ipamResourceCidrs()

	out = append(out, m.ipamTopLevelMetrics(resourceCidrs)...)
	out = append(out, m.ipamScopeMetrics()...)
	out = append(out, m.ipamPoolMetrics()...)
	out = append(out, m.ipamPublicIPMetrics()...)
	out = append(out, m.ipamResourceUtilizationMetrics(resourceCidrs)...)

	return out
}

func metric(name string, value float64, unit string, dims map[string]string) driver.IpamMetric {
	return driver.IpamMetric{
		Namespace: driver.IpamMetricNamespace, MetricName: name, Value: value, Unit: unit, Dimensions: dims,
	}
}

// ipamTopLevelMetrics emits TotalActiveIpCount per IPAM (active = addresses on
// resource CIDRs the IPAM tracks).
func (m *Mock) ipamTopLevelMetrics(resourceCidrs []driver.IpamResourceCidr) []driver.IpamMetric {
	if m.ipams.Len() == 0 {
		return nil
	}

	var active float64

	for i := range resourceCidrs {
		c := resourceCidrs[i]
		if c.ResourceType == resourceTypeVPC {
			active += cidrSize(c.ResourceCIDR) * c.IPUsage
		}
	}

	out := make([]driver.IpamMetric, 0, m.ipams.Len())

	for _, i := range m.ipams.SortedValues() {
		out = append(out, metric("TotalActiveIpCount", active, "Count", map[string]string{"IpamId": i.ID}))
	}

	return out
}

// ipamScopeMetrics emits per-scope resource-CIDR compliance counts.
func (m *Mock) ipamScopeMetrics() []driver.IpamMetric {
	scopes := m.ipamScopes.SortedValues()
	out := make([]driver.IpamMetric, 0, len(scopes))

	managed := float64(m.vpcs.Len() + m.subnets.Len())

	for _, s := range scopes {
		dims := map[string]string{"ScopeID": s.ID}
		out = append(out,
			metric("ManagedResourceCidrs", managed, "Count", dims),
			metric("CompliantResourceCidrs", managed, "Count", dims),
			metric("NoncompliantResourceCidrs", 0, "Count", dims),
			metric("OverlappingResourceCidrs", 0, "Count", dims),
			metric("UnmanagedResourceCidrs", 0, "Count", dims),
		)
	}

	return out
}

// ipamPoolMetrics emits per-pool allocation percentages + compliance counts.
func (m *Mock) ipamPoolMetrics() []driver.IpamMetric {
	pools := m.ipamPools.SortedValues()
	out := make([]driver.IpamMetric, 0, len(pools))

	for _, p := range pools {
		supply := m.poolSupply(p.ID)
		assigned := m.poolAssigned(p.ID)

		var pctAssigned float64
		if supply > 0 {
			pctAssigned = assigned / supply * hundredPercent
		}

		dims := map[string]string{"PoolID": p.ID, "AddressFamily": p.AddressFamily}
		out = append(out,
			metric("PercentAssigned", pctAssigned, "Percent", dims),
			metric("PercentAllocated", pctAssigned, "Percent", dims),
			metric("PercentAvailable", hundredPercent-pctAssigned, "Percent", dims),
			metric("CompliantResourceCidrs", float64(m.poolAllocationCount(p.ID)), "Count", dims),
			metric("NoncompliantResourceCidrs", 0, "Count", dims),
		)
	}

	return out
}

// ipamPublicIPMetrics emits public-IP insight counts from Elastic IPs + BYOIP.
func (m *Mock) ipamPublicIPMetrics() []driver.IpamMetric {
	if m.ipams.Len() == 0 {
		return nil
	}

	var associated, unassociated float64

	for _, e := range m.eips.SortedValues() {
		if e.AssociationID != "" {
			associated++
		} else {
			unassociated++
		}
	}

	byoip := float64(m.ipamByoipCidrs.Len())

	ipams := m.ipams.SortedValues()
	out := make([]driver.IpamMetric, 0, len(ipams))

	for _, i := range ipams {
		dims := map[string]string{"IpamId": i.ID}
		out = append(out,
			metric("AmazonOwnedElasticIPs", associated+unassociated, "Count", dims),
			metric("AssociatedAmazonOwnedElasticIPs", associated, "Count", dims),
			metric("UnassociatedAmazonOwnedElasticIPs", unassociated, "Count", dims),
			metric("BringYourOwnIPs", byoip, "Count", dims),
		)
	}

	return out
}

// ipamResourceUtilizationMetrics emits VpcIPUsage / SubnetIPUsage per resource.
func (*Mock) ipamResourceUtilizationMetrics(resourceCidrs []driver.IpamResourceCidr) []driver.IpamMetric {
	var out []driver.IpamMetric

	for i := range resourceCidrs {
		c := resourceCidrs[i]
		switch c.ResourceType {
		case resourceTypeVPC:
			out = append(out, metric("VpcIPUsage", c.IPUsage*hundredPercent, "Percent", map[string]string{
				"VpcID": c.ResourceID, "AddressFamily": "IPv4", "Region": c.ResourceRegion,
			}))
		case resourceTypeSubnet:
			out = append(out, metric("SubnetIPUsage", c.IPUsage*hundredPercent, "Percent", map[string]string{
				"SubnetID": c.ResourceID, "VpcID": c.VPCID, "AddressFamily": "IPv4", "Region": c.ResourceRegion,
			}))
		}
	}

	return out
}

// poolSupply / poolAssigned / poolAllocationCount summarize a pool. Caller holds mu.
func (m *Mock) poolSupply(poolID string) float64 {
	var total float64

	for _, c := range m.ipamPoolCidrs.SortedValues() {
		if m.ipamPoolByCidr[c.ID] == poolID {
			total += cidrSize(c.CIDR)
		}
	}

	return total
}

func (m *Mock) poolAssigned(poolID string) float64 {
	var assigned float64

	for _, a := range m.ipamAllocations.SortedValues() {
		if m.ipamPoolByAllocation[a.ID] == poolID {
			assigned += cidrSize(a.CIDR)
		}
	}

	return assigned
}

func (m *Mock) poolAllocationCount(poolID string) int {
	var n int

	for _, a := range m.ipamAllocations.SortedValues() {
		if m.ipamPoolByAllocation[a.ID] == poolID {
			n++
		}
	}

	return n
}
