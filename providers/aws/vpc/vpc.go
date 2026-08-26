// Package vpc provides an in-memory mock implementation of AWS VPC networking.
package vpc

import (
	"context"
	"net"
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

// VPC IPv4 CIDR netmask bounds. EC2 requires a VPC block between a /16 and a
// /28; anything outside that range is rejected with InvalidVpcRange.
const (
	minVPCNetmask = 16
	maxVPCNetmask = 28
	// vpcRangeErrPrefix marks the range error so the wire layer can emit the
	// resource-specific InvalidVpcRange code (mirrors the InvalidSubnet.* prefix
	// convention used for subnet CIDR conflicts).
	vpcRangeErrPrefix = "InvalidVpcRange: "
	// subnetRangeErrPrefix marks a subnet CIDR that falls outside the VPC's CIDR
	// block so the wire layer can emit the resource-specific InvalidSubnet.Range
	// code (mirrors the InvalidSubnet.Conflict prefix convention).
	subnetRangeErrPrefix = "InvalidSubnet.Range: "
)

// Default security group identity. EC2 gives every VPC a group named "default"
// with this exact description; users can edit its rules but not delete it.
const (
	defaultSGName        = "default"
	defaultSGDescription = "default VPC security group"
)

// VPC instance tenancy. Real EC2 CreateVpc accepts only "default" and
// "dedicated"; "host" is a valid instance placement tenancy but is rejected by
// CreateVpc, and any other value is an InvalidParameterValue.
const (
	tenancyDefault   = "default"
	tenancyDedicated = "dedicated"
)

// Compile-time checks. The optional capabilities are asserted too: without
// this a signature drifting out of shape would silently stop satisfying the
// interface and every call would answer InvalidAction at runtime instead of
// failing the build.
var (
	_ driver.Networking                 = (*Mock)(nil)
	_ driver.NetworkInterfaces          = (*Mock)(nil)
	_ driver.NetworkInterfaceModifier   = (*Mock)(nil)
	_ driver.NetworkACLAssociator       = (*Mock)(nil)
	_ driver.VPCAttributes              = (*Mock)(nil)
	_ driver.SubnetAttributes           = (*Mock)(nil)
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
	_ driver.NetworkResourceTagger      = (*Mock)(nil)
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
	aclAssocs      *memstore.Store[*aclAssocData]
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

	// eniIPCounters tracks the next private-IP offset handed out per subnet when
	// a standalone ENI is created, so each interface gets a distinct address.
	// Guarded by mu.
	eniIPCounters map[string]int

	opts *config.Options
}

type vpcData struct {
	ID                 string
	CIDRBlock          string
	State              string
	Tags               map[string]string
	EnableDNSSupport   bool
	EnableDNSHostnames bool
	DhcpOptionsID      string
	InstanceTenancy    string
}

type subnetData struct {
	ID                  string
	VPCID               string
	CIDRBlock           string
	AvailabilityZone    string
	State               string
	Tags                map[string]string
	MapPublicIPOnLaunch bool
}

type sgData struct {
	ID           string
	Name         string
	Description  string
	VPCID        string
	IngressRules []driver.SecurityRule
	EgressRules  []driver.SecurityRule
	Tags         map[string]string
	// IsDefault marks the group EC2 auto-creates with every VPC. It cannot be
	// deleted on its own (Client.CannotDelete) and disappears with the VPC.
	IsDefault bool
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
		aclAssocs:      memstore.New[*aclAssocData](),
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
		eniIPCounters:         map[string]int{},

		opts: opts,
	}
}

// CreateVPC creates a new VPC with the given configuration.
func (m *Mock) CreateVPC(_ context.Context, cfg driver.VPCConfig) (*driver.VPCInfo, error) {
	if cfg.CIDRBlock == "" {
		return nil, errors.Newf(errors.InvalidArgument, "CIDR block is required")
	}

	if err := validateVPCCIDR(cfg.CIDRBlock); err != nil {
		return nil, err
	}

	tenancy, err := validateInstanceTenancy(cfg.InstanceTenancy)
	if err != nil {
		return nil, err
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
		InstanceTenancy:  tenancy,
	}
	m.vpcs.Set(id, v)

	m.createMainRouteTable(id, cfg.CIDRBlock)
	m.createDefaultSecurityGroup(id)
	m.createDefaultNetworkACL(id)

	m.mu.RLock()
	info := toVPCInfo(v)
	m.mu.RUnlock()

	return &info, nil
}

