// Package vpc provides an in-memory mock implementation of AWS VPC networking.
package vpc

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Time format and mock constants.
const (
	timeFormat                = time.RFC3339
	maxOctetValue             = 256
	defaultFlowLogRecordLimit = 10
)

// Compile-time checks. The optional capabilities are asserted too: without
// this a signature drifting out of shape would silently stop satisfying the
// interface and every call would answer InvalidAction at runtime instead of
// failing the build.
var (
	_ driver.Networking                 = (*Mock)(nil)
	_ driver.NetworkInterfaces          = (*Mock)(nil)
	_ driver.VPCAttributes              = (*Mock)(nil)
	_ driver.TransitGateways            = (*Mock)(nil)
	_ driver.VPNConnections             = (*Mock)(nil)
	_ driver.DHCPOptionSets             = (*Mock)(nil)
	_ driver.PrefixLists                = (*Mock)(nil)
	_ driver.EgressOnlyInternetGateways = (*Mock)(nil)
	_ driver.VPCEndpointServices        = (*Mock)(nil)
	_ driver.ClientVPN                  = (*Mock)(nil)
	_ driver.IPAM                       = (*Mock)(nil)
	_ driver.IPAMResources              = (*Mock)(nil)
	_ driver.IPAMDiscovery              = (*Mock)(nil)
	_ driver.IPAMByoasn                 = (*Mock)(nil)
	_ driver.IPAMByoip                  = (*Mock)(nil)
	_ driver.IPAMPrefixListResolver     = (*Mock)(nil)
	_ driver.IPAMExternalToken          = (*Mock)(nil)
	_ driver.IPAMPolicy                 = (*Mock)(nil)
	_ driver.IPAMMetrics                = (*Mock)(nil)
	_ driver.TrafficMirroring           = (*Mock)(nil)
	_ driver.NetworkInsights            = (*Mock)(nil)
	_ driver.VPCBlockPublicAccess       = (*Mock)(nil)
)

