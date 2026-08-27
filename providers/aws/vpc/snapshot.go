package vpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// vpcSnapshot is the full serialized state of the AWS VPC mock. Every memstore
// store is dumped keyed by its resource id so cross-references (a subnet's
// VPCID, an instance's SubnetID/SecurityGroups held in EC2) still resolve after
// a restore. Stores whose value type is fully exported round-trip through the
// generic memstore helper; enis carries an exported form because eniData has an
// unexported field. The mutexes and the wired *config.Options are intentionally
// not serialized.
type vpcSnapshot struct {
	// Core VPC stores.
	VPCs           json.RawMessage `json:"vpcs,omitempty"`
	Subnets        json.RawMessage `json:"subnets,omitempty"`
	SecurityGroups json.RawMessage `json:"securityGroups,omitempty"`
	Peerings       json.RawMessage `json:"peerings,omitempty"`
	NATGateways    json.RawMessage `json:"natGateways,omitempty"`
	FlowLogs       json.RawMessage `json:"flowLogs,omitempty"`
	RouteTables    json.RawMessage `json:"routeTables,omitempty"`
	NetworkACLs    json.RawMessage `json:"networkAcls,omitempty"`
	ACLAssocs      json.RawMessage `json:"aclAssocs,omitempty"`
	IGWs           json.RawMessage `json:"igws,omitempty"`
	EIPs           json.RawMessage `json:"eips,omitempty"`
	RTAssocs       json.RawMessage `json:"rtAssocs,omitempty"`
	Endpoints      json.RawMessage `json:"endpoints,omitempty"`

	// enis promotes its stored eniData (which has an unexported field) to an
	// exported snapshot form.
	ENIs map[string]*eniSnapshot `json:"enis,omitempty"`

	// Transit-gateway family.
	TransitGateways  json.RawMessage `json:"transitGateways,omitempty"`
	TGWAttachments   json.RawMessage `json:"tgwAttachments,omitempty"`
	TGWRouteTables   json.RawMessage `json:"tgwRouteTables,omitempty"`
	TGWRoutes        json.RawMessage `json:"tgwRoutes,omitempty"`
	TGWAssociations  json.RawMessage `json:"tgwAssociations,omitempty"`
	CustomerGateways json.RawMessage `json:"customerGateways,omitempty"`
	VPNGateways      json.RawMessage `json:"vpnGateways,omitempty"`
	VPNConnections   json.RawMessage `json:"vpnConnections,omitempty"`

	// Miscellaneous EC2-family capabilities.
	DHCPOptions        json.RawMessage `json:"dhcpOptions,omitempty"`
	PrefixLists        json.RawMessage `json:"prefixLists,omitempty"`
	EgressOnlyIGWs     json.RawMessage `json:"egressOnlyIgws,omitempty"`
	EndpointServices   json.RawMessage `json:"endpointServices,omitempty"`
	ClientVPNEndpoints json.RawMessage `json:"clientVpnEndpoints,omitempty"`
	ClientVPNAssocs    json.RawMessage `json:"clientVpnAssocs,omitempty"`
	ClientVPNAuthRules json.RawMessage `json:"clientVpnAuthRules,omitempty"`
	ClientVPNRoutes    json.RawMessage `json:"clientVpnRoutes,omitempty"`

	// IPAM family.
	IPAMs               json.RawMessage `json:"ipams,omitempty"`
	IPAMScopes          json.RawMessage `json:"ipamScopes,omitempty"`
	IPAMPools           json.RawMessage `json:"ipamPools,omitempty"`
	IPAMPoolCidrs       json.RawMessage `json:"ipamPoolCidrs,omitempty"`
	IPAMAllocations     json.RawMessage `json:"ipamAllocations,omitempty"`
	IPAMDiscoveries     json.RawMessage `json:"ipamDiscoveries,omitempty"`
	IPAMRDAssociations  json.RawMessage `json:"ipamRdAssociations,omitempty"`
	IPAMByoasns         json.RawMessage `json:"ipamByoasns,omitempty"`
	IPAMByoipCidrs      json.RawMessage `json:"ipamByoipCidrs,omitempty"`
	IPAMResolvers       json.RawMessage `json:"ipamResolvers,omitempty"`
	IPAMResolverTargets json.RawMessage `json:"ipamResolverTargets,omitempty"`
	IPAMTokens          json.RawMessage `json:"ipamTokens,omitempty"`
	IPAMPolicies        json.RawMessage `json:"ipamPolicies,omitempty"`

	// Traffic-mirror / network-insights family.
	TrafficMirrorTargets               json.RawMessage `json:"trafficMirrorTargets,omitempty"`
	TrafficMirrorFilters               json.RawMessage `json:"trafficMirrorFilters,omitempty"`
	TrafficMirrorSessions              json.RawMessage `json:"trafficMirrorSessions,omitempty"`
	NetworkInsightsPaths               json.RawMessage `json:"networkInsightsPaths,omitempty"`
	NetworkInsightsAnalyses            json.RawMessage `json:"networkInsightsAnalyses,omitempty"`
	NetworkInsightsAccessScopes        json.RawMessage `json:"networkInsightsAccessScopes,omitempty"`
	NetworkInsightsAccessScopeAnalyses json.RawMessage `json:"networkInsightsAccessScopeAnalyses,omitempty"`
	VPCBPAExclusions                   json.RawMessage `json:"vpcBpaExclusions,omitempty"`

	// mu-guarded scalar/map state.
	VPCBPAOptions         *driver.VPCBlockPublicAccessOptions     `json:"vpcBpaOptions,omitempty"`
	EndpointServicePerms  map[string][]string                     `json:"endpointServicePerms,omitempty"`
	IPAMPoolByCidr        map[string]string                       `json:"ipamPoolByCidr,omitempty"`
	IPAMPoolByAllocation  map[string]string                       `json:"ipamPoolByAllocation,omitempty"`
	IPAMResourceOverrides map[string]ipamResourceOverrideSnapshot `json:"ipamResourceOverrides,omitempty"`
	ENIIPCounters         map[string]int                          `json:"eniIpCounters,omitempty"`
}