// createDefaultSecurityGroup gives the new VPC the group EC2 auto-creates for
// it: name "default", an allow-all egress rule, and a self-referencing ingress
// rule that permits all traffic between members of the group. The group cannot
// be deleted directly and is removed when the VPC is deleted.
func (m *Mock) createDefaultSecurityGroup(vpcID string) {
	id := idgen.GenerateID("sg-")

	m.securityGroups.Set(id, &sgData{
		ID:          id,
		Name:        defaultSGName,
		Description: defaultSGDescription,
		VPCID:       vpcID,
		IsDefault:   true,
		IngressRules: []driver.SecurityRule{{
			Protocol:          allTrafficProtocol,
			ReferencedGroupID: id,
			RuleID:            idgen.GenerateID("sgr-"),
		}},
		EgressRules: []driver.SecurityRule{newDefaultEgressRule()},
		Tags:        map[string]string{},
	})
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
			"network interface %q is still attached in vpc %q", eni, id)
	}

	// Real EC2 refuses the delete while user-managed dependencies remain and
	// auto-removes the ones it created (main route table, default security
	// group). An active peering connection does not block — it is deleted with
	// the VPC — so it is deliberately absent from the dependency scan.
	if dep, blocked := m.vpcDependency(id); blocked {
		return errors.Newf(errors.FailedPrecondition,
			"the vpc %q has dependencies and cannot be deleted (%s)", id, dep)
	}

	m.vpcs.Delete(id)
	m.deleteMainRouteTable(id)
	m.deleteDefaultSecurityGroup(id)
	m.deleteDefaultNetworkACL(id)
	m.markVPCPeeringsDeleted(id)

	return nil
}

// deleteMainRouteTable removes the VPC's main route table and its association.
func (m *Mock) deleteMainRouteTable(vpcID string) {
	for rtID, rt := range m.routeTables.All() {
		if rt.VPCID != vpcID || !rt.IsMain {
			continue
		}

		m.routeTables.Delete(rtID)

		for assocID, assoc := range m.rtAssocs.All() {
			if assoc.RouteTableID == rtID {
				m.rtAssocs.Delete(assocID)
			}
		}
	}
}

// deleteDefaultSecurityGroup removes the group EC2 auto-created with the VPC.
func (m *Mock) deleteDefaultSecurityGroup(vpcID string) {
	for sgID, sg := range m.securityGroups.All() {
		if sg.VPCID == vpcID && sg.IsDefault {
			m.securityGroups.Delete(sgID)
		}
	}
}

// markVPCPeeringsDeleted transitions any peering that referenced the VPC to
// deleted, mirroring real EC2's Deleting -> Deleted cascade. Peering never
// blocks the delete, so this runs after the VPC is gone.
func (m *Mock) markVPCPeeringsDeleted(vpcID string) {
	for _, p := range m.peerings.All() {
		if p.RequesterVPC == vpcID || p.AccepterVPC == vpcID {
			p.Status = PeeringStatusDeleted
		}
	}
}

// vpcDependency reports the first user-managed resource that blocks deleting
// the VPC, in the order real EC2 surfaces them. The main route table, the
// default security group, and the default network ACL are excluded because EC2
// removes them with the VPC; active peering connections are excluded because
// they are deleted alongside it. Live NAT gateways, running instances, and
// interface endpoints are already caught by the attached-ENI check in DeleteVPC.
func (m *Mock) vpcDependency(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.subnets.All() {
		if s.VPCID == id {
			return "subnet " + s.ID, true
		}
	}

	for _, sg := range m.securityGroups.All() {
		if sg.VPCID == id && !sg.IsDefault {
			return "security group " + sg.ID, true
		}
	}

	if dep, blocked := m.vpcRoutingDependency(id); blocked {
		return dep, true
	}

	return m.vpcGatewayDependency(id)
}

// vpcRoutingDependency reports a blocking non-main route table or non-default
// network ACL in the VPC.
func (m *Mock) vpcRoutingDependency(id string) (string, bool) {
	for _, rt := range m.routeTables.All() {
		if rt.VPCID == id && !rt.IsMain {
			return "route table " + rt.ID, true
		}
	}

	for _, acl := range m.networkACLs.All() {
		if acl.VPCID == id && !acl.IsDefault {
			return "network ACL " + acl.ID, true
		}
	}

	return "", false
}