// Mock is an in-memory mock implementation of the AWS VPC networking service.
type Mock struct {
	// mu guards the *fields* of stored records, not the maps holding them.
	// memstore copies its map on All(), but the values are pointers, so a
	// mutation through one handle races every concurrent read of the same
	// record. The stores handle their own map-level locking; this covers the
	// read-modify-write the callers do on top.
	mu sync.RWMutex

	vpcs           *memstore.Store[*vpcData]
	subnets        *memstore.Store[*subnetData]
	securityGroups *memstore.Store[*sgData]
	peerings       *memstore.Store[*peeringData]
	natGateways    *memstore.Store[*natGatewayData]
	flowLogs       *memstore.Store[*flowLogData]
	routeTables    *memstore.Store[*routeTableData]
	networkACLs    *memstore.Store[*networkACLData]
	igws           *memstore.Store[*igwData]
	eips           *memstore.Store[*eipData]
	rtAssocs       *memstore.Store[*rtAssocData]
	enis           *memstore.Store[*eniData]
	endpoints      *memstore.Store[*driver.VPCEndpoint]

	// AWS-specific networking capabilities (optional interfaces).
	transitGateways    *memstore.Store[*driver.TransitGateway]
	tgwAttachments     *memstore.Store[*driver.TransitGatewayVPCAttachment]
	tgwRouteTables     *memstore.Store[*driver.TransitGatewayRouteTable]
	tgwRoutes          *memstore.Store[*driver.TransitGatewayRoute]
	tgwAssociations    *memstore.Store[*driver.TransitGatewayRouteTableAssociation]
	customerGateways   *memstore.Store[*driver.CustomerGateway]
	vpnGateways        *memstore.Store[*driver.VPNGateway]
	vpnConnections     *memstore.Store[*driver.VPNConnection]
	dhcpOptions        *memstore.Store[*driver.DHCPOptions]
	prefixLists        *memstore.Store[*driver.PrefixList]
	egressOnlyIGWs     *memstore.Store[*driver.EgressOnlyInternetGateway]
	endpointServices   *memstore.Store[*driver.EndpointService]
	clientVPNEndpoints *memstore.Store[*driver.ClientVPNEndpoint]
	clientVPNAssocs    *memstore.Store[*driver.ClientVPNTargetNetwork]
	clientVPNAuthRules *memstore.Store[*driver.ClientVPNAuthorizationRule]
	clientVPNRoutes    *memstore.Store[*driver.ClientVPNRoute]

	ipams               *memstore.Store[*driver.Ipam]
	ipamScopes          *memstore.Store[*driver.IpamScope]
	ipamPools           *memstore.Store[*driver.IpamPool]
	ipamPoolCidrs       *memstore.Store[*driver.IpamPoolCidr]
	ipamAllocations     *memstore.Store[*driver.IpamPoolAllocation]
	ipamDiscoveries     *memstore.Store[*driver.IpamResourceDiscovery]
	ipamRDAssociations  *memstore.Store[*driver.IpamResourceDiscoveryAssociation]
	ipamByoasns         *memstore.Store[*driver.Byoasn]
	ipamByoipCidrs      *memstore.Store[*driver.ByoipCidr]
	ipamResolvers       *memstore.Store[*driver.IpamPrefixListResolver]
	ipamResolverTargets *memstore.Store[*driver.IpamPrefixListResolverTarget]
	ipamTokens          *memstore.Store[*driver.IpamExternalResourceVerificationToken]
	ipamPolicies        *memstore.Store[*driver.IpamPolicy]

	// Stage B EC2-family capabilities (optional interfaces).
	trafficMirrorTargets               *memstore.Store[*driver.TrafficMirrorTarget]
	trafficMirrorFilters               *memstore.Store[*driver.TrafficMirrorFilter]
	trafficMirrorSessions              *memstore.Store[*driver.TrafficMirrorSession]
	networkInsightsPaths               *memstore.Store[*driver.NetworkInsightsPath]
	networkInsightsAnalyses            *memstore.Store[*driver.NetworkInsightsAnalysis]
	networkInsightsAccessScopes        *memstore.Store[*driver.NetworkInsightsAccessScope]
	networkInsightsAccessScopeAnalyses *memstore.Store[*driver.NetworkInsightsAccessScopeAnalysis]
	vpcBPAExclusions                   *memstore.Store[*driver.VPCBlockPublicAccessExclusion]

	// vpcBPAOptions is the account/region-level Block Public Access singleton,
	// nil until first modified. Guarded by mu.
	vpcBPAOptions *driver.VPCBlockPublicAccessOptions

	// endpointServicePerms holds allowed principals per endpoint-service id,
	// guarded by mu.
	endpointServicePerms map[string][]string

	// ipamPoolByCidr / ipamPoolByAllocation map a provisioned CIDR id and an
	// allocation id to their owning pool id, guarded by mu.
	ipamPoolByCidr       map[string]string
	ipamPoolByAllocation map[string]string

	// ipamResourceOverrides persists ModifyIpamResourceCidr scope/unmonitor
	// changes, keyed by resourceID, since the base resource-CIDR list is
	// re-derived from VPCs/subnets on every read. Guarded by mu.
	ipamResourceOverrides map[string]ipamResourceOverride

	opts *config.Options
}

type vpcData struct {
	ID                 string
	CIDRBlock          string
	State              string
	Tags               map[string]string
	EnableDNSSupport   bool
	EnableDNSHostnames bool
}

type subnetData struct {
	ID               string
	VPCID            string
	CIDRBlock        string
	AvailabilityZone string
	State            string
	Tags             map[string]string
}

type sgData struct {
	ID           string
	Name         string
	Description  string
	VPCID        string
	IngressRules []driver.SecurityRule
	EgressRules  []driver.SecurityRule
	Tags         map[string]string
}