// eniSnapshot mirrors eniData, promoting its one unexported field
// (deleteOnTermination) to an exported one so it survives JSON.
type eniSnapshot struct {
	ID                  string            `json:"id"`
	VPCID               string            `json:"vpcId,omitempty"`
	SubnetID            string            `json:"subnetId,omitempty"`
	Status              string            `json:"status,omitempty"`
	AttachmentID        string            `json:"attachmentId,omitempty"`
	InstanceID          string            `json:"instanceId,omitempty"`
	DeviceIndex         int               `json:"deviceIndex,omitempty"`
	Description         string            `json:"description,omitempty"`
	PrivateIP           string            `json:"privateIp,omitempty"`
	MacAddress          string            `json:"macAddress,omitempty"`
	SourceDestCheck     bool              `json:"sourceDestCheck,omitempty"`
	SecurityGroups      []string          `json:"securityGroups,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
	DeleteOnTermination bool              `json:"deleteOnTermination,omitempty"`
}

// ipamResourceOverrideSnapshot is the exported form of ipamResourceOverride.
type ipamResourceOverrideSnapshot struct {
	ScopeID   string `json:"scopeId,omitempty"`
	Unmanaged bool   `json:"unmanaged,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// VPC holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := vpcSnapshot{ENIs: m.snapshotENIs()}

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.snapshotScalarState(&snap)

	return json.Marshal(snap)
}

// snapshotENIs promotes each stored eniData to its exported snapshot form.
func (m *Mock) snapshotENIs() map[string]*eniSnapshot {
	if m.enis.Len() == 0 {
		return nil
	}

	out := make(map[string]*eniSnapshot, m.enis.Len())

	for id, d := range m.enis.All() {
		out[id] = &eniSnapshot{
			ID: d.ID, VPCID: d.VPCID, SubnetID: d.SubnetID, Status: d.Status,
			AttachmentID: d.AttachmentID, InstanceID: d.InstanceID, DeviceIndex: d.DeviceIndex,
			Description: d.Description, PrivateIP: d.PrivateIP, MacAddress: d.MacAddress,
			SourceDestCheck: d.SourceDestCheck, SecurityGroups: d.SecurityGroups, Tags: d.Tags,
			DeleteOnTermination: d.deleteOnTermination,
		}
	}

	return out
}

