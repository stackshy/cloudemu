package gcpvpc

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/networking/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	return New(opts)
}

func TestCreateVPC(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.VPCConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.VPCConfig{CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "test"}}},
		{name: "empty CIDR", cfg: driver.VPCConfig{}, wantErr: true, errSubstr: "CIDR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateVPC(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ID)
				assert.Equal(t, "10.0.0.0/16", info.CIDRBlock)
				assert.Equal(t, "READY", info.State)
				assert.Equal(t, "test", info.Tags["env"])
			}
		})
	}
}

func TestDeleteVPC(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		id        string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", id: vpc.ID},
		{name: "not found", id: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteVPC(ctx, tt.id)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeVPCs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	_, err = m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all vpcs", ids: nil, wantCount: 2},
		{name: "by id", ids: []string{vpc1.ID}, wantCount: 1},
		{name: "unknown id", ids: []string{"nope"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpcs, descErr := m.DescribeVPCs(ctx, tt.ids)
			require.NoError(t, descErr)
			assert.Len(t, vpcs, tt.wantCount)
		})
	}
}

func TestCreateSubnet(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.SubnetConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-central1-a"}},
		{name: "empty VPC ID", cfg: driver.SubnetConfig{CIDRBlock: "10.0.1.0/24"}, wantErr: true, errSubstr: "VPC ID"},
		{name: "empty CIDR", cfg: driver.SubnetConfig{VPCID: vpc.ID}, wantErr: true, errSubstr: "CIDR"},
		{name: "VPC not found", cfg: driver.SubnetConfig{VPCID: "missing", CIDRBlock: "10.0.1.0/24"}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateSubnet(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ID)
				assert.Equal(t, vpc.ID, info.VPCID)
				assert.Equal(t, "READY", info.State)
			}
		})
	}
}

func TestDeleteSubnet(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		id        string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", id: subnet.ID},
		{name: "not found", id: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteSubnet(ctx, tt.id)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeSubnets(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	s1, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)
	_, err = m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.2.0/24"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all subnets", ids: nil, wantCount: 2},
		{name: "by id", ids: []string{s1.ID}, wantCount: 1},
		{name: "unknown id", ids: []string{"nope"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subnets, descErr := m.DescribeSubnets(ctx, tt.ids)
			require.NoError(t, descErr)
			assert.Len(t, subnets, tt.wantCount)
		})
	}
}

func TestCreateSecurityGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.SecurityGroupConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.SecurityGroupConfig{Name: "web-sg", VPCID: vpc.ID, Description: "Web SG"}},
		{name: "empty name", cfg: driver.SecurityGroupConfig{VPCID: vpc.ID}, wantErr: true, errSubstr: "name"},
		{name: "empty VPC", cfg: driver.SecurityGroupConfig{Name: "sg"}, wantErr: true, errSubstr: "VPC ID"},
		{name: "VPC not found", cfg: driver.SecurityGroupConfig{Name: "sg", VPCID: "missing"}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateSecurityGroup(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ID)
				assert.Equal(t, "web-sg", info.Name)
				assert.Equal(t, vpc.ID, info.VPCID)
			}
		})
	}
}

func TestDeleteSecurityGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "sg1", VPCID: vpc.ID})
	require.NoError(t, err)

	tests := []struct {
		name      string
		id        string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", id: sg.ID},
		{name: "not found", id: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteSecurityGroup(ctx, tt.id)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeSecurityGroups(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	sg1, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "sg1", VPCID: vpc.ID})
	require.NoError(t, err)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all", ids: nil, wantCount: 1},
		{name: "by id", ids: []string{sg1.ID}, wantCount: 1},
		{name: "unknown id", ids: []string{"nope"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sgs, descErr := m.DescribeSecurityGroups(ctx, tt.ids)
			require.NoError(t, descErr)
			assert.Len(t, sgs, tt.wantCount)
		})
	}
}

func TestAddSecurityGroupRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "sg1", VPCID: vpc.ID})
	require.NoError(t, err)

	ingressRule := driver.SecurityRule{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "0.0.0.0/0"}
	egressRule := driver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0"}

	t.Run("add ingress", func(t *testing.T) {
		require.NoError(t, m.AddIngressRule(ctx, sg.ID, ingressRule))
		sgs, descErr := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		require.NoError(t, descErr)
		require.Len(t, sgs, 1)
		require.Len(t, sgs[0].IngressRules, 1)
		assert.Equal(t, 80, sgs[0].IngressRules[0].FromPort)
	})

	t.Run("add egress", func(t *testing.T) {
		require.NoError(t, m.AddEgressRule(ctx, sg.ID, egressRule))
		sgs, descErr := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		require.NoError(t, descErr)
		require.Len(t, sgs[0].EgressRules, 1)
		assert.Equal(t, 443, sgs[0].EgressRules[0].FromPort)
	})

	t.Run("add ingress to missing SG", func(t *testing.T) {
		err := m.AddIngressRule(ctx, "missing", ingressRule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("add egress to missing SG", func(t *testing.T) {
		err := m.AddEgressRule(ctx, "missing", egressRule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("remove ingress", func(t *testing.T) {
		require.NoError(t, m.RemoveIngressRule(ctx, sg.ID, ingressRule))
		sgs, descErr := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		require.NoError(t, descErr)
		assert.Empty(t, sgs[0].IngressRules)
	})

	t.Run("remove egress", func(t *testing.T) {
		require.NoError(t, m.RemoveEgressRule(ctx, sg.ID, egressRule))
		sgs, descErr := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		require.NoError(t, descErr)
		assert.Empty(t, sgs[0].EgressRules)
	})

	t.Run("remove ingress not found", func(t *testing.T) {
		err := m.RemoveIngressRule(ctx, sg.ID, ingressRule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("remove egress not found", func(t *testing.T) {
		err := m.RemoveEgressRule(ctx, sg.ID, egressRule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("remove from missing SG", func(t *testing.T) {
		err := m.RemoveIngressRule(ctx, "missing", ingressRule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestCreatePeeringConnection(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	vpc2, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})
	require.NoError(t, err)
	vpc3, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.PeeringConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg:  driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID, Tags: map[string]string{"env": "test"}},
		},
		{
			name:      "missing requester",
			cfg:       driver.PeeringConfig{RequesterVPC: "", AccepterVPC: vpc2.ID},
			wantErr:   true,
			errSubstr: "required",
		},
		{
			name:      "requester not found",
			cfg:       driver.PeeringConfig{RequesterVPC: "missing", AccepterVPC: vpc2.ID},
			wantErr:   true,
			errSubstr: "not found",
		},
		{
			name:      "accepter not found",
			cfg:       driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: "missing"},
			wantErr:   true,
			errSubstr: "not found",
		},
		{
			name:      "overlapping CIDRs",
			cfg:       driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: vpc3.ID},
			wantErr:   true,
			errSubstr: "overlap",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peering, peerErr := m.CreatePeeringConnection(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, peerErr)
				assert.Contains(t, peerErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, peerErr)
				assert.NotEmpty(t, peering.ID)
				assert.Equal(t, "pending-acceptance", peering.Status)
				assert.Equal(t, "test", peering.Tags["env"])
			}
		})
	}
}

func TestAcceptPeeringConnection(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	vpc2, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})
	require.NoError(t, err)

	peering, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID,
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		id        string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", id: peering.ID},
		{name: "already accepted", id: peering.ID, wantErr: true, errSubstr: "expected"},
		{name: "not found", id: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acceptErr := m.AcceptPeeringConnection(ctx, tt.id)
			switch {
			case tt.wantErr:
				require.Error(t, acceptErr)
				assert.Contains(t, acceptErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, acceptErr)
				// Verify status is now active
				peers, descErr := m.DescribePeeringConnections(ctx, []string{tt.id})
				require.NoError(t, descErr)
				require.Len(t, peers, 1)
				assert.Equal(t, "active", peers[0].Status)
			}
		})
	}
}

func TestCreateNATGateway(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{
		VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24",
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.NATGatewayConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg:  driver.NATGatewayConfig{SubnetID: subnet.ID, Tags: map[string]string{"env": "test"}},
		},
		{
			name:      "empty subnet",
			cfg:       driver.NATGatewayConfig{SubnetID: ""},
			wantErr:   true,
			errSubstr: "required",
		},
		{
			name:      "subnet not found",
			cfg:       driver.NATGatewayConfig{SubnetID: "missing"},
			wantErr:   true,
			errSubstr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nat, natErr := m.CreateNATGateway(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, natErr)
				assert.Contains(t, natErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, natErr)
				assert.NotEmpty(t, nat.ID)
				assert.Equal(t, "available", nat.State)
				assert.NotEmpty(t, nat.PublicIP)
				assert.Equal(t, subnet.ID, nat.SubnetID)
			}
		})
	}

	t.Run("delete NAT gateway", func(t *testing.T) {
		nats, descErr := m.DescribeNATGateways(ctx, nil)
		require.NoError(t, descErr)
		require.NotEmpty(t, nats)

		require.NoError(t, m.DeleteNATGateway(ctx, nats[0].ID))

		natsAfter, descErr := m.DescribeNATGateways(ctx, nil)
		require.NoError(t, descErr)
		assert.Empty(t, natsAfter)
	})

	t.Run("delete not found", func(t *testing.T) {
		delErr := m.DeleteNATGateway(ctx, "missing")
		require.Error(t, delErr)
		assert.Contains(t, delErr.Error(), "not found")
	})
}