// New creates a new VPC mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		vpcs:           memstore.New[*vpcData](),
		subnets:        memstore.New[*subnetData](),
		securityGroups: memstore.New[*sgData](),
		peerings:       memstore.New[*peeringData](),
		natGateways:    memstore.New[*natGatewayData](),
		flowLogs:       memstore.New[*flowLogData](),
		routeTables:    memstore.New[*routeTableData](),
		networkACLs:    memstore.New[*networkACLData](),
		igws:           memstore.New[*igwData](),
		eips:           memstore.New[*eipData](),
		rtAssocs:       memstore.New[*rtAssocData](),
		enis:           memstore.New[*eniData](),
		endpoints:      memstore.New[*driver.VPCEndpoint](),

		transitGateways:    memstore.New[*driver.TransitGateway](),
		tgwAttachments:     memstore.New[*driver.TransitGatewayVPCAttachment](),
		tgwRouteTables:     memstore.New[*driver.TransitGatewayRouteTable](),
		tgwRoutes:          memstore.New[*driver.TransitGatewayRoute](),
		tgwAssociations:    memstore.New[*driver.TransitGatewayRouteTableAssociation](),
		customerGateways:   memstore.New[*driver.CustomerGateway](),
		vpnGateways:        memstore.New[*driver.VPNGateway](),
		vpnConnections:     memstore.New[*driver.VPNConnection](),
		dhcpOptions:        memstore.New[*driver.DHCPOptions](),
		prefixLists:        memstore.New[*driver.PrefixList](),
		egressOnlyIGWs:     memstore.New[*driver.EgressOnlyInternetGateway](),
		endpointServices:   memstore.New[*driver.EndpointService](),
		clientVPNEndpoints: memstore.New[*driver.ClientVPNEndpoint](),
		clientVPNAssocs:    memstore.New[*driver.ClientVPNTargetNetwork](),
		clientVPNAuthRules: memstore.New[*driver.ClientVPNAuthorizationRule](),
		clientVPNRoutes:    memstore.New[*driver.ClientVPNRoute](),

		ipams:               memstore.New[*driver.Ipam](),
		ipamScopes:          memstore.New[*driver.IpamScope](),
		ipamPools:           memstore.New[*driver.IpamPool](),
		ipamPoolCidrs:       memstore.New[*driver.IpamPoolCidr](),
		ipamAllocations:     memstore.New[*driver.IpamPoolAllocation](),
		ipamDiscoveries:     memstore.New[*driver.IpamResourceDiscovery](),
		ipamRDAssociations:  memstore.New[*driver.IpamResourceDiscoveryAssociation](),
		ipamByoasns:         memstore.New[*driver.Byoasn](),
		ipamByoipCidrs:      memstore.New[*driver.ByoipCidr](),
		ipamResolvers:       memstore.New[*driver.IpamPrefixListResolver](),
		ipamResolverTargets: memstore.New[*driver.IpamPrefixListResolverTarget](),
		ipamTokens:          memstore.New[*driver.IpamExternalResourceVerificationToken](),
		ipamPolicies:        memstore.New[*driver.IpamPolicy](),

		trafficMirrorTargets:               memstore.New[*driver.TrafficMirrorTarget](),
		trafficMirrorFilters:               memstore.New[*driver.TrafficMirrorFilter](),
		trafficMirrorSessions:              memstore.New[*driver.TrafficMirrorSession](),
		networkInsightsPaths:               memstore.New[*driver.NetworkInsightsPath](),
		networkInsightsAnalyses:            memstore.New[*driver.NetworkInsightsAnalysis](),
		networkInsightsAccessScopes:        memstore.New[*driver.NetworkInsightsAccessScope](),
		networkInsightsAccessScopeAnalyses: memstore.New[*driver.NetworkInsightsAccessScopeAnalysis](),
		vpcBPAExclusions:                   memstore.New[*driver.VPCBlockPublicAccessExclusion](),

		endpointServicePerms:  map[string][]string{},
		ipamPoolByCidr:        map[string]string{},
		ipamPoolByAllocation:  map[string]string{},
		ipamResourceOverrides: map[string]ipamResourceOverride{},

		opts: opts,
	}
}

// CreateVPC creates a new VPC with the given configuration.
func (m *Mock) CreateVPC(_ context.Context, cfg driver.VPCConfig) (*driver.VPCInfo, error) {
	if cfg.CIDRBlock == "" {
		return nil, errors.Newf(errors.InvalidArgument, "CIDR block is required")
	}

	id := idgen.GenerateID("vpc-")
	tags := copyTags(cfg.Tags)

	v := &vpcData{
		ID:        id,
		CIDRBlock: cfg.CIDRBlock,
		State:     "available",
		Tags:      tags,
		// EC2 defaults DNS support on and DNS hostnames off for a new VPC.
		EnableDNSSupport: true,
	}
	m.vpcs.Set(id, v)

	m.createMainRouteTable(id, cfg.CIDRBlock)

	m.mu.RLock()
	info := toVPCInfo(v)
	m.mu.RUnlock()

	return &info, nil
}

