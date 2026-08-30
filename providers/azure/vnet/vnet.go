// Package vnet provides an in-memory mock implementation of Azure Virtual Network.
package vnet

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
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

// Compile-time check that Mock implements driver.Networking.
var _ driver.Networking = (*Mock)(nil)

type vpcData struct {
	ID        string
	CIDRBlock string
	State     string
	Tags      map[string]string
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

// Mock is an in-memory mock implementation of the Azure Virtual Network service.
type Mock struct {
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
	endpoints      *memstore.Store[*driver.VPCEndpoint]
	nics           *memstore.Store[*nicData]
	// azureVNetMeta / azureNSGMeta hold the ARM-specific fields the cross-cloud
	// VPC / SecurityGroup model cannot represent (region, full address-prefix
	// list, Azure security rules), keyed by the driver resource id.
	azureVNetMeta *memstore.Store[driver.AzureVNetMetadata]
	azureNSGMeta  *memstore.Store[driver.AzureNSGMetadata]
	// azureRouteTableMeta holds the ARM-specific route-table fields (region,
	// routes, user tags) the cross-cloud RouteTable model cannot represent, keyed
	// by the driver route table id.
	azureRouteTableMeta *memstore.Store[driver.AzureRouteTableMetadata]
	// azureVNetPeerings holds the ARM-specific virtualNetworkPeerings
	// sub-resources for each VNet, keyed by the VNet's driver id, separately
	// from azureVNetMeta so a whole-VNet PUT's PutAzureVNetMetadata (which
	// replaces the vnet's metadata wholesale) never clobbers peerings created
	// through the dedicated peerings sub-resource CRUD.
	azureVNetPeerings *memstore.Store[[]driver.AzureVNetPeering]
	// azureASGs holds the Azure-only application security groups (tag-like
	// groupings with no cross-cloud equivalent), keyed by (resourceGroup, name).
	azureASGs *memstore.Store[driver.AzureApplicationSecurityGroup]
	// nicMu serializes network-interface create/update, whose private-IP
	// allocation is a read-modify-write across the nics store (memstore is
	// per-op safe but can't make that sequence atomic).
	nicMu sync.RWMutex
	opts  *config.Options
}

// New creates a new Azure Virtual Network mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		vpcs:                memstore.New[*vpcData](),
		subnets:             memstore.New[*subnetData](),
		securityGroups:      memstore.New[*sgData](),
		peerings:            memstore.New[*peeringData](),
		natGateways:         memstore.New[*natGatewayData](),
		flowLogs:            memstore.New[*flowLogData](),
		routeTables:         memstore.New[*routeTableData](),
		networkACLs:         memstore.New[*networkACLData](),
		igws:                memstore.New[*igwData](),
		eips:                memstore.New[*eipData](),
		rtAssocs:            memstore.New[*rtAssocData](),
		endpoints:           memstore.New[*driver.VPCEndpoint](),
		nics:                memstore.New[*nicData](),
		azureVNetMeta:       memstore.New[driver.AzureVNetMetadata](),
		azureNSGMeta:        memstore.New[driver.AzureNSGMetadata](),
		azureRouteTableMeta: memstore.New[driver.AzureRouteTableMetadata](),
		azureVNetPeerings:   memstore.New[[]driver.AzureVNetPeering](),
		azureASGs:           memstore.New[driver.AzureApplicationSecurityGroup](),
		opts:                opts,
	}
}

// describeResources is a generic helper for Describe* methods that list or filter by IDs.
func describeResources[T any, R any](store *memstore.Store[T], ids []string, toInfo func(T) R) []R {
	if len(ids) == 0 {
		all := store.All()
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

// CreateVPC creates a new virtual network with the given configuration.
func (m *Mock) CreateVPC(_ context.Context, cfg driver.VPCConfig) (*driver.VPCInfo, error) {
	if cfg.CIDRBlock == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "CIDR block is required")
	}

	id := idgen.GenerateID("vnet-")

	tags := copyTags(cfg.Tags)

	v := &vpcData{
		ID:        id,
		CIDRBlock: cfg.CIDRBlock,
		State:     "available",
		Tags:      tags,
	}
	m.vpcs.Set(id, v)

	info := toVPCInfo(v)

	return &info, nil
}