func TestCreateFlowLog(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.FlowLogConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "VPC flow log",
			cfg:  driver.FlowLogConfig{ResourceID: vpc.ID, ResourceType: "VPC", TrafficType: "ALL"},
		},
		{
			name:      "empty resource ID",
			cfg:       driver.FlowLogConfig{ResourceID: "", ResourceType: "VPC"},
			wantErr:   true,
			errSubstr: "required",
		},
		{
			name:      "resource not found",
			cfg:       driver.FlowLogConfig{ResourceID: "missing", ResourceType: "VPC"},
			wantErr:   true,
			errSubstr: "not found",
		},
		{
			name:      "unsupported resource type",
			cfg:       driver.FlowLogConfig{ResourceID: vpc.ID, ResourceType: "Unknown"},
			wantErr:   true,
			errSubstr: "unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fl, flErr := m.CreateFlowLog(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, flErr)
				assert.Contains(t, flErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, flErr)
				assert.NotEmpty(t, fl.ID)
				assert.Equal(t, "ACTIVE", fl.Status)
				assert.Equal(t, "ALL", fl.TrafficType)
			}
		})
	}

	t.Run("get flow log records", func(t *testing.T) {
		fls, descErr := m.DescribeFlowLogs(ctx, nil)
		require.NoError(t, descErr)
		require.NotEmpty(t, fls)

		records, recErr := m.GetFlowLogRecords(ctx, fls[0].ID, 5)
		require.NoError(t, recErr)
		assert.Len(t, records, 5)
		assert.Equal(t, "tcp", records[0].Protocol)
	})

	t.Run("delete flow log", func(t *testing.T) {
		fls, descErr := m.DescribeFlowLogs(ctx, nil)
		require.NoError(t, descErr)
		require.NotEmpty(t, fls)

		require.NoError(t, m.DeleteFlowLog(ctx, fls[0].ID))
	})

	t.Run("delete flow log not found", func(t *testing.T) {
		delErr := m.DeleteFlowLog(ctx, "missing")
		require.Error(t, delErr)
		assert.Contains(t, delErr.Error(), "not found")
	})
}

func TestCreateRouteTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.RouteTableConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg:  driver.RouteTableConfig{VPCID: vpc.ID, Tags: map[string]string{"env": "test"}},
		},
		{
			name:      "empty VPC ID",
			cfg:       driver.RouteTableConfig{VPCID: ""},
			wantErr:   true,
			errSubstr: "required",
		},
		{
			name:      "VPC not found",
			cfg:       driver.RouteTableConfig{VPCID: "missing"},
			wantErr:   true,
			errSubstr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, rtErr := m.CreateRouteTable(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, rtErr)
				assert.Contains(t, rtErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, rtErr)
				assert.NotEmpty(t, rt.ID)
				assert.Equal(t, vpc.ID, rt.VPCID)
				// Default local route should be present
				require.Len(t, rt.Routes, 1)
				assert.Equal(t, "10.0.0.0/16", rt.Routes[0].DestinationCIDR)
				assert.Equal(t, "local", rt.Routes[0].TargetType)
			}
		})
	}
}

