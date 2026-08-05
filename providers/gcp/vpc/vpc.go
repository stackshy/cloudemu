// Package gcpvpc provides an in-memory mock implementation of Google Cloud VPC networking.
package gcpvpc

import (
	"context"
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

// Mock is an in-memory mock implementation of GCP VPC networking.
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
	opts           *config.Options
}

// New creates a new GCP VPC mock.
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
		endpoints:      memstore.New[*driver.VPCEndpoint](),
		opts:           opts,
	}
}

// CreateVPC creates a new VPC network.
func (m *Mock) CreateVPC(_ context.Context, cfg driver.VPCConfig) (*driver.VPCInfo, error) {
	if cfg.CIDRBlock == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "CIDR block is required")
	}

	id := idgen.GCPID(m.opts.ProjectID, "networks", idgen.GenerateID("vpc-"))
	tags := copyTags(cfg.Tags)

	v := &vpcData{
		ID:        id,
		CIDRBlock: cfg.CIDRBlock,
		State:     "READY",
		Tags:      tags,
	}
	m.vpcs.Set(id, v)

	info := toVPCInfo(v)

	return &info, nil
}

// DeleteVPC deletes the VPC network with the given ID.
func (m *Mock) DeleteVPC(_ context.Context, id string) error {
	if !m.vpcs.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "VPC %q not found", id)
	}

	return nil
}

// DescribeVPCs returns VPCs matching the given IDs, or all VPCs if ids is empty.
func (m *Mock) DescribeVPCs(_ context.Context, ids []string) ([]driver.VPCInfo, error) {
	return describeResources(m.vpcs, ids, toVPCInfo), nil
}

// CreateSubnet creates a new subnetwork.
func (m *Mock) CreateSubnet(_ context.Context, cfg driver.SubnetConfig) (*driver.SubnetInfo, error) {
	if cfg.VPCID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VPC ID is required")
	}

	if cfg.CIDRBlock == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "CIDR block is required")
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return nil, cerrors.Newf(cerrors.NotFound, "VPC %q not found", cfg.VPCID)
	}

	id := idgen.GCPID(m.opts.ProjectID, "subnetworks", idgen.GenerateID("subnet-"))
	tags := copyTags(cfg.Tags)

	s := &subnetData{
		ID:               id,
		VPCID:            cfg.VPCID,
		CIDRBlock:        cfg.CIDRBlock,
		AvailabilityZone: cfg.AvailabilityZone,
		State:            "READY",
		Tags:             tags,
	}
	m.subnets.Set(id, s)

	info := toSubnetInfo(s)

	return &info, nil
}

// DeleteSubnet deletes the subnetwork with the given ID.
func (m *Mock) DeleteSubnet(_ context.Context, id string) error {
	if !m.subnets.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "subnet %q not found", id)
	}

	return nil
}

// DescribeSubnets returns subnets matching the given IDs, or all subnets if ids is empty.
func (m *Mock) DescribeSubnets(_ context.Context, ids []string) ([]driver.SubnetInfo, error) {
	return describeResources(m.subnets, ids, toSubnetInfo), nil
}

// CreateSecurityGroup creates a new firewall rule group.
func (m *Mock) CreateSecurityGroup(_ context.Context, cfg driver.SecurityGroupConfig) (*driver.SecurityGroupInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "firewall rule name is required")
	}

	if cfg.VPCID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VPC ID is required")
	}

	if !m.vpcs.Has(cfg.VPCID) {
		return nil, cerrors.Newf(cerrors.NotFound, "VPC %q not found", cfg.VPCID)
	}

	id := idgen.GCPID(m.opts.ProjectID, "firewalls", idgen.GenerateID("fw-"))
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

// DeleteSecurityGroup deletes the firewall rule group with the given ID.
func (m *Mock) DeleteSecurityGroup(_ context.Context, id string) error {
	if !m.securityGroups.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", id)
	}

	return nil
}

