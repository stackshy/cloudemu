package networking

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements driver.Networking for testing
// the portable wrapper.
type mockDriver struct {
	vpcs           map[string]*driver.VPCInfo
	subnets        map[string]*driver.SubnetInfo
	securityGroups map[string]*driver.SecurityGroupInfo
	peerings       map[string]*driver.PeeringConnection
	natGateways    map[string]*driver.NATGateway
	flowLogs       map[string]*driver.FlowLog
	routeTables    map[string]*driver.RouteTable
	networkACLs    map[string]*driver.NetworkACL
	igws           map[string]*driver.InternetGateway
	eips           map[string]*driver.ElasticIP
	rtAssocs       map[string]*driver.RouteTableAssociation
	endpoints      map[string]*driver.VPCEndpoint
	seq            int
}

func newMockDriver() *mockDriver {
	return &mockDriver{
		vpcs:           make(map[string]*driver.VPCInfo),
		subnets:        make(map[string]*driver.SubnetInfo),
		securityGroups: make(map[string]*driver.SecurityGroupInfo),
		peerings:       make(map[string]*driver.PeeringConnection),
		natGateways:    make(map[string]*driver.NATGateway),
		flowLogs:       make(map[string]*driver.FlowLog),
		routeTables:    make(map[string]*driver.RouteTable),
		networkACLs:    make(map[string]*driver.NetworkACL),
		igws:           make(map[string]*driver.InternetGateway),
		eips:           make(map[string]*driver.ElasticIP),
		rtAssocs:       make(map[string]*driver.RouteTableAssociation),
		endpoints:      make(map[string]*driver.VPCEndpoint),
	}
}

func (m *mockDriver) nextID(prefix string) string {
	m.seq++

	return fmt.Sprintf("%s-%d", prefix, m.seq)
}

func (m *mockDriver) CreateVPC(_ context.Context, config driver.VPCConfig) (*driver.VPCInfo, error) {
	if config.CIDRBlock == "" {
		return nil, fmt.Errorf("cidr required")
	}

	id := m.nextID("vpc")
	info := &driver.VPCInfo{ID: id, CIDRBlock: config.CIDRBlock, State: "available", Tags: config.Tags}
	m.vpcs[id] = info

	return info, nil
}

func (m *mockDriver) DeleteVPC(_ context.Context, id string) error {
	if _, ok := m.vpcs[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.vpcs, id)

	return nil
}

func (m *mockDriver) DescribeVPCs(_ context.Context, ids []string) ([]driver.VPCInfo, error) {
	if len(ids) == 0 {
		result := make([]driver.VPCInfo, 0, len(m.vpcs))
		for _, v := range m.vpcs {
			result = append(result, *v)
		}

		return result, nil
	}

	var result []driver.VPCInfo

	for _, id := range ids {
		if v, ok := m.vpcs[id]; ok {
			result = append(result, *v)
		}
	}

	return result, nil
}

func (m *mockDriver) CreateSubnet(_ context.Context, config driver.SubnetConfig) (*driver.SubnetInfo, error) {
	if config.VPCID == "" {
		return nil, fmt.Errorf("vpc id required")
	}

	id := m.nextID("subnet")
	info := &driver.SubnetInfo{ID: id, VPCID: config.VPCID, CIDRBlock: config.CIDRBlock, State: "available"}
	m.subnets[id] = info

	return info, nil
}

func (m *mockDriver) DeleteSubnet(_ context.Context, id string) error {
	if _, ok := m.subnets[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.subnets, id)

	return nil
}

func (m *mockDriver) DescribeSubnets(_ context.Context, ids []string) ([]driver.SubnetInfo, error) {
	if len(ids) == 0 {
		result := make([]driver.SubnetInfo, 0, len(m.subnets))
		for _, s := range m.subnets {
			result = append(result, *s)
		}

		return result, nil
	}

	var result []driver.SubnetInfo

	for _, id := range ids {
		if s, ok := m.subnets[id]; ok {
			result = append(result, *s)
		}
	}

	return result, nil
}

func (m *mockDriver) CreateSecurityGroup(_ context.Context, config driver.SecurityGroupConfig) (*driver.SecurityGroupInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	id := m.nextID("sg")
	info := &driver.SecurityGroupInfo{ID: id, Name: config.Name, VPCID: config.VPCID}
	m.securityGroups[id] = info

	return info, nil
}

func (m *mockDriver) DeleteSecurityGroup(_ context.Context, id string) error {
	if _, ok := m.securityGroups[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.securityGroups, id)

	return nil
}

func (m *mockDriver) DescribeSecurityGroups(_ context.Context, ids []string) ([]driver.SecurityGroupInfo, error) {
	if len(ids) == 0 {
		result := make([]driver.SecurityGroupInfo, 0, len(m.securityGroups))
		for _, sg := range m.securityGroups {
			result = append(result, *sg)
		}

		return result, nil
	}

	var result []driver.SecurityGroupInfo

	for _, id := range ids {
		if sg, ok := m.securityGroups[id]; ok {
			result = append(result, *sg)
		}
	}

	return result, nil
}

