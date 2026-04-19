package vpc

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/networking/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	return New(opts)
}

func createTestVPC(m *Mock) *driver.VPCInfo {
	info, _ := m.CreateVPC(context.Background(), driver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
		Tags:      map[string]string{"env": "test"},
	})
	return info
}

func TestCreateVPC(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.VPCConfig
		expectErr bool
	}{
		{name: "success", cfg: driver.VPCConfig{CIDRBlock: "10.0.0.0/16"}},
		{name: "empty CIDR", cfg: driver.VPCConfig{}, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			info, err := m.CreateVPC(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, info.ID)
			assertEqual(t, "10.0.0.0/16", info.CIDRBlock)
			assertEqual(t, "available", info.State)
		})
	}
}

func TestDeleteVPC(t *testing.T) {
	m := newTestMock()
	v := createTestVPC(m)

	requireNoError(t, m.DeleteVPC(context.Background(), v.ID))
	assertError(t, m.DeleteVPC(context.Background(), "vpc-nope"), true)
}

func TestDescribeVPCs(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v1 := createTestVPC(m)
	v2 := createTestVPC(m)

	t.Run("all", func(t *testing.T) {
		vpcs, err := m.DescribeVPCs(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(vpcs))
	})

	t.Run("by ID", func(t *testing.T) {
		vpcs, err := m.DescribeVPCs(ctx, []string{v1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(vpcs))
		assertEqual(t, v1.ID, vpcs[0].ID)
	})

	t.Run("nonexistent ID", func(t *testing.T) {
		vpcs, err := m.DescribeVPCs(ctx, []string{"vpc-nope"})
		requireNoError(t, err)
		assertEqual(t, 0, len(vpcs))
	})

	_ = v2 // used to create second VPC
}

func TestCreateSubnet(t *testing.T) {
	m := newTestMock()
	v := createTestVPC(m)

	tests := []struct {
		name      string
		cfg       driver.SubnetConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg: driver.SubnetConfig{
				VPCID: v.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
			},
		},
		{name: "empty VPC ID", cfg: driver.SubnetConfig{CIDRBlock: "10.0.1.0/24"}, expectErr: true},
		{name: "empty CIDR", cfg: driver.SubnetConfig{VPCID: v.ID}, expectErr: true},
		{
			name: "VPC not found",
			cfg:  driver.SubnetConfig{VPCID: "vpc-nope", CIDRBlock: "10.0.1.0/24"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, err := m.CreateSubnet(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, info.ID)
			assertEqual(t, v.ID, info.VPCID)
			assertEqual(t, "available", info.State)
		})
	}
}

func TestDeleteSubnet(t *testing.T) {
	m := newTestMock()
	v := createTestVPC(m)
	s, _ := m.CreateSubnet(context.Background(), driver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24",
	})

	requireNoError(t, m.DeleteSubnet(context.Background(), s.ID))
	assertError(t, m.DeleteSubnet(context.Background(), "subnet-nope"), true)
}

func TestDescribeSubnets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	s1, _ := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	_, _ = m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.2.0/24"})

	t.Run("all", func(t *testing.T) {
		subnets, err := m.DescribeSubnets(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(subnets))
	})

	t.Run("by ID", func(t *testing.T) {
		subnets, err := m.DescribeSubnets(ctx, []string{s1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(subnets))
	})
}

func TestCreateSecurityGroup(t *testing.T) {
	m := newTestMock()
	v := createTestVPC(m)

	tests := []struct {
		name      string
		cfg       driver.SecurityGroupConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg:  driver.SecurityGroupConfig{Name: "web-sg", Description: "Web SG", VPCID: v.ID},
		},
		{name: "empty name", cfg: driver.SecurityGroupConfig{VPCID: v.ID}, expectErr: true},
		{name: "empty VPC ID", cfg: driver.SecurityGroupConfig{Name: "sg"}, expectErr: true},
		{
			name:      "VPC not found",
			cfg:       driver.SecurityGroupConfig{Name: "sg", VPCID: "vpc-nope"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, err := m.CreateSecurityGroup(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, info.ID)
			assertEqual(t, "web-sg", info.Name)
			assertEqual(t, v.ID, info.VPCID)
		})
	}
}