func (m *Mock) snapshotStores(snap *vpcSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.VPCs, m.vpcs.Snapshot},
		{&snap.Subnets, m.subnets.Snapshot},
		{&snap.SecurityGroups, m.securityGroups.Snapshot},
		{&snap.Peerings, m.peerings.Snapshot},
		{&snap.NATGateways, m.natGateways.Snapshot},
		{&snap.FlowLogs, m.flowLogs.Snapshot},
		{&snap.RouteTables, m.routeTables.Snapshot},
		{&snap.NetworkACLs, m.networkACLs.Snapshot},
		{&snap.ACLAssocs, m.aclAssocs.Snapshot},
		{&snap.IGWs, m.igws.Snapshot},
		{&snap.EIPs, m.eips.Snapshot},
		{&snap.RTAssocs, m.rtAssocs.Snapshot},
		{&snap.Endpoints, m.endpoints.Snapshot},
		{&snap.TransitGateways, m.transitGateways.Snapshot},
		{&snap.TGWAttachments, m.tgwAttachments.Snapshot},
		{&snap.TGWRouteTables, m.tgwRouteTables.Snapshot},
		{&snap.TGWRoutes, m.tgwRoutes.Snapshot},
		{&snap.TGWAssociations, m.tgwAssociations.Snapshot},
		{&snap.CustomerGateways, m.customerGateways.Snapshot},
		{&snap.VPNGateways, m.vpnGateways.Snapshot},
		{&snap.VPNConnections, m.vpnConnections.Snapshot},
		{&snap.DHCPOptions, m.dhcpOptions.Snapshot},
		{&snap.PrefixLists, m.prefixLists.Snapshot},
		{&snap.EgressOnlyIGWs, m.egressOnlyIGWs.Snapshot},
		{&snap.EndpointServices, m.endpointServices.Snapshot},
		{&snap.ClientVPNEndpoints, m.clientVPNEndpoints.Snapshot},
		{&snap.ClientVPNAssocs, m.clientVPNAssocs.Snapshot},
		{&snap.ClientVPNAuthRules, m.clientVPNAuthRules.Snapshot},
		{&snap.ClientVPNRoutes, m.clientVPNRoutes.Snapshot},
		{&snap.IPAMs, m.ipams.Snapshot},
		{&snap.IPAMScopes, m.ipamScopes.Snapshot},
		{&snap.IPAMPools, m.ipamPools.Snapshot},
		{&snap.IPAMPoolCidrs, m.ipamPoolCidrs.Snapshot},
		{&snap.IPAMAllocations, m.ipamAllocations.Snapshot},
		{&snap.IPAMDiscoveries, m.ipamDiscoveries.Snapshot},
		{&snap.IPAMRDAssociations, m.ipamRDAssociations.Snapshot},
		{&snap.IPAMByoasns, m.ipamByoasns.Snapshot},
		{&snap.IPAMByoipCidrs, m.ipamByoipCidrs.Snapshot},
		{&snap.IPAMResolvers, m.ipamResolvers.Snapshot},
		{&snap.IPAMResolverTargets, m.ipamResolverTargets.Snapshot},
		{&snap.IPAMTokens, m.ipamTokens.Snapshot},
		{&snap.IPAMPolicies, m.ipamPolicies.Snapshot},
		{&snap.TrafficMirrorTargets, m.trafficMirrorTargets.Snapshot},
		{&snap.TrafficMirrorFilters, m.trafficMirrorFilters.Snapshot},
		{&snap.TrafficMirrorSessions, m.trafficMirrorSessions.Snapshot},
		{&snap.NetworkInsightsPaths, m.networkInsightsPaths.Snapshot},
		{&snap.NetworkInsightsAnalyses, m.networkInsightsAnalyses.Snapshot},
		{&snap.NetworkInsightsAccessScopes, m.networkInsightsAccessScopes.Snapshot},
		{&snap.NetworkInsightsAccessScopeAnalyses, m.networkInsightsAccessScopeAnalyses.Snapshot},
		{&snap.VPCBPAExclusions, m.vpcBPAExclusions.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("vpc: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// snapshotScalarState captures the mu-guarded scalar and map state.
func (m *Mock) snapshotScalarState(snap *vpcSnapshot) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap.VPCBPAOptions = m.vpcBPAOptions
	snap.EndpointServicePerms = m.endpointServicePerms
	snap.IPAMPoolByCidr = m.ipamPoolByCidr
	snap.IPAMPoolByAllocation = m.ipamPoolByAllocation
	snap.ENIIPCounters = m.eniIPCounters

	if len(m.ipamResourceOverrides) > 0 {
		overrides := make(map[string]ipamResourceOverrideSnapshot, len(m.ipamResourceOverrides))
		for id, o := range m.ipamResourceOverrides {
			overrides[id] = ipamResourceOverrideSnapshot{ScopeID: o.scopeID, Unmanaged: o.unmanaged}
		}

		snap.IPAMResourceOverrides = overrides
	}
}

// Restore rebuilds the mock's state under the original identities: every
// resource id (and the id-string cross-references EC2 instances hold — SubnetID,
// VPCID, SecurityGroups) is preserved, so a restored instance's networking refs
// still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap vpcSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("vpc: parse snapshot: %w", err)
	}

	m.restoreENIs(snap.ENIs)

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.restoreScalarState(&snap)

	return nil
}