// createMainRouteTable gives the new VPC the route table EC2 creates for it,
// carrying the local route and an implicit main association. Callers list a
// VPC's route tables during teardown and skip the main one, so its absence is
// visible: it makes every VPC look like it has one fewer table than it does.
func (m *Mock) createMainRouteTable(vpcID, cidr string) {
	rtID := idgen.GenerateID("rtb-")

	m.routeTables.Set(rtID, &routeTableData{
		ID:    rtID,
		VPCID: vpcID,
		Routes: []driver.Route{{
			DestinationCIDR: cidr,
			TargetID:        RouteTargetLocal,
			TargetType:      RouteTargetLocal,
			State:           "active",
		}},
		IsMain: true,
	})

	assocID := idgen.GenerateID("rtbassoc-")
	m.rtAssocs.Set(assocID, &rtAssocData{
		ID:           assocID,
		RouteTableID: rtID,
		Main:         true,
	})
}

// DeleteVPC deletes the VPC with the given ID.
//
// The main route table and its association are implicit in the VPC, so they
// go with it. Leaving them behind would strand rows no caller can address:
// the main table refuses a direct delete.
func (m *Mock) DeleteVPC(_ context.Context, id string) error {
	if !m.vpcs.Has(id) {
		return errors.Newf(errors.NotFound, "vpc %q not found", id)
	}

	// An interface still attached inside the VPC blocks the delete, which is
	// what makes the drain a caller performs beforehand load-bearing rather
	// than ceremonial. Without this the emulator accepts a delete real EC2
	// refuses, and a caller whose drain is broken never finds out.
	if eni, blocked := m.attachedENIIn(id, ""); blocked {
		return errors.Newf(errors.FailedPrecondition,
			"DependencyViolation: network interface %q is still attached in vpc %q", eni, id)
	}

	m.vpcs.Delete(id)

	for rtID, rt := range m.routeTables.All() {
		if rt.VPCID != id || !rt.IsMain {
			continue
		}

		m.routeTables.Delete(rtID)

		for assocID, assoc := range m.rtAssocs.All() {
			if assoc.RouteTableID == rtID {
				m.rtAssocs.Delete(assocID)
			}
		}
	}

	return nil
}

// DescribeVPCs returns VPCs matching the given IDs, or all VPCs if ids is empty.
func (m *Mock) DescribeVPCs(_ context.Context, ids []string) ([]driver.VPCInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, id := range ids {
		if !m.vpcs.Has(id) {
			return nil, errors.Newf(errors.NotFound, "vpc %q not found", id)
		}
	}

	return describeResources(m.vpcs, ids, toVPCInfo), nil
}

// CreateSubnet creates a new subnet with the given configuration.
func (m *Mock) CreateSubnet(_ context.Context, cfg driver.SubnetConfig) (*driver.SubnetInfo, error) {
	if cfg.VPCID == "" {
		return nil, errors.Newf(errors.InvalidArgument, "VPC ID is required")
	}

	if cfg.CIDRBlock == "" {
		return nil, errors.Newf(errors.InvalidArgument, "CIDR block is required")
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return nil, errors.Newf(errors.NotFound, "vpc %q not found", cfg.VPCID)
	}

	id := idgen.GenerateID("subnet-")
	tags := copyTags(cfg.Tags)

	s := &subnetData{
		ID:               id,
		VPCID:            cfg.VPCID,
		CIDRBlock:        cfg.CIDRBlock,
		AvailabilityZone: cfg.AvailabilityZone,
		State:            "available",
		Tags:             tags,
	}
	m.subnets.Set(id, s)

	info := toSubnetInfo(s)

	return &info, nil
}

// DeleteSubnet deletes the subnet with the given ID.
func (m *Mock) DeleteSubnet(_ context.Context, id string) error {
	sub, ok := m.subnets.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "subnet %q not found", id)
	}

	// Same contract as DeleteVPC, one level down: a managed resource holding
	// an interface in this subnet keeps it alive.
	if eni, blocked := m.attachedENIIn(sub.VPCID, id); blocked {
		return errors.Newf(errors.FailedPrecondition,
			"DependencyViolation: network interface %q is still attached in subnet %q", eni, id)
	}

	m.subnets.Delete(id)

	return nil
}

// attachedENIIn reports an interface still attached within the given VPC, and
// within the given subnet when one is named. An interface that has been
// detached no longer blocks anything — that is the whole point of detaching it.
func (m *Mock) attachedENIIn(vpcID, subnetID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, eni := range m.enis.All() {
		if eni.VPCID != vpcID || eni.AttachmentID == "" {
			continue
		}

		if subnetID != "" && eni.SubnetID != subnetID {
			continue
		}

		return eni.ID, true
	}

	return "", false
}