func TestDeleteSecurityGroup(t *testing.T) {
	m := newTestMock()
	v := createTestVPC(m)
	sg, _ := m.CreateSecurityGroup(context.Background(), driver.SecurityGroupConfig{
		Name: "sg", VPCID: v.ID,
	})

	requireNoError(t, m.DeleteSecurityGroup(context.Background(), sg.ID))
	assertError(t, m.DeleteSecurityGroup(context.Background(), "sg-nope"), true)
}

func TestDescribeSecurityGroups(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	sg1, _ := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "sg1", VPCID: v.ID})
	_, _ = m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "sg2", VPCID: v.ID})

	t.Run("all", func(t *testing.T) {
		sgs, err := m.DescribeSecurityGroups(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(sgs))
	})

	t.Run("by ID", func(t *testing.T) {
		sgs, err := m.DescribeSecurityGroups(ctx, []string{sg1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(sgs))
	})
}

func TestAddAndRemoveSecurityGroupRules(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	sg, _ := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "sg", VPCID: v.ID})

	ingressRule := driver.SecurityRule{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "0.0.0.0/0"}
	egressRule := driver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0"}

	t.Run("add ingress rule", func(t *testing.T) {
		err := m.AddIngressRule(ctx, sg.ID, ingressRule)
		requireNoError(t, err)

		sgs, _ := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		assertEqual(t, 1, len(sgs[0].IngressRules))
		assertEqual(t, 80, sgs[0].IngressRules[0].FromPort)
	})

	t.Run("add egress rule", func(t *testing.T) {
		err := m.AddEgressRule(ctx, sg.ID, egressRule)
		requireNoError(t, err)

		sgs, _ := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		assertEqual(t, 1, len(sgs[0].EgressRules))
	})

	t.Run("remove ingress rule", func(t *testing.T) {
		err := m.RemoveIngressRule(ctx, sg.ID, ingressRule)
		requireNoError(t, err)

		sgs, _ := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		assertEqual(t, 0, len(sgs[0].IngressRules))
	})

	t.Run("remove egress rule", func(t *testing.T) {
		err := m.RemoveEgressRule(ctx, sg.ID, egressRule)
		requireNoError(t, err)

		sgs, _ := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		assertEqual(t, 0, len(sgs[0].EgressRules))
	})

	t.Run("remove nonexistent ingress rule", func(t *testing.T) {
		err := m.RemoveIngressRule(ctx, sg.ID, ingressRule)
		assertError(t, err, true)
	})

	t.Run("remove nonexistent egress rule", func(t *testing.T) {
		err := m.RemoveEgressRule(ctx, sg.ID, egressRule)
		assertError(t, err, true)
	})

	t.Run("add rule to nonexistent SG", func(t *testing.T) {
		err := m.AddIngressRule(ctx, "sg-nope", ingressRule)
		assertError(t, err, true)

		err = m.AddEgressRule(ctx, "sg-nope", egressRule)
		assertError(t, err, true)
	})

	t.Run("remove rule from nonexistent SG", func(t *testing.T) {
		err := m.RemoveIngressRule(ctx, "sg-nope", ingressRule)
		assertError(t, err, true)

		err = m.RemoveEgressRule(ctx, "sg-nope", egressRule)
		assertError(t, err, true)
	})
}

func TestCreatePeeringConnection(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v1 := createTestVPC(m)
	v2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})

	t.Run("success", func(t *testing.T) {
		pc, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
			RequesterVPC: v1.ID,
			AccepterVPC:  v2.ID,
		})
		requireNoError(t, err)
		assertNotEmpty(t, pc.ID)
		assertEqual(t, "pending-acceptance", pc.Status)
		assertEqual(t, v1.ID, pc.RequesterVPC)
		assertEqual(t, v2.ID, pc.AccepterVPC)
	})

	t.Run("missing VPC IDs", func(t *testing.T) {
		_, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{})
		assertError(t, err, true)
	})

	t.Run("nonexistent requester VPC", func(t *testing.T) {
		_, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
			RequesterVPC: "vpc-nope", AccepterVPC: v2.ID,
		})
		assertError(t, err, true)
	})

	t.Run("nonexistent accepter VPC", func(t *testing.T) {
		_, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
			RequesterVPC: v1.ID, AccepterVPC: "vpc-nope",
		})
		assertError(t, err, true)
	})
}