// DeleteVPC deletes the virtual network with the given ID.
func (m *Mock) DeleteVPC(_ context.Context, id string) error {
	if !m.vpcs.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "vnet %q not found", id)
	}

	return nil
}

// DescribeVPCs returns virtual networks matching the given IDs, or all if ids is empty.
func (m *Mock) DescribeVPCs(_ context.Context, ids []string) ([]driver.VPCInfo, error) {
	return describeResources(m.vpcs, ids, toVPCInfo), nil
}

// CreateSubnet creates a new subnet within a virtual network.
func (m *Mock) CreateSubnet(_ context.Context, cfg driver.SubnetConfig) (*driver.SubnetInfo, error) {
	if cfg.VPCID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VNet ID is required")
	}

	if cfg.CIDRBlock == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "CIDR block is required")
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return nil, cerrors.Newf(cerrors.NotFound, "vnet %q not found", cfg.VPCID)
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
	if !m.subnets.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "subnet %q not found", id)
	}

	return nil
}

// DescribeSubnets returns subnets matching the given IDs, or all if ids is empty.
func (m *Mock) DescribeSubnets(_ context.Context, ids []string) ([]driver.SubnetInfo, error) {
	return describeResources(m.subnets, ids, toSubnetInfo), nil
}

// CreateSecurityGroup creates a new network security group.
func (m *Mock) CreateSecurityGroup(_ context.Context, cfg driver.SecurityGroupConfig) (*driver.SecurityGroupInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "security group name is required")
	}

	if cfg.VPCID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VNet ID is required")
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return nil, cerrors.Newf(cerrors.NotFound, "vnet %q not found", cfg.VPCID)
	}

	id := idgen.GenerateID("nsg-")

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

// DeleteSecurityGroup deletes the network security group with the given ID.
func (m *Mock) DeleteSecurityGroup(_ context.Context, id string) error {
	if !m.securityGroups.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", id)
	}

	return nil
}

// DescribeSecurityGroups returns security groups matching the given IDs, or all if ids is empty.
func (m *Mock) DescribeSecurityGroups(_ context.Context, ids []string) ([]driver.SecurityGroupInfo, error) {
	return describeResources(m.securityGroups, ids, toSGInfo), nil
}

// AddIngressRule adds an inbound security rule to the specified network security group.
//
//nolint:gocritic // hugeParam: rule is passed by value to satisfy the Networking driver interface.
func (m *Mock) AddIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", groupID)
	}

	sg.IngressRules = append(sg.IngressRules, rule)

	return nil
}

// AddEgressRule adds an outbound security rule to the specified network security group.
//
//nolint:gocritic // hugeParam: rule is passed by value to satisfy the Networking driver interface.
func (m *Mock) AddEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", groupID)
	}

	sg.EgressRules = append(sg.EgressRules, rule)

	return nil
}

// RemoveIngressRule removes a matching inbound rule from the specified network security group.
//
//nolint:gocritic // hugeParam: rule is passed by value to satisfy the Networking driver interface.
func (m *Mock) RemoveIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", groupID)
	}

	for i, r := range sg.IngressRules {
		if r.Equal(&rule) {
			sg.IngressRules = append(sg.IngressRules[:i], sg.IngressRules[i+1:]...)
			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "ingress rule not found in network security group %q", groupID)
}

// RemoveEgressRule removes a matching outbound rule from the specified network security group.
//
//nolint:gocritic // hugeParam: rule is passed by value to satisfy the Networking driver interface.
func (m *Mock) RemoveEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", groupID)
	}

	for i, r := range sg.EgressRules {
		if r.Equal(&rule) {
			sg.EgressRules = append(sg.EgressRules[:i], sg.EgressRules[i+1:]...)
			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "egress rule not found in network security group %q", groupID)
}