func (m *mockDriver) AddIngressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups[groupID]
	if !ok {
		return fmt.Errorf("not found")
	}

	sg.IngressRules = append(sg.IngressRules, rule)

	return nil
}

func (m *mockDriver) AddEgressRule(_ context.Context, groupID string, rule driver.SecurityRule) error {
	sg, ok := m.securityGroups[groupID]
	if !ok {
		return fmt.Errorf("not found")
	}

	sg.EgressRules = append(sg.EgressRules, rule)

	return nil
}

func (m *mockDriver) RemoveIngressRule(_ context.Context, groupID string, _ driver.SecurityRule) error {
	if _, ok := m.securityGroups[groupID]; !ok {
		return fmt.Errorf("not found")
	}

	return nil
}

func (m *mockDriver) RemoveEgressRule(_ context.Context, groupID string, _ driver.SecurityRule) error {
	if _, ok := m.securityGroups[groupID]; !ok {
		return fmt.Errorf("not found")
	}

	return nil
}

func (m *mockDriver) CreatePeeringConnection(_ context.Context, config driver.PeeringConfig) (*driver.PeeringConnection, error) {
	id := m.nextID("pcx")
	pc := &driver.PeeringConnection{ID: id, RequesterVPC: config.RequesterVPC, AccepterVPC: config.AccepterVPC, Status: "pending-acceptance"}
	m.peerings[id] = pc

	return pc, nil
}

func (m *mockDriver) AcceptPeeringConnection(_ context.Context, peeringID string) error {
	pc, ok := m.peerings[peeringID]
	if !ok {
		return fmt.Errorf("not found")
	}

	pc.Status = "active"

	return nil
}

func (m *mockDriver) RejectPeeringConnection(_ context.Context, peeringID string) error {
	pc, ok := m.peerings[peeringID]
	if !ok {
		return fmt.Errorf("not found")
	}

	pc.Status = "rejected"

	return nil
}

func (m *mockDriver) DeletePeeringConnection(_ context.Context, peeringID string) error {
	if _, ok := m.peerings[peeringID]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.peerings, peeringID)

	return nil
}

func (m *mockDriver) DescribePeeringConnections(_ context.Context, ids []string) ([]driver.PeeringConnection, error) {
	if len(ids) == 0 {
		result := make([]driver.PeeringConnection, 0, len(m.peerings))
		for _, pc := range m.peerings {
			result = append(result, *pc)
		}

		return result, nil
	}

	var result []driver.PeeringConnection

	for _, id := range ids {
		if pc, ok := m.peerings[id]; ok {
			result = append(result, *pc)
		}
	}

	return result, nil
}

func (m *mockDriver) CreateNATGateway(_ context.Context, config driver.NATGatewayConfig) (*driver.NATGateway, error) {
	id := m.nextID("nat")
	nat := &driver.NATGateway{ID: id, SubnetID: config.SubnetID, State: "available"}
	m.natGateways[id] = nat

	return nat, nil
}

func (m *mockDriver) DeleteNATGateway(_ context.Context, id string) error {
	if _, ok := m.natGateways[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.natGateways, id)

	return nil
}

func (m *mockDriver) DescribeNATGateways(_ context.Context, ids []string) ([]driver.NATGateway, error) {
	if len(ids) == 0 {
		result := make([]driver.NATGateway, 0, len(m.natGateways))
		for _, nat := range m.natGateways {
			result = append(result, *nat)
		}

		return result, nil
	}

	var result []driver.NATGateway

	for _, id := range ids {
		if nat, ok := m.natGateways[id]; ok {
			result = append(result, *nat)
		}
	}

	return result, nil
}

func (m *mockDriver) CreateFlowLog(_ context.Context, config driver.FlowLogConfig) (*driver.FlowLog, error) {
	id := m.nextID("fl")
	fl := &driver.FlowLog{ID: id, ResourceID: config.ResourceID, ResourceType: config.ResourceType, TrafficType: config.TrafficType, Status: "ACTIVE"}
	m.flowLogs[id] = fl

	return fl, nil
}

func (m *mockDriver) DeleteFlowLog(_ context.Context, id string) error {
	if _, ok := m.flowLogs[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.flowLogs, id)

	return nil
}

func (m *mockDriver) DescribeFlowLogs(_ context.Context, ids []string) ([]driver.FlowLog, error) {
	if len(ids) == 0 {
		result := make([]driver.FlowLog, 0, len(m.flowLogs))
		for _, fl := range m.flowLogs {
			result = append(result, *fl)
		}

		return result, nil
	}

	var result []driver.FlowLog

	for _, id := range ids {
		if fl, ok := m.flowLogs[id]; ok {
			result = append(result, *fl)
		}
	}

	return result, nil
}