func TestCreateRoute(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpc.ID})
	require.NoError(t, err)

	tests := []struct {
		name      string
		rtID      string
		destCIDR  string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", rtID: rt.ID, destCIDR: "0.0.0.0/0"},
		{name: "duplicate route", rtID: rt.ID, destCIDR: "0.0.0.0/0", wantErr: true, errSubstr: "already exists"},
		{name: "route table not found", rtID: "missing", destCIDR: "1.2.3.0/24", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routeErr := m.CreateRoute(ctx, tt.rtID, tt.destCIDR, "igw-123", "internet-gateway")
			switch {
			case tt.wantErr:
				require.Error(t, routeErr)
				assert.Contains(t, routeErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, routeErr)

				rts, descErr := m.DescribeRouteTables(ctx, []string{tt.rtID})
				require.NoError(t, descErr)
				require.Len(t, rts, 1)
				assert.Len(t, rts[0].Routes, 2) // local + new route
			}
		})
	}

	t.Run("delete route", func(t *testing.T) {
		require.NoError(t, m.DeleteRoute(ctx, rt.ID, "0.0.0.0/0"))

		rts, descErr := m.DescribeRouteTables(ctx, []string{rt.ID})
		require.NoError(t, descErr)
		assert.Len(t, rts[0].Routes, 1) // only local route remains
	})

	t.Run("delete route not found", func(t *testing.T) {
		delErr := m.DeleteRoute(ctx, rt.ID, "9.9.9.0/24")
		require.Error(t, delErr)
		assert.Contains(t, delErr.Error(), "not found")
	})
}

func TestCreateNetworkACL(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		vpcID     string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", vpcID: vpc.ID},
		{name: "empty VPC", vpcID: "", wantErr: true, errSubstr: "required"},
		{name: "VPC not found", vpcID: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acl, aclErr := m.CreateNetworkACL(ctx, tt.vpcID, map[string]string{"env": "test"})
			switch {
			case tt.wantErr:
				require.Error(t, aclErr)
				assert.Contains(t, aclErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, aclErr)
				assert.NotEmpty(t, acl.ID)
				assert.Equal(t, vpc.ID, acl.VPCID)
				// Should have 2 default rules (ingress + egress)
				assert.Len(t, acl.Rules, 2)
			}
		})
	}
}

func TestAddNetworkACLRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	acl, err := m.CreateNetworkACL(ctx, vpc.ID, nil)
	require.NoError(t, err)

	t.Run("add ingress rule", func(t *testing.T) {
		rule := &driver.NetworkACLRule{
			RuleNumber: 50,
			Protocol:   "tcp",
			Action:     "allow",
			CIDR:       "10.0.0.0/8",
			FromPort:   80,
			ToPort:     80,
			Egress:     false,
		}
		require.NoError(t, m.AddNetworkACLRule(ctx, acl.ID, rule))

		acls, descErr := m.DescribeNetworkACLs(ctx, []string{acl.ID})
		require.NoError(t, descErr)
		require.Len(t, acls, 1)
		assert.Len(t, acls[0].Rules, 3) // 2 default + 1 new
		// Rules should be sorted by rule number (50 first)
		assert.Equal(t, 50, acls[0].Rules[0].RuleNumber)
	})

	t.Run("add egress rule", func(t *testing.T) {
		rule := &driver.NetworkACLRule{
			RuleNumber: 200,
			Protocol:   "tcp",
			Action:     "deny",
			CIDR:       "0.0.0.0/0",
			FromPort:   443,
			ToPort:     443,
			Egress:     true,
		}
		require.NoError(t, m.AddNetworkACLRule(ctx, acl.ID, rule))

		acls, descErr := m.DescribeNetworkACLs(ctx, []string{acl.ID})
		require.NoError(t, descErr)
		assert.Len(t, acls[0].Rules, 4)
	})

	t.Run("remove rule", func(t *testing.T) {
		require.NoError(t, m.RemoveNetworkACLRule(ctx, acl.ID, 50, false))

		acls, descErr := m.DescribeNetworkACLs(ctx, []string{acl.ID})
		require.NoError(t, descErr)
		assert.Len(t, acls[0].Rules, 3)
	})

	t.Run("remove rule not found", func(t *testing.T) {
		rmErr := m.RemoveNetworkACLRule(ctx, acl.ID, 999, false)
		require.Error(t, rmErr)
		assert.Contains(t, rmErr.Error(), "not found")
	})

	t.Run("ACL not found", func(t *testing.T) {
		addErr := m.AddNetworkACLRule(ctx, "missing", &driver.NetworkACLRule{RuleNumber: 10})
		require.Error(t, addErr)
		assert.Contains(t, addErr.Error(), "not found")
	})

	t.Run("remove rule ACL not found", func(t *testing.T) {
		rmErr := m.RemoveNetworkACLRule(ctx, "missing", 50, false)
		require.Error(t, rmErr)
		assert.Contains(t, rmErr.Error(), "not found")
	})
}