// UpdateVPCTags merges the given tags into the virtual network's tag map.
// The mutation runs under memstore.Update's lock; a fresh map is swapped in
// so concurrent readers iterating the old map are unaffected.
func (m *Mock) UpdateVPCTags(_ context.Context, id string, tags map[string]string) error {
	if !m.vpcs.Update(id, func(v *vpcData) *vpcData {
		v.Tags = mergeTagMap(v.Tags, tags)
		return v
	}) {
		return cerrors.Newf(cerrors.NotFound, "virtual network %q not found", id)
	}

	return nil
}

// RemoveVPCTags removes the given tag keys from a virtual network.
func (m *Mock) RemoveVPCTags(_ context.Context, id string, keys []string) error {
	if !m.vpcs.Update(id, func(v *vpcData) *vpcData {
		v.Tags = removeTagMapKeys(v.Tags, keys)
		return v
	}) {
		return cerrors.Newf(cerrors.NotFound, "virtual network %q not found", id)
	}

	return nil
}

// UpdateSubnetCIDR changes a subnet's address prefix in place, implementing the
// SubnetCIDRUpdater capability (the ARM Subnets.CreateOrUpdate re-PUT path).
func (m *Mock) UpdateSubnetCIDR(_ context.Context, id, cidr string) error {
	if cidr == "" {
		return cerrors.New(cerrors.InvalidArgument, "CIDR block is required")
	}

	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.CIDRBlock = cidr
		return s
	}) {
		return cerrors.Newf(cerrors.NotFound, "subnet %q not found", id)
	}

	return nil
}

// UpdateSubnetTags merges tags into the subnet's tag map.
func (m *Mock) UpdateSubnetTags(_ context.Context, id string, tags map[string]string) error {
	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.Tags = mergeTagMap(s.Tags, tags)
		return s
	}) {
		return cerrors.Newf(cerrors.NotFound, "subnet %q not found", id)
	}

	return nil
}

// RemoveSubnetTags removes the given tag keys from a subnet.
func (m *Mock) RemoveSubnetTags(_ context.Context, id string, keys []string) error {
	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.Tags = removeTagMapKeys(s.Tags, keys)
		return s
	}) {
		return cerrors.Newf(cerrors.NotFound, "subnet %q not found", id)
	}

	return nil
}

// UpdateSecurityGroupTags merges tags into the NSG's tag map.
func (m *Mock) UpdateSecurityGroupTags(_ context.Context, id string, tags map[string]string) error {
	if !m.securityGroups.Update(id, func(sg *sgData) *sgData {
		sg.Tags = mergeTagMap(sg.Tags, tags)
		return sg
	}) {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", id)
	}

	return nil
}

// RemoveSecurityGroupTags removes the given tag keys from an NSG.
func (m *Mock) RemoveSecurityGroupTags(_ context.Context, id string, keys []string) error {
	if !m.securityGroups.Update(id, func(sg *sgData) *sgData {
		sg.Tags = removeTagMapKeys(sg.Tags, keys)
		return sg
	}) {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", id)
	}

	return nil
}

// mergeTagMap returns a fresh map containing existing's keys plus tags's
// keys (tags wins on overlap). The original existing map is not modified
// so concurrent readers can keep iterating it safely.
func mergeTagMap(existing, tags map[string]string) map[string]string {
	// Size the hint from the existing map only; adding len(tags) risks an integer
	// overflow in the allocation size. The map grows to absorb tags as needed, so
	// the result is unchanged.
	out := make(map[string]string, len(existing))

	for k, v := range existing {
		out[k] = v
	}

	for k, v := range tags {
		out[k] = v
	}

	return out
}

// removeTagMapKeys returns a fresh map with the listed keys removed.
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
		ID:        v.ID,
		CIDRBlock: v.CIDRBlock,
		State:     v.State,
		Tags:      copyTags(v.Tags),
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