func (m *mockDriver) GetFlowLogRecords(_ context.Context, flowLogID string, _ int) ([]driver.FlowLogRecord, error) {
	if _, ok := m.flowLogs[flowLogID]; !ok {
		return nil, fmt.Errorf("not found")
	}

	return []driver.FlowLogRecord{}, nil
}

func (m *mockDriver) CreateRouteTable(_ context.Context, config driver.RouteTableConfig) (*driver.RouteTable, error) {
	id := m.nextID("rtb")
	rt := &driver.RouteTable{ID: id, VPCID: config.VPCID, Tags: config.Tags}
	m.routeTables[id] = rt

	return rt, nil
}

func (m *mockDriver) DeleteRouteTable(_ context.Context, id string) error {
	if _, ok := m.routeTables[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.routeTables, id)

	return nil
}

func (m *mockDriver) DescribeRouteTables(_ context.Context, ids []string) ([]driver.RouteTable, error) {
	if len(ids) == 0 {
		result := make([]driver.RouteTable, 0, len(m.routeTables))
		for _, rt := range m.routeTables {
			result = append(result, *rt)
		}

		return result, nil
	}

	var result []driver.RouteTable

	for _, id := range ids {
		if rt, ok := m.routeTables[id]; ok {
			result = append(result, *rt)
		}
	}

	return result, nil
}

func (m *mockDriver) CreateRoute(_ context.Context, routeTableID, _, _, _ string) error {
	if _, ok := m.routeTables[routeTableID]; !ok {
		return fmt.Errorf("not found")
	}

	return nil
}

func (m *mockDriver) DeleteRoute(_ context.Context, routeTableID, _ string) error {
	if _, ok := m.routeTables[routeTableID]; !ok {
		return fmt.Errorf("not found")
	}

	return nil
}

func (m *mockDriver) CreateNetworkACL(_ context.Context, vpcID string, tags map[string]string) (*driver.NetworkACL, error) {
	if _, ok := m.vpcs[vpcID]; !ok {
		return nil, fmt.Errorf("vpc not found")
	}

	id := m.nextID("acl")
	acl := &driver.NetworkACL{ID: id, VPCID: vpcID, Tags: tags}
	m.networkACLs[id] = acl

	return acl, nil
}

func (m *mockDriver) DeleteNetworkACL(_ context.Context, id string) error {
	if _, ok := m.networkACLs[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.networkACLs, id)

	return nil
}

func (m *mockDriver) DescribeNetworkACLs(_ context.Context, ids []string) ([]driver.NetworkACL, error) {
	if len(ids) == 0 {
		result := make([]driver.NetworkACL, 0, len(m.networkACLs))
		for _, acl := range m.networkACLs {
			result = append(result, *acl)
		}

		return result, nil
	}

	var result []driver.NetworkACL

	for _, id := range ids {
		if acl, ok := m.networkACLs[id]; ok {
			result = append(result, *acl)
		}
	}

	return result, nil
}

func (m *mockDriver) AddNetworkACLRule(_ context.Context, aclID string, _ *driver.NetworkACLRule) error {
	if _, ok := m.networkACLs[aclID]; !ok {
		return fmt.Errorf("not found")
	}

	return nil
}

func (m *mockDriver) RemoveNetworkACLRule(_ context.Context, aclID string, _ int, _ bool) error {
	if _, ok := m.networkACLs[aclID]; !ok {
		return fmt.Errorf("not found")
	}

	return nil
}

func (m *mockDriver) CreateInternetGateway(
	_ context.Context, cfg driver.InternetGatewayConfig,
) (*driver.InternetGateway, error) {
	id := m.nextID("igw")
	igw := &driver.InternetGateway{
		ID: id, State: "detached", Tags: cfg.Tags,
	}
	m.igws[id] = igw

	return igw, nil
}

func (m *mockDriver) DeleteInternetGateway(
	_ context.Context, id string,
) error {
	if _, ok := m.igws[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.igws, id)

	return nil
}

func (m *mockDriver) DescribeInternetGateways(
	_ context.Context, ids []string,
) ([]driver.InternetGateway, error) {
	if len(ids) == 0 {
		result := make([]driver.InternetGateway, 0, len(m.igws))
		for _, igw := range m.igws {
			result = append(result, *igw)
		}

		return result, nil
	}

	var result []driver.InternetGateway

	for _, id := range ids {
		if igw, ok := m.igws[id]; ok {
			result = append(result, *igw)
		}
	}

	return result, nil
}

func (m *mockDriver) AttachInternetGateway(
	_ context.Context, igwID, vpcID string,
) error {
	igw, ok := m.igws[igwID]
	if !ok {
		return fmt.Errorf("not found")
	}

	igw.VpcID = vpcID
	igw.State = "attached"

	return nil
}

func (m *mockDriver) DetachInternetGateway(
	_ context.Context, igwID, _ string,
) error {
	igw, ok := m.igws[igwID]
	if !ok {
		return fmt.Errorf("not found")
	}

	igw.VpcID = ""
	igw.State = "detached"

	return nil
}