func TestAcceptPeeringConnection(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v1 := createTestVPC(m)
	v2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})

	pc, _ := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: v1.ID, AccepterVPC: v2.ID,
	})

	t.Run("accept pending", func(t *testing.T) {
		err := m.AcceptPeeringConnection(ctx, pc.ID)
		requireNoError(t, err)

		pcs, _ := m.DescribePeeringConnections(ctx, []string{pc.ID})
		assertEqual(t, 1, len(pcs))
		assertEqual(t, "active", pcs[0].Status)
	})

	t.Run("accept already active fails", func(t *testing.T) {
		err := m.AcceptPeeringConnection(ctx, pc.ID)
		assertError(t, err, true)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.AcceptPeeringConnection(ctx, "pcx-nope")
		assertError(t, err, true)
	})
}

func TestRejectPeeringConnection(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v1 := createTestVPC(m)
	v2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})

	pc, _ := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: v1.ID, AccepterVPC: v2.ID,
	})

	t.Run("reject pending", func(t *testing.T) {
		err := m.RejectPeeringConnection(ctx, pc.ID)
		requireNoError(t, err)

		pcs, _ := m.DescribePeeringConnections(ctx, []string{pc.ID})
		assertEqual(t, 1, len(pcs))
		assertEqual(t, "rejected", pcs[0].Status)
	})

	t.Run("reject already rejected fails", func(t *testing.T) {
		err := m.RejectPeeringConnection(ctx, pc.ID)
		assertError(t, err, true)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.RejectPeeringConnection(ctx, "pcx-nope")
		assertError(t, err, true)
	})
}

func TestDeletePeeringConnection(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v1 := createTestVPC(m)
	v2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})

	pc, _ := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: v1.ID, AccepterVPC: v2.ID,
	})

	t.Run("delete existing", func(t *testing.T) {
		err := m.DeletePeeringConnection(ctx, pc.ID)
		requireNoError(t, err)

		pcs, _ := m.DescribePeeringConnections(ctx, []string{pc.ID})
		assertEqual(t, 0, len(pcs))
	})

	t.Run("delete nonexistent", func(t *testing.T) {
		err := m.DeletePeeringConnection(ctx, "pcx-nope")
		assertError(t, err, true)
	})
}

func TestDescribePeeringConnections(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v1 := createTestVPC(m)
	v2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})
	v3, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "192.168.0.0/16"})

	pc1, _ := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: v1.ID, AccepterVPC: v2.ID,
	})
	_, _ = m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: v1.ID, AccepterVPC: v3.ID,
	})

	t.Run("all", func(t *testing.T) {
		pcs, err := m.DescribePeeringConnections(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(pcs))
	})

	t.Run("by ID", func(t *testing.T) {
		pcs, err := m.DescribePeeringConnections(ctx, []string{pc1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(pcs))
		assertEqual(t, pc1.ID, pcs[0].ID)
	})
}

func TestCreateNATGateway(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	s, _ := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})

	t.Run("success", func(t *testing.T) {
		nat, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: s.ID})
		requireNoError(t, err)
		assertNotEmpty(t, nat.ID)
		assertEqual(t, s.ID, nat.SubnetID)
		assertEqual(t, v.ID, nat.VPCID)
		assertEqual(t, "available", nat.State)
		assertNotEmpty(t, nat.PublicIP)
	})

	t.Run("empty subnet ID", func(t *testing.T) {
		_, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{})
		assertError(t, err, true)
	})

	t.Run("nonexistent subnet", func(t *testing.T) {
		_, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: "subnet-nope"})
		assertError(t, err, true)
	})
}

func TestDeleteNATGateway(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	s, _ := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	nat, _ := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: s.ID})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteNATGateway(ctx, nat.ID)
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteNATGateway(ctx, "nat-nope")
		assertError(t, err, true)
	})
}