// DescribeSecurityGroups returns firewall rule groups matching the given IDs, or all if ids is empty.
func (m *Mock) DescribeSecurityGroups(_ context.Context, ids []string) ([]driver.SecurityGroupInfo, error) {
	return describeResources(m.securityGroups, ids, toSGInfo), nil
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

// AddIngressRule adds an ingress rule to the specified firewall rule group.
func (m *Mock) AddIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", groupID)
	}

	sg.IngressRules = append(sg.IngressRules, rule)

	return nil
}

// AddEgressRule adds an egress rule to the specified firewall rule group.
func (m *Mock) AddEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", groupID)
	}

	sg.EgressRules = append(sg.EgressRules, rule)

	return nil
}

// RemoveIngressRule removes a matching ingress rule from the specified firewall rule group.
func (m *Mock) RemoveIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", groupID)
	}

	for i, r := range sg.IngressRules {
		if r == rule {
			sg.IngressRules = append(sg.IngressRules[:i], sg.IngressRules[i+1:]...)

			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "ingress rule not found in firewall rule %q", groupID)
}

// RemoveEgressRule removes a matching egress rule from the specified firewall rule group.
func (m *Mock) RemoveEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups.Get(groupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", groupID)
	}

	for i, r := range sg.EgressRules {
		if r == rule {
			sg.EgressRules = append(sg.EgressRules[:i], sg.EgressRules[i+1:]...)

			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "egress rule not found in firewall rule %q", groupID)
}

// UpdateVPCTags merges the given labels into the network's label map. GCP
// calls these labels; the cross-cloud Networking interface uses the term tags.
// The mutation runs under memstore.Update's lock; a fresh map is swapped in
// so concurrent readers iterating the old map are unaffected.
func (m *Mock) UpdateVPCTags(_ context.Context, id string, tags map[string]string) error {
	if !m.vpcs.Update(id, func(v *vpcData) *vpcData {
		v.Tags = mergeTagMap(v.Tags, tags)
		return v
	}) {
		return cerrors.Newf(cerrors.NotFound, "network %q not found", id)
	}

	return nil
}

// RemoveVPCTags removes the given label keys from a network.
func (m *Mock) RemoveVPCTags(_ context.Context, id string, keys []string) error {
	if !m.vpcs.Update(id, func(v *vpcData) *vpcData {
		v.Tags = removeTagMapKeys(v.Tags, keys)
		return v
	}) {
		return cerrors.Newf(cerrors.NotFound, "network %q not found", id)
	}

	return nil
}

// UpdateSubnetTags merges labels into the subnetwork's label map.
func (m *Mock) UpdateSubnetTags(_ context.Context, id string, tags map[string]string) error {
	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.Tags = mergeTagMap(s.Tags, tags)
		return s
	}) {
		return cerrors.Newf(cerrors.NotFound, "subnetwork %q not found", id)
	}

	return nil
}

// RemoveSubnetTags removes the given label keys from a subnetwork.
func (m *Mock) RemoveSubnetTags(_ context.Context, id string, keys []string) error {
	if !m.subnets.Update(id, func(s *subnetData) *subnetData {
		s.Tags = removeTagMapKeys(s.Tags, keys)
		return s
	}) {
		return cerrors.Newf(cerrors.NotFound, "subnetwork %q not found", id)
	}

	return nil
}

// UpdateSecurityGroupTags merges labels into the firewall rule's label map.
func (m *Mock) UpdateSecurityGroupTags(_ context.Context, id string, tags map[string]string) error {
	if !m.securityGroups.Update(id, func(sg *sgData) *sgData {
		sg.Tags = mergeTagMap(sg.Tags, tags)
		return sg
	}) {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", id)
	}

	return nil
}

// RemoveSecurityGroupTags removes the given label keys from a firewall rule.
func (m *Mock) RemoveSecurityGroupTags(_ context.Context, id string, keys []string) error {
	if !m.securityGroups.Update(id, func(sg *sgData) *sgData {
		sg.Tags = removeTagMapKeys(sg.Tags, keys)
		return sg
	}) {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", id)
	}

	return nil
}

// mergeTagMap returns a fresh map containing existing's keys plus tags's
// keys (tags wins on overlap). The original existing map is not modified.
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