func (m *mockDriver) AllocateAddress(
	_ context.Context, cfg driver.ElasticIPConfig,
) (*driver.ElasticIP, error) {
	id := m.nextID("eipalloc")
	eip := &driver.ElasticIP{
		AllocationID: id,
		PublicIP:     "10.0.0.1",
		Tags:         cfg.Tags,
	}
	m.eips[id] = eip

	return eip, nil
}

func (m *mockDriver) ReleaseAddress(
	_ context.Context, allocationID string,
) error {
	if _, ok := m.eips[allocationID]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.eips, allocationID)

	return nil
}

func (m *mockDriver) DescribeAddresses(
	_ context.Context, ids []string,
) ([]driver.ElasticIP, error) {
	if len(ids) == 0 {
		result := make([]driver.ElasticIP, 0, len(m.eips))
		for _, eip := range m.eips {
			result = append(result, *eip)
		}

		return result, nil
	}

	var result []driver.ElasticIP

	for _, id := range ids {
		if eip, ok := m.eips[id]; ok {
			result = append(result, *eip)
		}
	}

	return result, nil
}

func (m *mockDriver) AssociateAddress(
	_ context.Context, allocationID, instanceID string,
) (string, error) {
	eip, ok := m.eips[allocationID]
	if !ok {
		return "", fmt.Errorf("not found")
	}

	assocID := m.nextID("eipassoc")
	eip.AssociationID = assocID
	eip.InstanceID = instanceID

	return assocID, nil
}

func (m *mockDriver) DisassociateAddress(
	_ context.Context, associationID string,
) error {
	for _, eip := range m.eips {
		if eip.AssociationID == associationID {
			eip.AssociationID = ""
			eip.InstanceID = ""

			return nil
		}
	}

	return fmt.Errorf("not found")
}

func (m *mockDriver) AssociateRouteTable(
	_ context.Context, routeTableID, subnetID string,
) (*driver.RouteTableAssociation, error) {
	if _, ok := m.routeTables[routeTableID]; !ok {
		return nil, fmt.Errorf("not found")
	}

	id := m.nextID("rtbassoc")
	assoc := &driver.RouteTableAssociation{
		ID:           id,
		RouteTableID: routeTableID,
		SubnetID:     subnetID,
	}
	m.rtAssocs[id] = assoc

	return assoc, nil
}

func (m *mockDriver) DisassociateRouteTable(
	_ context.Context, associationID string,
) error {
	if _, ok := m.rtAssocs[associationID]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.rtAssocs, associationID)

	return nil
}

func (m *mockDriver) CreateVPCEndpoint(
	_ context.Context, config driver.VPCEndpointConfig,
) (*driver.VPCEndpoint, error) {
	if config.VPCID == "" {
		return nil, fmt.Errorf("vpc id required")
	}

	if config.ServiceName == "" {
		return nil, fmt.Errorf("service name required")
	}

	if _, ok := m.vpcs[config.VPCID]; !ok {
		return nil, fmt.Errorf("vpc not found")
	}

	id := m.nextID("vpce")
	ep := &driver.VPCEndpoint{
		ID:           id,
		VPCID:        config.VPCID,
		ServiceName:  config.ServiceName,
		EndpointType: config.EndpointType,
		State:        "available",
		Tags:         config.Tags,
	}
	m.endpoints[id] = ep

	return ep, nil
}

func (m *mockDriver) DeleteVPCEndpoint(
	_ context.Context, id string,
) error {
	if _, ok := m.endpoints[id]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.endpoints, id)

	return nil
}

func (m *mockDriver) DescribeVPCEndpoints(
	_ context.Context, ids []string,
) ([]driver.VPCEndpoint, error) {
	if len(ids) == 0 {
		result := make([]driver.VPCEndpoint, 0, len(m.endpoints))
		for _, ep := range m.endpoints {
			result = append(result, *ep)
		}

		return result, nil
	}

	var result []driver.VPCEndpoint

	for _, id := range ids {
		if ep, ok := m.endpoints[id]; ok {
			result = append(result, *ep)
		}
	}

	return result, nil
}

func (m *mockDriver) ModifyVPCEndpoint(
	_ context.Context, id string, config driver.VPCEndpointConfig,
) (*driver.VPCEndpoint, error) {
	ep, ok := m.endpoints[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	if len(config.SubnetIDs) > 0 {
		ep.SubnetIDs = config.SubnetIDs
	}

	if len(config.SecurityGroupIDs) > 0 {
		ep.SecurityGroupIDs = config.SecurityGroupIDs
	}

	if len(config.RouteTableIDs) > 0 {
		ep.RouteTableIDs = config.RouteTableIDs
	}

	if len(config.Tags) > 0 {
		ep.Tags = config.Tags
	}

	return ep, nil
}

func newTestNetworking(opts ...Option) *Networking {
	return NewNetworking(newMockDriver(), opts...)
}

func TestNewNetworking(t *testing.T) {
	n := newTestNetworking()
	require.NotNil(t, n)
	require.NotNil(t, n.driver)
}

func TestCreateVPC(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.0/16", info.CIDRBlock)
		assert.Equal(t, "available", info.State)
	})

	t.Run("empty cidr error", func(t *testing.T) {
		_, err := n.CreateVPC(ctx, driver.VPCConfig{})
		require.Error(t, err)
	})
}