func TestDescribeNATGateways(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	s, _ := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	nat1, _ := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: s.ID})
	_, _ = m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: s.ID})

	t.Run("all", func(t *testing.T) {
		nats, err := m.DescribeNATGateways(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(nats))
	})

	t.Run("by ID", func(t *testing.T) {
		nats, err := m.DescribeNATGateways(ctx, []string{nat1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(nats))
		assertEqual(t, nat1.ID, nats[0].ID)
	})
}

func TestCreateFlowLog(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)

	t.Run("VPC flow log", func(t *testing.T) {
		fl, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{
			ResourceID:   v.ID,
			ResourceType: "VPC",
			TrafficType:  "ALL",
		})
		requireNoError(t, err)
		assertNotEmpty(t, fl.ID)
		assertEqual(t, v.ID, fl.ResourceID)
		assertEqual(t, "VPC", fl.ResourceType)
		assertEqual(t, "ALL", fl.TrafficType)
		assertEqual(t, "ACTIVE", fl.Status)
	})

	t.Run("empty resource ID", func(t *testing.T) {
		_, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceType: "VPC"})
		assertError(t, err, true)
	})

	t.Run("nonexistent VPC", func(t *testing.T) {
		_, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{
			ResourceID: "vpc-nope", ResourceType: "VPC",
		})
		assertError(t, err, true)
	})

	t.Run("unsupported resource type", func(t *testing.T) {
		_, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{
			ResourceID: v.ID, ResourceType: "Unknown",
		})
		assertError(t, err, true)
	})
}

func TestDeleteFlowLog(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	fl, _ := m.CreateFlowLog(ctx, driver.FlowLogConfig{
		ResourceID: v.ID, ResourceType: "VPC",
	})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteFlowLog(ctx, fl.ID)
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteFlowLog(ctx, "fl-nope")
		assertError(t, err, true)
	})
}

func TestDescribeFlowLogs(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	fl1, _ := m.CreateFlowLog(ctx, driver.FlowLogConfig{
		ResourceID: v.ID, ResourceType: "VPC", TrafficType: "ACCEPT",
	})
	_, _ = m.CreateFlowLog(ctx, driver.FlowLogConfig{
		ResourceID: v.ID, ResourceType: "VPC", TrafficType: "REJECT",
	})

	t.Run("all", func(t *testing.T) {
		fls, err := m.DescribeFlowLogs(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(fls))
	})

	t.Run("by ID", func(t *testing.T) {
		fls, err := m.DescribeFlowLogs(ctx, []string{fl1.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(fls))
		assertEqual(t, fl1.ID, fls[0].ID)
	})
}

func TestGetFlowLogRecords(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	fl, _ := m.CreateFlowLog(ctx, driver.FlowLogConfig{
		ResourceID: v.ID, ResourceType: "VPC", TrafficType: "ALL",
	})

	t.Run("returns records", func(t *testing.T) {
		records, err := m.GetFlowLogRecords(ctx, fl.ID, 5)
		requireNoError(t, err)
		assertEqual(t, 5, len(records))
		assertEqual(t, fl.ID, records[0].FlowLogID)
		assertNotEmpty(t, records[0].SourceIP)
		assertNotEmpty(t, records[0].DestIP)
	})

	t.Run("default limit", func(t *testing.T) {
		records, err := m.GetFlowLogRecords(ctx, fl.ID, 0)
		requireNoError(t, err)
		assertEqual(t, 10, len(records))
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetFlowLogRecords(ctx, "fl-nope", 5)
		assertError(t, err, true)
	})
}

func TestCreateRouteTable(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)

	t.Run("success with default route", func(t *testing.T) {
		rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
		requireNoError(t, err)
		assertNotEmpty(t, rt.ID)
		assertEqual(t, v.ID, rt.VPCID)
		// Should have a default local route.
		assertEqual(t, 1, len(rt.Routes))
		assertEqual(t, "10.0.0.0/16", rt.Routes[0].DestinationCIDR)
		assertEqual(t, "local", rt.Routes[0].TargetType)
		assertEqual(t, "active", rt.Routes[0].State)
	})

	t.Run("empty VPC ID", func(t *testing.T) {
		_, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{})
		assertError(t, err, true)
	})

	t.Run("nonexistent VPC", func(t *testing.T) {
		_, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: "vpc-nope"})
		assertError(t, err, true)
	})
}