func TestRejectPeeringConnection(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	vpc2, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})
	require.NoError(t, err)

	peering, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID,
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		require.NoError(t, m.RejectPeeringConnection(ctx, peering.ID))

		peers, descErr := m.DescribePeeringConnections(ctx, []string{peering.ID})
		require.NoError(t, descErr)
		require.Len(t, peers, 1)
		assert.Equal(t, "rejected", peers[0].Status)
	})

	t.Run("already rejected", func(t *testing.T) {
		err := m.RejectPeeringConnection(ctx, peering.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected")
	})

	t.Run("not found", func(t *testing.T) {
		err := m.RejectPeeringConnection(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeletePeeringConnection(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	vpc2, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})
	require.NoError(t, err)

	peering, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID,
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		require.NoError(t, m.DeletePeeringConnection(ctx, peering.ID))

		peers, descErr := m.DescribePeeringConnections(ctx, []string{peering.ID})
		require.NoError(t, descErr)
		assert.Empty(t, peers)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeletePeeringConnection(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDescribePeeringConnectionsFiltered(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	vpc2, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})
	require.NoError(t, err)
	vpc3, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "192.168.0.0/16"})
	require.NoError(t, err)

	p1, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID})
	require.NoError(t, err)
	_, err = m.CreatePeeringConnection(ctx, driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: vpc3.ID})
	require.NoError(t, err)

	t.Run("all peerings", func(t *testing.T) {
		peers, descErr := m.DescribePeeringConnections(ctx, nil)
		require.NoError(t, descErr)
		assert.Len(t, peers, 2)
	})

	t.Run("by ID filter", func(t *testing.T) {
		peers, descErr := m.DescribePeeringConnections(ctx, []string{p1.ID})
		require.NoError(t, descErr)
		assert.Len(t, peers, 1)
		assert.Equal(t, p1.ID, peers[0].ID)
	})

	t.Run("unknown ID", func(t *testing.T) {
		peers, descErr := m.DescribePeeringConnections(ctx, []string{"nope"})
		require.NoError(t, descErr)
		assert.Empty(t, peers)
	})
}

func TestDescribeNATGatewaysWithIDFilter(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	s1, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)
	s2, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.2.0/24"})
	require.NoError(t, err)

	nat1, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: s1.ID})
	require.NoError(t, err)
	_, err = m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: s2.ID})
	require.NoError(t, err)

	t.Run("all NAT gateways", func(t *testing.T) {
		nats, descErr := m.DescribeNATGateways(ctx, nil)
		require.NoError(t, descErr)
		assert.Len(t, nats, 2)
	})

	t.Run("by ID", func(t *testing.T) {
		nats, descErr := m.DescribeNATGateways(ctx, []string{nat1.ID})
		require.NoError(t, descErr)
		assert.Len(t, nats, 1)
		assert.Equal(t, nat1.ID, nats[0].ID)
	})

	t.Run("unknown ID", func(t *testing.T) {
		nats, descErr := m.DescribeNATGateways(ctx, []string{"nope"})
		require.NoError(t, descErr)
		assert.Empty(t, nats)
	})
}