// vpcGatewayDependency reports a blocking attached internet gateway or VPC
// endpoint (gateway endpoints hold no ENI, so they need an explicit scan).
func (m *Mock) vpcGatewayDependency(id string) (string, bool) {
	for _, igw := range m.igws.All() {
		if igw.VpcID == id && igw.State == IGWStateAttached {
			return "internet gateway " + igw.ID, true
		}
	}

	for _, ep := range m.endpoints.All() {
		if ep.VPCID == id {
			return "vpc endpoint " + ep.ID, true
		}
	}

	return "", false
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

	v, ok := m.vpcs.Get(cfg.VPCID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "vpc %q not found", cfg.VPCID)
	}

	if conflict, err := m.subnetCIDRConflict(cfg.VPCID, cfg.CIDRBlock); err != nil {
		return nil, err
	} else if conflict != "" {
		return nil, errors.Newf(errors.AlreadyExists,
			"InvalidSubnet.Conflict: subnet CIDR %q conflicts with existing subnet %q", cfg.CIDRBlock, conflict)
	}

	// A subnet's CIDR must sit entirely inside the VPC's CIDR block; real EC2
	// rejects an out-of-range block with InvalidSubnet.Range.
	if !cidrWithinVPC(cfg.CIDRBlock, v.CIDRBlock) {
		return nil, errors.Newf(errors.InvalidArgument,
			"%sThe CIDR '%s' is invalid", subnetRangeErrPrefix, cfg.CIDRBlock)
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

	// A new subnet is automatically associated with its VPC's default network
	// ACL, matching real EC2 (this is the association ReplaceNetworkAclAssociation
	// later moves to a different ACL).
	m.associateDefaultNetworkACL(cfg.VPCID, id)

	info := toSubnetInfo(s)

	return &info, nil
}

// DeleteSubnet deletes the subnet with the given ID.
func (m *Mock) DeleteSubnet(_ context.Context, id string) error {
	if !m.subnets.Has(id) {
		return errors.Newf(errors.NotFound, "subnet %q not found", id)
	}

	// Real EC2 refuses to delete a subnet while ANY network interface still
	// resides in it — an unattached (available) ENI counts, not just an attached
	// one. Accepting the delete otherwise lets a broken drain pass unnoticed.
	if eni, blocked := m.eniInSubnet(id); blocked {
		return errors.Newf(errors.FailedPrecondition,
			"network interface %q still resides in subnet %q", eni, id)
	}

	m.subnets.Delete(id)

	for assocID, a := range m.aclAssocs.All() {
		if a.SubnetID == id {
			m.aclAssocs.Delete(assocID)
		}
	}

	return nil
}

// eniInSubnet reports the first network interface residing in the given subnet,
// whether attached or available. Real EC2 blocks DeleteSubnet on either.
func (m *Mock) eniInSubnet(subnetID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, eni := range m.enis.All() {
		if eni.SubnetID == subnetID {
			return eni.ID, true
		}
	}

	return "", false
}

// validateVPCCIDR checks a VPC's IPv4 CIDR the way EC2 does: a malformed value
// is an InvalidParameterValue, and a syntactically-valid block whose netmask
// falls outside /16../28 is an InvalidVpcRange. The range error carries the
// vpcRangeErrPrefix so the wire layer can emit the resource-specific code.
func validateVPCCIDR(cidr string) error {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return errors.Newf(errors.InvalidArgument, "invalid CIDR block %q", cidr)
	}

	ones, bits := ipnet.Mask.Size()
	if bits != net.IPv4len*8 {
		return errors.Newf(errors.InvalidArgument, "invalid CIDR block %q", cidr)
	}

	if ones < minVPCNetmask || ones > maxVPCNetmask {
		return errors.Newf(errors.InvalidArgument,
			"%sThe block range must be between a /28 netmask and /16 netmask", vpcRangeErrPrefix)
	}

	return nil
}