func TestDeleteVPC(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	vpc, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := n.DeleteVPC(ctx, vpc.ID)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := n.DeleteVPC(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestDescribeVPCs(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	_, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	_, err = n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.1.0.0/16"})
	require.NoError(t, err)

	vpcs, err := n.DescribeVPCs(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, len(vpcs))
}

func TestCreateSubnet(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	vpc, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := n.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
		require.NoError(t, err)
		assert.Equal(t, vpc.ID, info.VPCID)
	})

	t.Run("empty vpc error", func(t *testing.T) {
		_, err := n.CreateSubnet(ctx, driver.SubnetConfig{})
		require.Error(t, err)
	})
}

func TestDeleteSubnet(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	vpc, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := n.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := n.DeleteSubnet(ctx, subnet.ID)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := n.DeleteSubnet(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestCreateSecurityGroup(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := n.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "my-sg", VPCID: "vpc-1"})
		require.NoError(t, err)
		assert.Equal(t, "my-sg", info.Name)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := n.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{})
		require.Error(t, err)
	})
}

func TestSecurityGroupRules(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	sg, err := n.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "rule-sg", VPCID: "vpc-1"})
	require.NoError(t, err)

	rule := driver.SecurityRule{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "0.0.0.0/0"}

	t.Run("add ingress", func(t *testing.T) {
		err := n.AddIngressRule(ctx, sg.ID, rule)
		require.NoError(t, err)
	})

	t.Run("add egress", func(t *testing.T) {
		err := n.AddEgressRule(ctx, sg.ID, rule)
		require.NoError(t, err)
	})

	t.Run("remove ingress", func(t *testing.T) {
		err := n.RemoveIngressRule(ctx, sg.ID, rule)
		require.NoError(t, err)
	})

	t.Run("remove egress", func(t *testing.T) {
		err := n.RemoveEgressRule(ctx, sg.ID, rule)
		require.NoError(t, err)
	})

	t.Run("ingress not found", func(t *testing.T) {
		err := n.AddIngressRule(ctx, "nonexistent", rule)
		require.Error(t, err)
	})
}