// restoreENIs reinstates each ENI under its original id.
func (m *Mock) restoreENIs(enis map[string]*eniSnapshot) {
	for id, s := range enis {
		m.enis.Set(id, &eniData{
			ID: s.ID, VPCID: s.VPCID, SubnetID: s.SubnetID, Status: s.Status,
			AttachmentID: s.AttachmentID, InstanceID: s.InstanceID, DeviceIndex: s.DeviceIndex,
			Description: s.Description, PrivateIP: s.PrivateIP, MacAddress: s.MacAddress,
			SourceDestCheck: s.SourceDestCheck, SecurityGroups: s.SecurityGroups, Tags: s.Tags,
			deleteOnTermination: s.DeleteOnTermination,
		})
	}
}

func (m *Mock) restoreStores(snap *vpcSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.VPCs, m.vpcs.LoadSnapshot},
		{snap.Subnets, m.subnets.LoadSnapshot},
		{snap.SecurityGroups, m.securityGroups.LoadSnapshot},
		{snap.Peerings, m.peerings.LoadSnapshot},
		{snap.NATGateways, m.natGateways.LoadSnapshot},
		{snap.FlowLogs, m.flowLogs.LoadSnapshot},
		{snap.RouteTables, m.routeTables.LoadSnapshot},
		{snap.NetworkACLs, m.networkACLs.LoadSnapshot},
		{snap.ACLAssocs, m.aclAssocs.LoadSnapshot},
		{snap.IGWs, m.igws.LoadSnapshot},
		{snap.EIPs, m.eips.LoadSnapshot},
		{snap.RTAssocs, m.rtAssocs.LoadSnapshot},
		{snap.Endpoints, m.endpoints.LoadSnapshot},
		{snap.TransitGateways, m.transitGateways.LoadSnapshot},
		{snap.TGWAttachments, m.tgwAttachments.LoadSnapshot},
		{snap.TGWRouteTables, m.tgwRouteTables.LoadSnapshot},
		{snap.TGWRoutes, m.tgwRoutes.LoadSnapshot},
		{snap.TGWAssociations, m.tgwAssociations.LoadSnapshot},
		{snap.CustomerGateways, m.customerGateways.LoadSnapshot},
		{snap.VPNGateways, m.vpnGateways.LoadSnapshot},
		{snap.VPNConnections, m.vpnConnections.LoadSnapshot},
		{snap.DHCPOptions, m.dhcpOptions.LoadSnapshot},
		{snap.PrefixLists, m.prefixLists.LoadSnapshot},
		{snap.EgressOnlyIGWs, m.egressOnlyIGWs.LoadSnapshot},
		{snap.EndpointServices, m.endpointServices.LoadSnapshot},
		{snap.ClientVPNEndpoints, m.clientVPNEndpoints.LoadSnapshot},
		{snap.ClientVPNAssocs, m.clientVPNAssocs.LoadSnapshot},
		{snap.ClientVPNAuthRules, m.clientVPNAuthRules.LoadSnapshot},
		{snap.ClientVPNRoutes, m.clientVPNRoutes.LoadSnapshot},
		{snap.IPAMs, m.ipams.LoadSnapshot},
		{snap.IPAMScopes, m.ipamScopes.LoadSnapshot},
		{snap.IPAMPools, m.ipamPools.LoadSnapshot},
		{snap.IPAMPoolCidrs, m.ipamPoolCidrs.LoadSnapshot},
		{snap.IPAMAllocations, m.ipamAllocations.LoadSnapshot},
		{snap.IPAMDiscoveries, m.ipamDiscoveries.LoadSnapshot},
		{snap.IPAMRDAssociations, m.ipamRDAssociations.LoadSnapshot},
		{snap.IPAMByoasns, m.ipamByoasns.LoadSnapshot},
		{snap.IPAMByoipCidrs, m.ipamByoipCidrs.LoadSnapshot},
		{snap.IPAMResolvers, m.ipamResolvers.LoadSnapshot},
		{snap.IPAMResolverTargets, m.ipamResolverTargets.LoadSnapshot},
		{snap.IPAMTokens, m.ipamTokens.LoadSnapshot},
		{snap.IPAMPolicies, m.ipamPolicies.LoadSnapshot},
		{snap.TrafficMirrorTargets, m.trafficMirrorTargets.LoadSnapshot},
		{snap.TrafficMirrorFilters, m.trafficMirrorFilters.LoadSnapshot},
		{snap.TrafficMirrorSessions, m.trafficMirrorSessions.LoadSnapshot},
		{snap.NetworkInsightsPaths, m.networkInsightsPaths.LoadSnapshot},
		{snap.NetworkInsightsAnalyses, m.networkInsightsAnalyses.LoadSnapshot},
		{snap.NetworkInsightsAccessScopes, m.networkInsightsAccessScopes.LoadSnapshot},
		{snap.NetworkInsightsAccessScopeAnalyses, m.networkInsightsAccessScopeAnalyses.LoadSnapshot},
		{snap.VPCBPAExclusions, m.vpcBPAExclusions.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("vpc: restore store: %w", err)
		}
	}

	return nil
}

// restoreScalarState reinstates the mu-guarded scalar/map fields, leaving unset
// ones at their New() defaults.
func (m *Mock) restoreScalarState(snap *vpcSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if snap.VPCBPAOptions != nil {
		m.vpcBPAOptions = snap.VPCBPAOptions
	}

	if snap.EndpointServicePerms != nil {
		m.endpointServicePerms = snap.EndpointServicePerms
	}

	if snap.IPAMPoolByCidr != nil {
		m.ipamPoolByCidr = snap.IPAMPoolByCidr
	}

	if snap.IPAMPoolByAllocation != nil {
		m.ipamPoolByAllocation = snap.IPAMPoolByAllocation
	}

	if snap.ENIIPCounters != nil {
		m.eniIPCounters = snap.ENIIPCounters
	}

	if snap.IPAMResourceOverrides != nil {
		overrides := make(map[string]ipamResourceOverride, len(snap.IPAMResourceOverrides))
		for id, o := range snap.IPAMResourceOverrides {
			overrides[id] = ipamResourceOverride{scopeID: o.ScopeID, unmanaged: o.Unmanaged}
		}

		m.ipamResourceOverrides = overrides
	}
}