func TestDescribeFlowLogsWithIDFilter(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	fl1, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: vpc.ID, ResourceType: "VPC", TrafficType: "ALL"})
	require.NoError(t, err)
	fl2, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: vpc.ID, ResourceType: "VPC", TrafficType: "ACCEPT"})
	require.NoError(t, err)

	t.Run("all flow logs", func(t *testing.T) {
		fls, descErr := m.DescribeFlowLogs(ctx, nil)
		require.NoError(t, descErr)
		assert.Len(t, fls, 2)
	})

	t.Run("by ID", func(t *testing.T) {
		fls, descErr := m.DescribeFlowLogs(ctx, []string{fl1.ID})
		require.NoError(t, descErr)
		assert.Len(t, fls, 1)
		assert.Equal(t, fl1.ID, fls[0].ID)
	})

	t.Run("unknown ID", func(t *testing.T) {
		fls, descErr := m.DescribeFlowLogs(ctx, []string{"nope"})
		require.NoError(t, descErr)
		assert.Empty(t, fls)
	})

	t.Run("flow log records for ACCEPT only", func(t *testing.T) {
		records, recErr := m.GetFlowLogRecords(ctx, fl2.ID, 4)
		require.NoError(t, recErr)
		assert.Len(t, records, 4)

		for _, rec := range records {
			assert.Equal(t, "ACCEPT", rec.Action)
		}
	})
}

func TestDescribeRouteTablesWithIDFilter(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	rt1, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpc.ID})
	require.NoError(t, err)
	_, err = m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpc.ID})
	require.NoError(t, err)

	t.Run("all route tables", func(t *testing.T) {
		rts, descErr := m.DescribeRouteTables(ctx, nil)
		require.NoError(t, descErr)
		assert.Len(t, rts, 2)
	})

	t.Run("by ID", func(t *testing.T) {
		rts, descErr := m.DescribeRouteTables(ctx, []string{rt1.ID})
		require.NoError(t, descErr)
		assert.Len(t, rts, 1)
		assert.Equal(t, rt1.ID, rts[0].ID)
	})

	t.Run("unknown ID", func(t *testing.T) {
		rts, descErr := m.DescribeRouteTables(ctx, []string{"nope"})
		require.NoError(t, descErr)
		assert.Empty(t, rts)
	})
}

func TestDescribeNetworkACLsWithIDFilter(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	acl1, err := m.CreateNetworkACL(ctx, vpc.ID, nil)
	require.NoError(t, err)
	_, err = m.CreateNetworkACL(ctx, vpc.ID, nil)
	require.NoError(t, err)

	t.Run("all ACLs", func(t *testing.T) {
		acls, descErr := m.DescribeNetworkACLs(ctx, nil)
		require.NoError(t, descErr)
		assert.Len(t, acls, 2)
	})

	t.Run("by ID", func(t *testing.T) {
		acls, descErr := m.DescribeNetworkACLs(ctx, []string{acl1.ID})
		require.NoError(t, descErr)
		assert.Len(t, acls, 1)
		assert.Equal(t, acl1.ID, acls[0].ID)
	})

	t.Run("unknown ID", func(t *testing.T) {
		acls, descErr := m.DescribeNetworkACLs(ctx, []string{"nope"})
		require.NoError(t, descErr)
		assert.Empty(t, acls)
	})
}

func TestDeleteRouteTableNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	err := m.DeleteRouteTable(ctx, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteRouteTableSuccess(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpc.ID})
	require.NoError(t, err)

	require.NoError(t, m.DeleteRouteTable(ctx, rt.ID))

	rts, descErr := m.DescribeRouteTables(ctx, []string{rt.ID})
	require.NoError(t, descErr)
	assert.Empty(t, rts)
}

func TestDeleteNetworkACLNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	err := m.DeleteNetworkACL(ctx, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteNetworkACLSuccess(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	acl, err := m.CreateNetworkACL(ctx, vpc.ID, nil)
	require.NoError(t, err)

	require.NoError(t, m.DeleteNetworkACL(ctx, acl.ID))

	acls, descErr := m.DescribeNetworkACLs(ctx, []string{acl.ID})
	require.NoError(t, descErr)
	assert.Empty(t, acls)
}

func TestGetFlowLogRecordsVariousLimits(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	fl, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: vpc.ID, ResourceType: "VPC", TrafficType: "ALL"})
	require.NoError(t, err)

	t.Run("default limit when zero", func(t *testing.T) {
		records, recErr := m.GetFlowLogRecords(ctx, fl.ID, 0)
		require.NoError(t, recErr)
		assert.Len(t, records, 10) // defaultFlowLogRecordLimit
	})

	t.Run("custom limit", func(t *testing.T) {
		records, recErr := m.GetFlowLogRecords(ctx, fl.ID, 3)
		require.NoError(t, recErr)
		assert.Len(t, records, 3)
	})

	t.Run("large limit", func(t *testing.T) {
		records, recErr := m.GetFlowLogRecords(ctx, fl.ID, 20)
		require.NoError(t, recErr)
		assert.Len(t, records, 20)
	})

	t.Run("not found", func(t *testing.T) {
		_, recErr := m.GetFlowLogRecords(ctx, "missing", 5)
		require.Error(t, recErr)
		assert.Contains(t, recErr.Error(), "not found")
	})
}

func TestFlowLogSubnetResource(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	fl, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: subnet.ID, ResourceType: "Subnet"})
	require.NoError(t, err)
	assert.NotEmpty(t, fl.ID)
	assert.Equal(t, "ACTIVE", fl.Status)
}

func TestFlowLogRejectTrafficType(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	fl, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: vpc.ID, ResourceType: "VPC", TrafficType: "REJECT"})
	require.NoError(t, err)

	records, recErr := m.GetFlowLogRecords(ctx, fl.ID, 4)
	require.NoError(t, recErr)
	assert.Len(t, records, 4)

	for _, rec := range records {
		assert.Equal(t, "REJECT", rec.Action)
	}
}

// --- Internet Gateway tests ---