func TestPeeringConnection(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	pc, err := n.CreatePeeringConnection(ctx, driver.PeeringConfig{RequesterVPC: "vpc-1", AccepterVPC: "vpc-2"})
	require.NoError(t, err)
	assert.Equal(t, "pending-acceptance", pc.Status)

	t.Run("accept", func(t *testing.T) {
		err := n.AcceptPeeringConnection(ctx, pc.ID)
		require.NoError(t, err)
	})

	t.Run("describe", func(t *testing.T) {
		pcs, err := n.DescribePeeringConnections(ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(pcs))
	})

	t.Run("delete", func(t *testing.T) {
		err := n.DeletePeeringConnection(ctx, pc.ID)
		require.NoError(t, err)
	})

	t.Run("delete not found", func(t *testing.T) {
		err := n.DeletePeeringConnection(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestRejectPeeringConnection(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	pc, err := n.CreatePeeringConnection(ctx, driver.PeeringConfig{RequesterVPC: "vpc-1", AccepterVPC: "vpc-2"})
	require.NoError(t, err)

	err = n.RejectPeeringConnection(ctx, pc.ID)
	require.NoError(t, err)
}

func TestNATGateway(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	nat, err := n.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: "subnet-1"})
	require.NoError(t, err)
	assert.Equal(t, "available", nat.State)

	t.Run("describe", func(t *testing.T) {
		nats, err := n.DescribeNATGateways(ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(nats))
	})

	t.Run("delete", func(t *testing.T) {
		err := n.DeleteNATGateway(ctx, nat.ID)
		require.NoError(t, err)
	})

	t.Run("delete not found", func(t *testing.T) {
		err := n.DeleteNATGateway(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestFlowLog(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	fl, err := n.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: "vpc-1", ResourceType: "VPC", TrafficType: "ALL"})
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", fl.Status)

	t.Run("describe", func(t *testing.T) {
		fls, err := n.DescribeFlowLogs(ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(fls))
	})

	t.Run("get records", func(t *testing.T) {
		records, err := n.GetFlowLogRecords(ctx, fl.ID, 10)
		require.NoError(t, err)
		assert.Equal(t, 0, len(records))
	})

	t.Run("delete", func(t *testing.T) {
		err := n.DeleteFlowLog(ctx, fl.ID)
		require.NoError(t, err)
	})

	t.Run("delete not found", func(t *testing.T) {
		err := n.DeleteFlowLog(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestRouteTable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	rt, err := n.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: "vpc-1"})
	require.NoError(t, err)
	assert.NotEmpty(t, rt.ID)

	t.Run("create route", func(t *testing.T) {
		err := n.CreateRoute(ctx, rt.ID, "0.0.0.0/0", "igw-1", "gateway")
		require.NoError(t, err)
	})

	t.Run("delete route", func(t *testing.T) {
		err := n.DeleteRoute(ctx, rt.ID, "0.0.0.0/0")
		require.NoError(t, err)
	})

	t.Run("describe", func(t *testing.T) {
		rts, err := n.DescribeRouteTables(ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(rts))
	})

	t.Run("delete", func(t *testing.T) {
		err := n.DeleteRouteTable(ctx, rt.ID)
		require.NoError(t, err)
	})

	t.Run("delete not found", func(t *testing.T) {
		err := n.DeleteRouteTable(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestNetworkACL(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	// Create a VPC first so the ACL creation succeeds.
	vpc, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	acl, err := n.CreateNetworkACL(ctx, vpc.ID, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, acl.ID)

	t.Run("add rule", func(t *testing.T) {
		rule := &driver.NetworkACLRule{RuleNumber: 100, Protocol: "tcp", Action: "allow", CIDR: "0.0.0.0/0"}
		err := n.AddNetworkACLRule(ctx, acl.ID, rule)
		require.NoError(t, err)
	})

	t.Run("remove rule", func(t *testing.T) {
		err := n.RemoveNetworkACLRule(ctx, acl.ID, 100, false)
		require.NoError(t, err)
	})

	t.Run("describe", func(t *testing.T) {
		acls, err := n.DescribeNetworkACLs(ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(acls))
	})

	t.Run("delete", func(t *testing.T) {
		err := n.DeleteNetworkACL(ctx, acl.ID)
		require.NoError(t, err)
	})

	t.Run("vpc not found", func(t *testing.T) {
		_, err := n.CreateNetworkACL(ctx, "nonexistent", nil)
		require.Error(t, err)
	})
}

func TestNetworkingWithRecorder(t *testing.T) {
	rec := recorder.New()
	n := newTestNetworking(WithRecorder(rec))
	ctx := context.Background()

	_, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 1)

	createCalls := rec.CallCountFor("networking", "CreateVPC")
	assert.Equal(t, 1, createCalls)
}

func TestNetworkingWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	n := newTestNetworking(WithRecorder(rec))
	ctx := context.Background()

	_ = n.DeleteVPC(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last)
	assert.NotNil(t, last.Error)
}

func TestNetworkingWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	n := newTestNetworking(WithMetrics(mc))
	ctx := context.Background()

	_, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 1)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 1)
}

func TestNetworkingWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	n := newTestNetworking(WithMetrics(mc))
	ctx := context.Background()

	_ = n.DeleteVPC(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestNetworkingWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	n := newTestNetworking(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("networking", "CreateVPC", injectedErr, inject.Always{})

	_, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestNetworkingWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	n := newTestNetworking(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("networking", "DeleteVPC", injectedErr, inject.Always{})

	_, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	err = n.DeleteVPC(ctx, "vpc-1")
	require.Error(t, err)

	delCalls := rec.CallsFor("networking", "DeleteVPC")
	assert.Equal(t, 1, len(delCalls))
	assert.NotNil(t, delCalls[0].Error)
}

func TestNetworkingWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	n := newTestNetworking(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("networking", "CreateVPC", injectedErr, inject.Always{})

	_, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.Error(t, err)

	inj.Remove("networking", "CreateVPC")

	_, err = n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
}

func TestNetworkingWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	n := newTestNetworking(WithLatency(latency))
	ctx := context.Background()

	info, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0/16", info.CIDRBlock)
}

func TestNetworkingAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	n := NewNetworking(newMockDriver(),
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	_, err = n.DescribeVPCs(ctx, nil)
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestInternetGatewayPortable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	t.Run("create IGW", func(t *testing.T) {
		igw, err := n.CreateInternetGateway(ctx, driver.InternetGatewayConfig{
			Tags: map[string]string{"env": "test"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, igw.ID)
		assert.Equal(t, "detached", igw.State)
	})

	t.Run("describe all IGWs", func(t *testing.T) {
		igws, err := n.DescribeInternetGateways(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, igws, 1)
	})

	t.Run("describe by ID", func(t *testing.T) {
		igws, _ := n.DescribeInternetGateways(ctx, nil)
		require.NotEmpty(t, igws)

		filtered, err := n.DescribeInternetGateways(ctx, []string{igws[0].ID})
		require.NoError(t, err)
		assert.Len(t, filtered, 1)
		assert.Equal(t, igws[0].ID, filtered[0].ID)
	})

	t.Run("attach IGW to VPC", func(t *testing.T) {
		igws, _ := n.DescribeInternetGateways(ctx, nil)
		vpc, _ := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})

		err := n.AttachInternetGateway(ctx, igws[0].ID, vpc.ID)
		require.NoError(t, err)
	})

	t.Run("detach IGW", func(t *testing.T) {
		igws, _ := n.DescribeInternetGateways(ctx, nil)
		err := n.DetachInternetGateway(ctx, igws[0].ID, "vpc-1")
		require.NoError(t, err)
	})

	t.Run("delete IGW", func(t *testing.T) {
		igws, _ := n.DescribeInternetGateways(ctx, nil)
		err := n.DeleteInternetGateway(ctx, igws[0].ID)
		require.NoError(t, err)
	})

	t.Run("delete nonexistent IGW", func(t *testing.T) {
		err := n.DeleteInternetGateway(ctx, "igw-nonexistent")
		require.Error(t, err)
	})

	t.Run("attach nonexistent IGW", func(t *testing.T) {
		err := n.AttachInternetGateway(ctx, "igw-nonexistent", "vpc-1")
		require.Error(t, err)
	})

	t.Run("detach nonexistent IGW", func(t *testing.T) {
		err := n.DetachInternetGateway(ctx, "igw-nonexistent", "vpc-1")
		require.Error(t, err)
	})
}

func TestElasticIPPortable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	t.Run("allocate address", func(t *testing.T) {
		eip, err := n.AllocateAddress(ctx, driver.ElasticIPConfig{
			Tags: map[string]string{"env": "test"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, eip.AllocationID)
		assert.NotEmpty(t, eip.PublicIP)
	})

	t.Run("describe all addresses", func(t *testing.T) {
		eips, err := n.DescribeAddresses(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, eips, 1)
	})

	t.Run("describe by ID", func(t *testing.T) {
		eips, _ := n.DescribeAddresses(ctx, nil)
		require.NotEmpty(t, eips)

		filtered, err := n.DescribeAddresses(ctx, []string{eips[0].AllocationID})
		require.NoError(t, err)
		assert.Len(t, filtered, 1)
		assert.Equal(t, eips[0].AllocationID, filtered[0].AllocationID)
	})

	t.Run("associate address", func(t *testing.T) {
		eips, _ := n.DescribeAddresses(ctx, nil)
		assocID, err := n.AssociateAddress(ctx, eips[0].AllocationID, "i-12345")
		require.NoError(t, err)
		assert.NotEmpty(t, assocID)
	})

	t.Run("disassociate address", func(t *testing.T) {
		eips, _ := n.DescribeAddresses(ctx, nil)
		err := n.DisassociateAddress(ctx, eips[0].AssociationID)
		require.NoError(t, err)
	})

	t.Run("release address", func(t *testing.T) {
		eips, _ := n.DescribeAddresses(ctx, nil)
		err := n.ReleaseAddress(ctx, eips[0].AllocationID)
		require.NoError(t, err)

		eips, _ = n.DescribeAddresses(ctx, nil)
		assert.Empty(t, eips)
	})

	t.Run("release nonexistent", func(t *testing.T) {
		err := n.ReleaseAddress(ctx, "eipalloc-nonexistent")
		require.Error(t, err)
	})

	t.Run("associate nonexistent allocation", func(t *testing.T) {
		_, err := n.AssociateAddress(ctx, "eipalloc-nonexistent", "i-12345")
		require.Error(t, err)
	})

	t.Run("disassociate nonexistent", func(t *testing.T) {
		err := n.DisassociateAddress(ctx, "eipassoc-nonexistent")
		require.Error(t, err)
	})
}

func TestRouteTableAssociationPortable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	rt, err := n.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: "vpc-1"})
	require.NoError(t, err)

	t.Run("associate route table", func(t *testing.T) {
		assoc, err := n.AssociateRouteTable(ctx, rt.ID, "subnet-1")
		require.NoError(t, err)
		assert.NotEmpty(t, assoc.ID)
		assert.Equal(t, rt.ID, assoc.RouteTableID)
		assert.Equal(t, "subnet-1", assoc.SubnetID)
	})

	t.Run("disassociate route table", func(t *testing.T) {
		assoc, err := n.AssociateRouteTable(ctx, rt.ID, "subnet-2")
		require.NoError(t, err)

		err = n.DisassociateRouteTable(ctx, assoc.ID)
		require.NoError(t, err)
	})

	t.Run("associate nonexistent route table", func(t *testing.T) {
		_, err := n.AssociateRouteTable(ctx, "rtb-nonexistent", "subnet-1")
		require.Error(t, err)
	})

	t.Run("disassociate nonexistent", func(t *testing.T) {
		err := n.DisassociateRouteTable(ctx, "rtbassoc-nonexistent")
		require.Error(t, err)
	})
}

func TestGetFlowLogRecordsPortable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	fl, err := n.CreateFlowLog(ctx, driver.FlowLogConfig{
		ResourceID: "vpc-1", ResourceType: "VPC", TrafficType: "ALL",
	})
	require.NoError(t, err)

	t.Run("get records", func(t *testing.T) {
		records, err := n.GetFlowLogRecords(ctx, fl.ID, 10)
		require.NoError(t, err)
		// Mock driver returns empty slice.
		assert.Equal(t, 0, len(records))
	})

	t.Run("not found", func(t *testing.T) {
		_, err := n.GetFlowLogRecords(ctx, "fl-nonexistent", 10)
		require.Error(t, err)
	})
}

func TestDescribeSubnetsPortable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	vpc, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	sub, err := n.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	subnets, err := n.DescribeSubnets(ctx, nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(subnets), 1)

	subnets, err = n.DescribeSubnets(ctx, []string{sub.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, len(subnets))
}

func TestDeleteSecurityGroupPortable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	vpc, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	sg, err := n.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{VPCID: vpc.ID, Name: "test-sg", Description: "test"})
	require.NoError(t, err)

	err = n.DeleteSecurityGroup(ctx, sg.ID)
	require.NoError(t, err)

	err = n.DeleteSecurityGroup(ctx, "sg-nonexistent")
	require.Error(t, err)
}