func TestCreateRoute(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	rt, _ := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})

	t.Run("add route", func(t *testing.T) {
		err := m.CreateRoute(ctx, rt.ID, "0.0.0.0/0", "igw-12345", "gateway")
		requireNoError(t, err)

		rts, _ := m.DescribeRouteTables(ctx, []string{rt.ID})
		assertEqual(t, 2, len(rts[0].Routes))
	})

	t.Run("duplicate CIDR fails", func(t *testing.T) {
		err := m.CreateRoute(ctx, rt.ID, "0.0.0.0/0", "igw-other", "gateway")
		assertError(t, err, true)
	})

	t.Run("route table not found", func(t *testing.T) {
		err := m.CreateRoute(ctx, "rtb-nope", "10.1.0.0/16", "igw-1", "gateway")
		assertError(t, err, true)
	})
}

func TestDeleteRoute(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	rt, _ := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	_ = m.CreateRoute(ctx, rt.ID, "0.0.0.0/0", "igw-12345", "gateway")

	t.Run("delete existing route", func(t *testing.T) {
		err := m.DeleteRoute(ctx, rt.ID, "0.0.0.0/0")
		requireNoError(t, err)

		rts, _ := m.DescribeRouteTables(ctx, []string{rt.ID})
		assertEqual(t, 1, len(rts[0].Routes)) // only local route remains
	})

	t.Run("delete nonexistent route", func(t *testing.T) {
		err := m.DeleteRoute(ctx, rt.ID, "192.168.0.0/16")
		assertError(t, err, true)
	})

	t.Run("route table not found", func(t *testing.T) {
		err := m.DeleteRoute(ctx, "rtb-nope", "0.0.0.0/0")
		assertError(t, err, true)
	})
}

func TestDeleteRouteTable(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	rt, _ := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteRouteTable(ctx, rt.ID)
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteRouteTable(ctx, "rtb-nope")
		assertError(t, err, true)
	})
}

func TestCreateNetworkACL(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)

	t.Run("success", func(t *testing.T) {
		acl, err := m.CreateNetworkACL(ctx, v.ID, map[string]string{"env": "test"})
		requireNoError(t, err)
		assertNotEmpty(t, acl.ID)
		assertEqual(t, v.ID, acl.VPCID)
		assertEqual(t, false, acl.IsDefault)
		// Should have 2 default rules (ingress + egress allow-all).
		assertEqual(t, 2, len(acl.Rules))
	})

	t.Run("empty VPC ID", func(t *testing.T) {
		_, err := m.CreateNetworkACL(ctx, "", nil)
		assertError(t, err, true)
	})

	t.Run("nonexistent VPC", func(t *testing.T) {
		_, err := m.CreateNetworkACL(ctx, "vpc-nope", nil)
		assertError(t, err, true)
	})
}

func TestAddNetworkACLRule(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	acl, _ := m.CreateNetworkACL(ctx, v.ID, nil)

	t.Run("add rule and verify ordering", func(t *testing.T) {
		// Add a rule with lower number than default (100).
		err := m.AddNetworkACLRule(ctx, acl.ID, &driver.NetworkACLRule{
			RuleNumber: 50,
			Protocol:   "tcp",
			Action:     "deny",
			CIDR:       "10.0.0.0/8",
			FromPort:   22,
			ToPort:     22,
			Egress:     false,
		})
		requireNoError(t, err)

		acls, _ := m.DescribeNetworkACLs(ctx, []string{acl.ID})
		assertEqual(t, 3, len(acls[0].Rules))
		// First rule should be number 50 (sorted).
		assertEqual(t, 50, acls[0].Rules[0].RuleNumber)
	})

	t.Run("ACL not found", func(t *testing.T) {
		err := m.AddNetworkACLRule(ctx, "acl-nope", &driver.NetworkACLRule{
			RuleNumber: 10, Protocol: "tcp", Action: "allow", CIDR: "0.0.0.0/0",
		})
		assertError(t, err, true)
	})
}