// DescribeSubnets returns subnets matching the given IDs, or all subnets if ids is empty.
func (m *Mock) DescribeSubnets(_ context.Context, ids []string) ([]driver.SubnetInfo, error) {
	return describeResources(m.subnets, ids, toSubnetInfo), nil
}

// CreateSecurityGroup creates a new security group with the given configuration.
func (m *Mock) CreateSecurityGroup(_ context.Context, cfg driver.SecurityGroupConfig) (*driver.SecurityGroupInfo, error) {
	if cfg.Name == "" {
		return nil, errors.Newf(errors.InvalidArgument, "security group name is required")
	}

	if cfg.VPCID == "" {
		return nil, errors.Newf(errors.InvalidArgument, "VPC ID is required")
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return nil, errors.Newf(errors.NotFound, "vpc %q not found", cfg.VPCID)
	}

	id := idgen.GenerateID("sg-")
	tags := copyTags(cfg.Tags)

	sg := &sgData{
		ID:           id,
		Name:         cfg.Name,
		Description:  cfg.Description,
		VPCID:        cfg.VPCID,
		IngressRules: []driver.SecurityRule{},
		EgressRules:  []driver.SecurityRule{},
		Tags:         tags,
	}
	m.securityGroups.Set(id, sg)

	info := toSGInfo(sg)

	return &info, nil
}

// DeleteSecurityGroup deletes the security group with the given ID.
func (m *Mock) DeleteSecurityGroup(_ context.Context, id string) error {
	if !m.securityGroups.Delete(id) {
		return errors.Newf(errors.NotFound, "security group %q not found", id)
	}

	return nil
}

// DescribeSecurityGroups returns security groups matching the given IDs, or all if ids is empty.
func (m *Mock) DescribeSecurityGroups(_ context.Context, ids []string) ([]driver.SecurityGroupInfo, error) {
	for _, id := range ids {
		if !m.securityGroups.Has(id) {
			return nil, errors.Newf(errors.NotFound, "security group %q not found", id)
		}
	}

	return describeResources(m.securityGroups, ids, toSGInfo), nil
}

// describeResources is a generic helper for Describe* methods that list or filter by IDs.
func describeResources[T any, R any](store *memstore.Store[T], ids []string, toInfo func(T) R) []R {
	if len(ids) == 0 {
		// SortedValues (not All) so no-filter Describe* output is deterministic,
		// matching the repo's list-ordering contract.
		all := store.SortedValues()
		result := make([]R, 0, len(all))

		for _, item := range all {
			result = append(result, toInfo(item))
		}

		return result
	}

	result := make([]R, 0, len(ids))

	for _, id := range ids {
		item, ok := store.Get(id)
		if !ok {
			continue
		}

		result = append(result, toInfo(item))
	}

	return result
}

// AddIngressRule adds an ingress rule to the specified security group.
func (m *Mock) AddIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	sg.IngressRules = append(sg.IngressRules, rule)

	return nil
}

// AddEgressRule adds an egress rule to the specified security group.
func (m *Mock) AddEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	sg.EgressRules = append(sg.EgressRules, rule)

	return nil
}

// RemoveIngressRule removes a matching ingress rule from the specified security group.
func (m *Mock) RemoveIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	for i, r := range sg.IngressRules {
		if r == rule {
			sg.IngressRules = append(sg.IngressRules[:i], sg.IngressRules[i+1:]...)
			return nil
		}
	}

	return errors.Newf(errors.NotFound, "ingress rule not found in security group %q", groupID)
}

// RemoveEgressRule removes a matching egress rule from the specified security group.
func (m *Mock) RemoveEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	for i, r := range sg.EgressRules {
		if r == rule {
			sg.EgressRules = append(sg.EgressRules[:i], sg.EgressRules[i+1:]...)
			return nil
		}
	}

	return errors.Newf(errors.NotFound, "egress rule not found in security group %q", groupID)
}

// UpdateVPCTags merges the given tags into the VPC's tag map. Existing keys
// not present in tags are preserved; overlapping keys are overwritten.
// The mutation runs inside memstore.Update so the store's lock is held for
// the duration; the new map is built fresh and swapped in atomically so
// concurrent readers iterating the old map are unaffected.
func (m *Mock) UpdateVPCTags(_ context.Context, id string, tags map[string]string) error {
	if !m.vpcs.Update(id, func(v *vpcData) *vpcData {
		v.Tags = mergeTagMap(v.Tags, tags)
		return v
	}) {
		return errors.Newf(errors.NotFound, "VPC %q not found", id)
	}

	return nil
}