func TestDescribeSecurityGroupsPortable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	vpc, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	sg, err := n.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{VPCID: vpc.ID, Name: "test-sg", Description: "test"})
	require.NoError(t, err)

	groups, err := n.DescribeSecurityGroups(ctx, nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(groups), 1)

	groups, err = n.DescribeSecurityGroups(ctx, []string{sg.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, len(groups))
}

func TestNetworkingWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	limiter := ratelimit.New(1, 1, fc)
	n := newTestNetworking(WithRateLimiter(limiter))
	ctx := context.Background()

	_, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	_, err = n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.1.0.0/16"})
	require.Error(t, err)
}

func TestVPCEndpointPortable(t *testing.T) {
	n := newTestNetworking()
	ctx := context.Background()

	// Create a VPC first so endpoint creation succeeds.
	vpc, err := n.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	t.Run("create endpoint", func(t *testing.T) {
		ep, err := n.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
			VPCID:        vpc.ID,
			ServiceName:  "com.amazonaws.us-east-1.s3",
			EndpointType: "Gateway",
			Tags:         map[string]string{"env": "test"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, ep.ID)
		assert.Equal(t, "available", ep.State)
		assert.Equal(t, vpc.ID, ep.VPCID)
		assert.Equal(t, "com.amazonaws.us-east-1.s3", ep.ServiceName)
	})

	t.Run("create endpoint missing vpc id", func(t *testing.T) {
		_, err := n.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
			ServiceName: "com.amazonaws.us-east-1.s3",
		})
		require.Error(t, err)
	})

	t.Run("create endpoint missing service name", func(t *testing.T) {
		_, err := n.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
			VPCID: vpc.ID,
		})
		require.Error(t, err)
	})

	t.Run("create endpoint vpc not found", func(t *testing.T) {
		_, err := n.CreateVPCEndpoint(ctx, driver.VPCEndpointConfig{
			VPCID:       "vpc-nonexistent",
			ServiceName: "svc",
		})
		require.Error(t, err)
	})

	t.Run("describe all endpoints", func(t *testing.T) {
		eps, err := n.DescribeVPCEndpoints(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, eps, 1)
	})

	t.Run("describe by ID", func(t *testing.T) {
		eps, _ := n.DescribeVPCEndpoints(ctx, nil)
		require.NotEmpty(t, eps)

		filtered, err := n.DescribeVPCEndpoints(ctx, []string{eps[0].ID})
		require.NoError(t, err)
		assert.Len(t, filtered, 1)
		assert.Equal(t, eps[0].ID, filtered[0].ID)
	})

	t.Run("modify endpoint", func(t *testing.T) {
		eps, _ := n.DescribeVPCEndpoints(ctx, nil)
		require.NotEmpty(t, eps)

		modified, err := n.ModifyVPCEndpoint(ctx, eps[0].ID, driver.VPCEndpointConfig{
			SubnetIDs: []string{"subnet-1", "subnet-2"},
			Tags:      map[string]string{"env": "prod"},
		})
		require.NoError(t, err)
		assert.Equal(t, eps[0].ID, modified.ID)
	})

	t.Run("modify nonexistent endpoint", func(t *testing.T) {
		_, err := n.ModifyVPCEndpoint(ctx, "vpce-nonexistent", driver.VPCEndpointConfig{
			SubnetIDs: []string{"subnet-1"},
		})
		require.Error(t, err)
	})

	t.Run("delete endpoint", func(t *testing.T) {
		eps, _ := n.DescribeVPCEndpoints(ctx, nil)
		require.NotEmpty(t, eps)

		err := n.DeleteVPCEndpoint(ctx, eps[0].ID)
		require.NoError(t, err)

		eps, _ = n.DescribeVPCEndpoints(ctx, nil)
		assert.Empty(t, eps)
	})

	t.Run("delete nonexistent endpoint", func(t *testing.T) {
		err := n.DeleteVPCEndpoint(ctx, "vpce-nonexistent")
		require.Error(t, err)
	})
}
