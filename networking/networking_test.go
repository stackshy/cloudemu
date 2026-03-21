package networking

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements driver.Networking for testing the portable wrapper.
type mockDriver struct {
	vpcs           map[string]*driver.VPCInfo
	subnets        map[string]*driver.SubnetInfo
	securityGroups map[string]*driver.SecurityGroupInfo
	peerings       map[string]*driver.PeeringConnection
	natGateways    map[string]*driver.NATGateway
	flowLogs       map[string]*driver.FlowLog
	routeTables    map[string]*driver.RouteTable
	networkACLs    map[string]*driver.NetworkACL
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