func TestRemoveNetworkACLRule(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	acl, _ := m.CreateNetworkACL(ctx, v.ID, nil)

	t.Run("remove default ingress rule", func(t *testing.T) {
		err := m.RemoveNetworkACLRule(ctx, acl.ID, 100, false)
		requireNoError(t, err)

		acls, _ := m.DescribeNetworkACLs(ctx, []string{acl.ID})
		assertEqual(t, 1, len(acls[0].Rules))
		// Only egress rule remains.
		assertEqual(t, true, acls[0].Rules[0].Egress)
	})

	t.Run("remove nonexistent rule", func(t *testing.T) {
		err := m.RemoveNetworkACLRule(ctx, acl.ID, 999, false)
		assertError(t, err, true)
	})

	t.Run("ACL not found", func(t *testing.T) {
		err := m.RemoveNetworkACLRule(ctx, "acl-nope", 100, false)
		assertError(t, err, true)
	})
}

func TestDeleteNetworkACL(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	acl, _ := m.CreateNetworkACL(ctx, v.ID, nil)

	t.Run("success", func(t *testing.T) {
		err := m.DeleteNetworkACL(ctx, acl.ID)
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteNetworkACL(ctx, "acl-nope")
		assertError(t, err, true)
	})
}

func TestInternetGateway(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)

	t.Run("create IGW", func(t *testing.T) {
		igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{
			Tags: map[string]string{"env": "test"},
		})
		requireNoError(t, err)
		assertNotEmpty(t, igw.ID)
		assertEqual(t, "detached", igw.State)
	})

	t.Run("describe IGWs", func(t *testing.T) {
		igws, err := m.DescribeInternetGateways(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(igws))
		assertEqual(t, "detached", igws[0].State)
	})

	t.Run("attach IGW to VPC", func(t *testing.T) {
		igws, _ := m.DescribeInternetGateways(ctx, nil)
		err := m.AttachInternetGateway(ctx, igws[0].ID, v.ID)
		requireNoError(t, err)

		igws, _ = m.DescribeInternetGateways(ctx, []string{igws[0].ID})
		assertEqual(t, 1, len(igws))
		assertEqual(t, "attached", igws[0].State)
		assertEqual(t, v.ID, igws[0].VpcID)
	})

	t.Run("detach IGW from VPC", func(t *testing.T) {
		igws, _ := m.DescribeInternetGateways(ctx, nil)
		err := m.DetachInternetGateway(ctx, igws[0].ID, v.ID)
		requireNoError(t, err)

		igws, _ = m.DescribeInternetGateways(ctx, []string{igws[0].ID})
		assertEqual(t, 1, len(igws))
		assertEqual(t, "detached", igws[0].State)
	})

	t.Run("delete IGW", func(t *testing.T) {
		igws, _ := m.DescribeInternetGateways(ctx, nil)
		err := m.DeleteInternetGateway(ctx, igws[0].ID)
		requireNoError(t, err)

		igws, _ = m.DescribeInternetGateways(ctx, nil)
		assertEqual(t, 0, len(igws))
	})

	t.Run("delete nonexistent IGW", func(t *testing.T) {
		err := m.DeleteInternetGateway(ctx, "igw-nope")
		assertError(t, err, true)
	})

	t.Run("attach to nonexistent VPC", func(t *testing.T) {
		igw, _ := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
		err := m.AttachInternetGateway(ctx, igw.ID, "vpc-nope")
		assertError(t, err, true)
	})

	t.Run("attach nonexistent IGW", func(t *testing.T) {
		err := m.AttachInternetGateway(ctx, "igw-nope", v.ID)
		assertError(t, err, true)
	})

	t.Run("detach nonexistent IGW", func(t *testing.T) {
		err := m.DetachInternetGateway(ctx, "igw-nope", v.ID)
		assertError(t, err, true)
	})

	t.Run("describe by ID", func(t *testing.T) {
		igw, _ := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
		igws, err := m.DescribeInternetGateways(ctx, []string{igw.ID})
		requireNoError(t, err)
		assertEqual(t, 1, len(igws))
		assertEqual(t, igw.ID, igws[0].ID)
	})

	t.Run("describe nonexistent ID", func(t *testing.T) {
		igws, err := m.DescribeInternetGateways(ctx, []string{"igw-nope"})
		requireNoError(t, err)
		assertEqual(t, 0, len(igws))
	})
}