func TestInternetGateway(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	t.Run("create IGW", func(t *testing.T) {
		igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{
			Tags: map[string]string{"env": "test"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, igw.ID)
		assert.Equal(t, "detached", igw.State)
	})

	t.Run("describe all IGWs", func(t *testing.T) {
		igws, err := m.DescribeInternetGateways(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, igws, 1)
		assert.Equal(t, "detached", igws[0].State)
	})

	t.Run("attach IGW to VPC", func(t *testing.T) {
		igws, _ := m.DescribeInternetGateways(ctx, nil)
		err := m.AttachInternetGateway(ctx, igws[0].ID, vpc.ID)
		require.NoError(t, err)

		igws, _ = m.DescribeInternetGateways(ctx, []string{igws[0].ID})
		require.Len(t, igws, 1)
		assert.Equal(t, "attached", igws[0].State)
		assert.Equal(t, vpc.ID, igws[0].VpcID)
	})

	t.Run("detach IGW", func(t *testing.T) {
		igws, _ := m.DescribeInternetGateways(ctx, nil)
		err := m.DetachInternetGateway(ctx, igws[0].ID, vpc.ID)
		require.NoError(t, err)

		igws, _ = m.DescribeInternetGateways(ctx, []string{igws[0].ID})
		require.Len(t, igws, 1)
		assert.Equal(t, "detached", igws[0].State)
	})

	t.Run("delete IGW", func(t *testing.T) {
		igws, _ := m.DescribeInternetGateways(ctx, nil)
		err := m.DeleteInternetGateway(ctx, igws[0].ID)
		require.NoError(t, err)

		igws, _ = m.DescribeInternetGateways(ctx, nil)
		assert.Empty(t, igws)
	})

	t.Run("delete nonexistent IGW", func(t *testing.T) {
		err := m.DeleteInternetGateway(ctx, "igw-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("attach to nonexistent VPC", func(t *testing.T) {
		igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
		require.NoError(t, err)

		err = m.AttachInternetGateway(ctx, igw.ID, "vpc-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("attach nonexistent IGW", func(t *testing.T) {
		err := m.AttachInternetGateway(ctx, "igw-missing", vpc.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("detach nonexistent IGW", func(t *testing.T) {
		err := m.DetachInternetGateway(ctx, "igw-missing", vpc.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("describe by ID", func(t *testing.T) {
		igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
		require.NoError(t, err)

		igws, err := m.DescribeInternetGateways(ctx, []string{igw.ID})
		require.NoError(t, err)
		require.Len(t, igws, 1)
		assert.Equal(t, igw.ID, igws[0].ID)
	})

	t.Run("describe nonexistent ID", func(t *testing.T) {
		igws, err := m.DescribeInternetGateways(ctx, []string{"igw-missing"})
		require.NoError(t, err)
		assert.Empty(t, igws)
	})
}

// --- Elastic IP tests ---

func TestElasticIP(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("allocate address", func(t *testing.T) {
		eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{
			Tags: map[string]string{"env": "test"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, eip.AllocationID)
		assert.NotEmpty(t, eip.PublicIP)
	})

	t.Run("describe addresses", func(t *testing.T) {
		eips, err := m.DescribeAddresses(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, eips, 1)
	})

	t.Run("associate address", func(t *testing.T) {
		eips, _ := m.DescribeAddresses(ctx, nil)
		assocID, err := m.AssociateAddress(ctx, eips[0].AllocationID, "i-12345")
		require.NoError(t, err)
		assert.NotEmpty(t, assocID)

		eips, _ = m.DescribeAddresses(ctx, []string{eips[0].AllocationID})
		require.Len(t, eips, 1)
		assert.Equal(t, assocID, eips[0].AssociationID)
		assert.Equal(t, "i-12345", eips[0].InstanceID)
	})

	t.Run("disassociate address", func(t *testing.T) {
		eips, _ := m.DescribeAddresses(ctx, nil)
		err := m.DisassociateAddress(ctx, eips[0].AssociationID)
		require.NoError(t, err)

		eips, _ = m.DescribeAddresses(ctx, nil)
		assert.Equal(t, "", eips[0].AssociationID)
		assert.Equal(t, "", eips[0].InstanceID)
	})

	t.Run("release address", func(t *testing.T) {
		eips, _ := m.DescribeAddresses(ctx, nil)
		err := m.ReleaseAddress(ctx, eips[0].AllocationID)
		require.NoError(t, err)

		eips, _ = m.DescribeAddresses(ctx, nil)
		assert.Empty(t, eips)
	})

	t.Run("release nonexistent", func(t *testing.T) {
		err := m.ReleaseAddress(ctx, "eipalloc-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("associate nonexistent allocation", func(t *testing.T) {
		_, err := m.AssociateAddress(ctx, "eipalloc-missing", "i-12345")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("disassociate nonexistent", func(t *testing.T) {
		err := m.DisassociateAddress(ctx, "eipassoc-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("describe by ID", func(t *testing.T) {
		eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
		require.NoError(t, err)

		eips, err := m.DescribeAddresses(ctx, []string{eip.AllocationID})
		require.NoError(t, err)
		require.Len(t, eips, 1)
		assert.Equal(t, eip.AllocationID, eips[0].AllocationID)
	})

	t.Run("describe nonexistent ID", func(t *testing.T) {
		eips, err := m.DescribeAddresses(ctx, []string{"eipalloc-missing"})
		require.NoError(t, err)
		assert.Empty(t, eips)
	})
}

// --- Route Table Association tests ---

func TestRouteTableAssociation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpc.ID})
	require.NoError(t, err)

	t.Run("associate route table", func(t *testing.T) {
		assoc, err := m.AssociateRouteTable(ctx, rt.ID, subnet.ID)
		require.NoError(t, err)
		assert.NotEmpty(t, assoc.ID)
		assert.Equal(t, rt.ID, assoc.RouteTableID)
		assert.Equal(t, subnet.ID, assoc.SubnetID)
	})

	t.Run("disassociate route table", func(t *testing.T) {
		assoc, err := m.AssociateRouteTable(ctx, rt.ID, subnet.ID)
		require.NoError(t, err)

		err = m.DisassociateRouteTable(ctx, assoc.ID)
		require.NoError(t, err)
	})

	t.Run("associate nonexistent route table", func(t *testing.T) {
		_, err := m.AssociateRouteTable(ctx, "rt-missing", subnet.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("disassociate nonexistent", func(t *testing.T) {
		err := m.DisassociateRouteTable(ctx, "rtbassoc-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}