// RemoveVPCTags removes the given tag keys from a VPC. Unknown keys are ignored.
func (m *Mock) RemoveVPCTags(_ context.Context, id string, keys []string) error {
	if !m.vpcs.Update(id, func(v *vpcData) *vpcData {
		v.Tags = removeTagMapKeys(v.Tags, keys)
		return v
	}) {
		return errors.Newf(errors.NotFound, "VPC %q not found", id)
	}

	return nil
}

// UpdateSubnetTags merges tags into the subnet's tag map.
func (m *Mock) UpdateSubnetTags(_ context.Context, id string, tags map[string]string) error {
	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.Tags = mergeTagMap(s.Tags, tags)
		return s
	}) {
		return errors.Newf(errors.NotFound, "subnet %q not found", id)
	}

	return nil
}

// RemoveSubnetTags removes the given tag keys from a subnet.
func (m *Mock) RemoveSubnetTags(_ context.Context, id string, keys []string) error {
	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.Tags = removeTagMapKeys(s.Tags, keys)
		return s
	}) {
		return errors.Newf(errors.NotFound, "subnet %q not found", id)
	}

	return nil
}

// UpdateSecurityGroupTags merges tags into the security group's tag map.
func (m *Mock) UpdateSecurityGroupTags(_ context.Context, id string, tags map[string]string) error {
	if !m.securityGroups.Update(id, func(sg *sgData) *sgData {
		sg.Tags = mergeTagMap(sg.Tags, tags)
		return sg
	}) {
		return errors.Newf(errors.NotFound, "security group %q not found", id)
	}

	return nil
}

// RemoveSecurityGroupTags removes the given tag keys from a security group.
func (m *Mock) RemoveSecurityGroupTags(_ context.Context, id string, keys []string) error {
	if !m.securityGroups.Update(id, func(sg *sgData) *sgData {
		sg.Tags = removeTagMapKeys(sg.Tags, keys)
		return sg
	}) {
		return errors.Newf(errors.NotFound, "security group %q not found", id)
	}

	return nil
}

// mergeTagMap returns a fresh map containing existing's keys plus tags's
// keys (tags wins on overlap). The original existing map is not modified
// so concurrent readers can keep iterating it safely.
func mergeTagMap(existing, tags map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(tags))

	for k, v := range existing {
		out[k] = v
	}

	for k, v := range tags {
		out[k] = v
	}

	return out
}

// removeTagMapKeys returns a fresh map with the listed keys removed. The
// original map is not modified.
func removeTagMapKeys(existing map[string]string, keys []string) map[string]string {
	if len(existing) == 0 {
		return existing
	}

	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}

	out := make(map[string]string, len(existing))

	for k, v := range existing {
		if _, gone := drop[k]; gone {
			continue
		}

		out[k] = v
	}

	return out
}

// copyTags creates a shallow copy of a tags map.
func copyTags(tags map[string]string) map[string]string {
	if tags == nil {
		return make(map[string]string)
	}

	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}

	return out
}

func toVPCInfo(v *vpcData) driver.VPCInfo {
	return driver.VPCInfo{
		ID:                 v.ID,
		CIDRBlock:          v.CIDRBlock,
		State:              v.State,
		Tags:               copyTags(v.Tags),
		EnableDNSSupport:   v.EnableDNSSupport,
		EnableDNSHostnames: v.EnableDNSHostnames,
	}
}

func toSubnetInfo(s *subnetData) driver.SubnetInfo {
	return driver.SubnetInfo{
		ID:               s.ID,
		VPCID:            s.VPCID,
		CIDRBlock:        s.CIDRBlock,
		AvailabilityZone: s.AvailabilityZone,
		State:            s.State,
		Tags:             copyTags(s.Tags),
	}
}

func toSGInfo(sg *sgData) driver.SecurityGroupInfo {
	ingress := make([]driver.SecurityRule, len(sg.IngressRules))
	copy(ingress, sg.IngressRules)

	egress := make([]driver.SecurityRule, len(sg.EgressRules))
	copy(egress, sg.EgressRules)

	return driver.SecurityGroupInfo{
		ID:           sg.ID,
		Name:         sg.Name,
		Description:  sg.Description,
		VPCID:        sg.VPCID,
		IngressRules: ingress,
		EgressRules:  egress,
		Tags:         copyTags(sg.Tags),
	}
}