func TestElasticIP(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	t.Run("allocate address", func(t *testing.T) {
		eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{
			Tags: map[string]string{"env": "test"},
		})
		requireNoError(t, err)
		assertNotEmpty(t, eip.AllocationID)
		assertNotEmpty(t, eip.PublicIP)
	})

	t.Run("describe addresses", func(t *testing.T) {
		eips, err := m.DescribeAddresses(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(eips))
	})

	t.Run("associate address", func(t *testing.T) {
		eips, _ := m.DescribeAddresses(ctx, nil)
		assocID, err := m.AssociateAddress(ctx, eips[0].AllocationID, "i-12345")
		requireNoError(t, err)
		assertNotEmpty(t, assocID)

		eips, _ = m.DescribeAddresses(ctx, []string{eips[0].AllocationID})
		assertEqual(t, 1, len(eips))
		assertEqual(t, assocID, eips[0].AssociationID)
		assertEqual(t, "i-12345", eips[0].InstanceID)
	})

	t.Run("disassociate address", func(t *testing.T) {
		eips, _ := m.DescribeAddresses(ctx, nil)
		err := m.DisassociateAddress(ctx, eips[0].AssociationID)
		requireNoError(t, err)

		eips, _ = m.DescribeAddresses(ctx, nil)
		assertEqual(t, "", eips[0].AssociationID)
		assertEqual(t, "", eips[0].InstanceID)
	})

	t.Run("release address", func(t *testing.T) {
		eips, _ := m.DescribeAddresses(ctx, nil)
		err := m.ReleaseAddress(ctx, eips[0].AllocationID)
		requireNoError(t, err)

		eips, _ = m.DescribeAddresses(ctx, nil)
		assertEqual(t, 0, len(eips))
	})

	t.Run("release nonexistent", func(t *testing.T) {
		err := m.ReleaseAddress(ctx, "eipalloc-nope")
		assertError(t, err, true)
	})

	t.Run("associate nonexistent allocation", func(t *testing.T) {
		_, err := m.AssociateAddress(ctx, "eipalloc-nope", "i-12345")
		assertError(t, err, true)
	})

	t.Run("disassociate nonexistent", func(t *testing.T) {
		err := m.DisassociateAddress(ctx, "eipassoc-nope")
		assertError(t, err, true)
	})

	t.Run("describe by ID", func(t *testing.T) {
		eip, _ := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
		eips, err := m.DescribeAddresses(ctx, []string{eip.AllocationID})
		requireNoError(t, err)
		assertEqual(t, 1, len(eips))
		assertEqual(t, eip.AllocationID, eips[0].AllocationID)
	})

	t.Run("describe nonexistent ID", func(t *testing.T) {
		eips, err := m.DescribeAddresses(ctx, []string{"eipalloc-nope"})
		requireNoError(t, err)
		assertEqual(t, 0, len(eips))
	})
}

func TestRouteTableAssociation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	v := createTestVPC(m)
	s, _ := m.CreateSubnet(ctx, driver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24",
	})
	rt, _ := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})

	t.Run("associate route table", func(t *testing.T) {
		assoc, err := m.AssociateRouteTable(ctx, rt.ID, s.ID)
		requireNoError(t, err)
		assertNotEmpty(t, assoc.ID)
		assertEqual(t, rt.ID, assoc.RouteTableID)
		assertEqual(t, s.ID, assoc.SubnetID)
	})

	t.Run("disassociate route table", func(t *testing.T) {
		// Re-associate to get a fresh association ID.
		assoc, _ := m.AssociateRouteTable(ctx, rt.ID, s.ID)
		err := m.DisassociateRouteTable(ctx, assoc.ID)
		requireNoError(t, err)
	})

	t.Run("associate nonexistent route table", func(t *testing.T) {
		_, err := m.AssociateRouteTable(ctx, "rtb-nope", s.ID)
		assertError(t, err, true)
	})

	t.Run("disassociate nonexistent", func(t *testing.T) {
		err := m.DisassociateRouteTable(ctx, "rtbassoc-nope")
		assertError(t, err, true)
	})
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}