// validateInstanceTenancy normalizes and checks a VPC's requested instance
// tenancy. An empty value defaults to "default". Real EC2 CreateVpc accepts only
// "default" and "dedicated" — "host" and every other value are rejected with
// InvalidParameterValue (the wire layer maps this InvalidArgument to that code).
func validateInstanceTenancy(tenancy string) (string, error) {
	switch tenancy {
	case "":
		return tenancyDefault, nil
	case tenancyDefault, tenancyDedicated:
		return tenancy, nil
	default:
		return "", errors.Newf(errors.InvalidArgument,
			"invalid value %q for InstanceTenancy", tenancy)
	}
}

// cidrWithinVPC reports whether the subnet CIDR sits entirely inside the VPC's
// CIDR block: its network address must fall within the VPC block and its prefix
// must be at least as long (a smaller-or-equal block). An unparseable subnet CIDR
// is not contained; an unparseable VPC CIDR does not block (validation happens at
// VPC-create time).
func cidrWithinVPC(subnetCIDR, vpcCIDR string) bool {
	_, subnet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return false
	}

	_, vpcNet, err := net.ParseCIDR(vpcCIDR)
	if err != nil {
		return true
	}

	subnetOnes, _ := subnet.Mask.Size()
	vpcOnes, _ := vpcNet.Mask.Size()

	return subnetOnes >= vpcOnes && vpcNet.Contains(subnet.IP)
}

// subnetCIDRConflict reports an existing subnet in the same VPC whose CIDR
// overlaps the candidate, or an error if either CIDR is malformed. An empty id
// with a nil error means no conflict.
func (m *Mock) subnetCIDRConflict(vpcID, cidr string) (string, error) {
	_, candidate, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", errors.Newf(errors.InvalidArgument, "invalid CIDR block %q", cidr)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.subnets.All() {
		if s.VPCID != vpcID {
			continue
		}

		_, existing, perr := net.ParseCIDR(s.CIDRBlock)
		if perr != nil {
			continue
		}

		if existing.Contains(candidate.IP) || candidate.Contains(existing.IP) {
			return s.ID, nil
		}
	}

	return "", nil
}

// ModifySubnetAttribute changes one subnet launch attribute (the AWS-specific
// SubnetAttributes optional capability). A nil pointer leaves that attribute
// untouched, matching an API that accepts one attribute per call.
func (m *Mock) ModifySubnetAttribute(_ context.Context, id string, update driver.SubnetAttributeUpdate) error {
	if update.MapPublicIPOnLaunch == nil {
		return nil
	}

	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.MapPublicIPOnLaunch = *update.MapPublicIPOnLaunch
		return s
	}) {
		return errors.Newf(errors.NotFound, "subnet %q not found", id)
	}

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
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, id := range ids {
		if !m.subnets.Has(id) {
			return nil, errors.Newf(errors.NotFound, "subnet %q not found", id)
		}
	}

	return describeResources(m.subnets, ids, toSubnetInfo), nil
}

// CreateSecurityGroup creates a new security group with the given configuration.
// defaultEgressRule is the allow-all outbound rule real EC2 attaches to every
// new security group (protocol -1, 0.0.0.0/0, all ports).
//
// newDefaultEgressRule builds the allow-all outbound rule with a freshly minted
// "sgr-" id so each group's default egress rule is individually identifiable.
func newDefaultEgressRule() driver.SecurityRule {
	return driver.SecurityRule{
		Protocol: allTrafficProtocol,
		CIDR:     allTrafficCIDR,
		RuleID:   idgen.GenerateID("sgr-"),
	}
}

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

	// Security-group names are unique within a VPC. This invariant lives in the
	// provider so every run mode (wire server, portable API, typed Go API) shares
	// it; the wire layer only maps AlreadyExists to the InvalidGroup.Duplicate code.
	for _, existing := range m.securityGroups.SortedValues() {
		if existing.VPCID == cfg.VPCID && existing.Name == cfg.Name {
			return nil, errors.Newf(errors.AlreadyExists,
				"security group %q already exists in vpc %q", cfg.Name, cfg.VPCID)
		}
	}

	id := idgen.GenerateID("sg-")
	tags := copyTags(cfg.Tags)

	sg := &sgData{
		ID:           id,
		Name:         cfg.Name,
		Description:  cfg.Description,
		VPCID:        cfg.VPCID,
		IngressRules: []driver.SecurityRule{},
		// Real EC2 seeds every new security group with an allow-all egress rule
		// (no ingress). IaC tools rely on it — Terraform revokes this default
		// before applying the egress blocks in the config — and the topology
		// engine otherwise reports a fresh SG as denying all outbound traffic.
		// The rule gets its own "sgr-" id so DescribeSecurityGroupRules can
		// return and filter it the way real EC2 does.
		EgressRules: []driver.SecurityRule{newDefaultEgressRule()},
		Tags:        tags,
	}
	m.securityGroups.Set(id, sg)

	info := toSGInfo(sg)

	return &info, nil
}

// DeleteSecurityGroup deletes the security group with the given ID. The group
// EC2 auto-creates for a VPC cannot be deleted directly; real EC2 answers
// Client.CannotDelete, which the wire layer maps from this FailedPrecondition.
func (m *Mock) DeleteSecurityGroup(_ context.Context, id string) error {
	sg, ok := m.securityGroups.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", id)
	}

	if sg.IsDefault {
		return errors.Newf(errors.FailedPrecondition,
			"CannotDelete: default security group %q cannot be deleted", id)
	}

	// Real EC2 refuses to delete a security group that is still attached to a
	// network interface or referenced by another security group in the VPC.
	if dep, blocked := m.securityGroupInUse(id); blocked {
		return errors.Newf(errors.FailedPrecondition,
			"security group %q is in use by %s", id, dep)
	}

	m.securityGroups.Delete(id)

	return nil
}

// securityGroupInUse reports the first network interface using the group or the
// first other security group whose rules reference it.
func (m *Mock) securityGroupInUse(id string) (string, bool) {
	for _, eni := range m.enis.All() {
		for _, g := range eni.SecurityGroups {
			if g == id {
				return "network interface " + eni.ID, true
			}
		}
	}

	for _, other := range m.securityGroups.All() {
		if other.ID == id {
			continue
		}

		if sgReferencesGroup(other.IngressRules, id) || sgReferencesGroup(other.EgressRules, id) {
			return "security group " + other.ID, true
		}
	}

	return "", false
}

// sgReferencesGroup reports whether any rule references the group id.
func sgReferencesGroup(rules []driver.SecurityRule, id string) bool {
	for i := range rules {
		if rules[i].ReferencedGroupID == id {
			return true
		}
	}

	return false
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
//
//nolint:gocritic // hugeParam: rule is passed by value to satisfy the Networking driver interface.
func (m *Mock) AddIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	sg.IngressRules = append(sg.IngressRules, rule)

	return nil
}

// AddEgressRule adds an egress rule to the specified security group.
//
//nolint:gocritic // hugeParam: rule is passed by value to satisfy the Networking driver interface.
func (m *Mock) AddEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	sg.EgressRules = append(sg.EgressRules, rule)

	return nil
}

// RemoveIngressRule removes a matching ingress rule from the specified security group.
//
//nolint:gocritic // hugeParam: rule is passed by value to satisfy the Networking driver interface.
func (m *Mock) RemoveIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	for i := range sg.IngressRules {
		if sg.IngressRules[i].Matches(&rule) {
			sg.IngressRules = append(sg.IngressRules[:i], sg.IngressRules[i+1:]...)
			return nil
		}
	}

	return errors.Newf(errors.NotFound, "ingress rule not found in security group %q", groupID)
}

// RemoveEgressRule removes a matching egress rule from the specified security group.
//
//nolint:gocritic // hugeParam: rule is passed by value to satisfy the Networking driver interface.
func (m *Mock) RemoveEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return errors.Newf(errors.NotFound, "security group %q not found", groupID)
	}

	for i := range sg.EgressRules {
		if sg.EgressRules[i].Matches(&rule) {
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
		DhcpOptionsID:      v.DhcpOptionsID,
		InstanceTenancy:    v.InstanceTenancy,
	}
}

func toSubnetInfo(s *subnetData) driver.SubnetInfo {
	return driver.SubnetInfo{
		ID:                  s.ID,
		VPCID:               s.VPCID,
		CIDRBlock:           s.CIDRBlock,
		AvailabilityZone:    s.AvailabilityZone,
		State:               s.State,
		Tags:                copyTags(s.Tags),
		MapPublicIPOnLaunch: s.MapPublicIPOnLaunch,
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
