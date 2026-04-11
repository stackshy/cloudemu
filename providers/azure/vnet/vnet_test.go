package vnet

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
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"))

	return New(opts)
}

func createTestVPC(t *testing.T, m *Mock) string {
	t.Helper()

	ctx := context.Background()
	info, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "test"}})
	require.NoError(t, err)

	return info.ID
}

func TestCreateVPC(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.VPCConfig
		wantErr bool
		errMsg  string
	}{
		{name: "success", cfg: driver.VPCConfig{CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "prod"}}},
		{name: "empty CIDR", cfg: driver.VPCConfig{CIDRBlock: ""}, wantErr: true, errMsg: "CIDR block is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateVPC(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ID)
				assert.Equal(t, "10.0.0.0/16", info.CIDRBlock)
				assert.Equal(t, "available", info.State)
				assert.Equal(t, "prod", info.Tags["env"])
			}
		})
	}
}

func TestDeleteVPC(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	tests := []struct {
		name    string
		id      string
		wantErr bool
		errMsg  string
	}{
		{name: "success", id: vpcID},
		{name: "not found", id: "vnet-missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteVPC(ctx, tt.id)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeVPCs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	id1 := createTestVPC(t, m)
	id2 := createTestVPC(t, m)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all VPCs", ids: nil, wantCount: 2},
		{name: "by ID", ids: []string{id1}, wantCount: 1},
		{name: "multiple IDs", ids: []string{id1, id2}, wantCount: 2},
		{name: "nonexistent ID", ids: []string{"vnet-missing"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpcs, err := m.DescribeVPCs(ctx, tt.ids)
			require.NoError(t, err)
			assert.Len(t, vpcs, tt.wantCount)
		})
	}
}

func TestCreateSubnet(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	tests := []struct {
		name    string
		cfg     driver.SubnetConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "eastus-1"},
		},
		{name: "empty VPC ID", cfg: driver.SubnetConfig{CIDRBlock: "10.0.1.0/24"}, wantErr: true, errMsg: "VNet ID is required"},
		{name: "empty CIDR", cfg: driver.SubnetConfig{VPCID: vpcID, CIDRBlock: ""}, wantErr: true, errMsg: "CIDR block is required"},
		{name: "VPC not found", cfg: driver.SubnetConfig{VPCID: "vnet-missing", CIDRBlock: "10.0.1.0/24"}, wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateSubnet(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ID)
				assert.Equal(t, vpcID, info.VPCID)
				assert.Equal(t, "10.0.1.0/24", info.CIDRBlock)
				assert.Equal(t, "available", info.State)
			}
		})
	}
}

func TestDeleteSubnet(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	info, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	tests := []struct {
		name    string
		id      string
		wantErr bool
		errMsg  string
	}{
		{name: "success", id: info.ID},
		{name: "not found", id: "subnet-missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteSubnet(ctx, tt.id)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeSubnets(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	s1, _ := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24"})
	_, _ = m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.2.0/24"})

	t.Run("all subnets", func(t *testing.T) {
		subnets, err := m.DescribeSubnets(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, subnets, 2)
	})

	t.Run("by ID", func(t *testing.T) {
		subnets, err := m.DescribeSubnets(ctx, []string{s1.ID})
		require.NoError(t, err)
		assert.Len(t, subnets, 1)
	})
}

func TestCreateSecurityGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	tests := []struct {
		name    string
		cfg     driver.SecurityGroupConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.SecurityGroupConfig{Name: "web-nsg", Description: "Web NSG", VPCID: vpcID},
		},
		{name: "empty name", cfg: driver.SecurityGroupConfig{VPCID: vpcID}, wantErr: true, errMsg: "security group name is required"},
		{name: "empty VPC", cfg: driver.SecurityGroupConfig{Name: "nsg"}, wantErr: true, errMsg: "VNet ID is required"},
		{name: "VPC not found", cfg: driver.SecurityGroupConfig{Name: "nsg", VPCID: "vnet-missing"}, wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateSecurityGroup(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ID)
				assert.Equal(t, "web-nsg", info.Name)
				assert.Equal(t, "Web NSG", info.Description)
			}
		})
	}
}

func TestDeleteSecurityGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg", VPCID: vpcID})
	require.NoError(t, err)

	tests := []struct {
		name    string
		id      string
		wantErr bool
		errMsg  string
	}{
		{name: "success", id: sg.ID},
		{name: "not found", id: "nsg-missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteSecurityGroup(ctx, tt.id)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeSecurityGroups(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	sg1, _ := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg1", VPCID: vpcID})
	_, _ = m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg2", VPCID: vpcID})

	t.Run("all", func(t *testing.T) {
		sgs, err := m.DescribeSecurityGroups(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, sgs, 2)
	})

	t.Run("by ID", func(t *testing.T) {
		sgs, err := m.DescribeSecurityGroups(ctx, []string{sg1.ID})
		require.NoError(t, err)
		assert.Len(t, sgs, 1)
	})
}

func TestAddSecurityGroupRules(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	sg, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg", VPCID: vpcID})
	require.NoError(t, err)

	ingressRule := driver.SecurityRule{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "0.0.0.0/0"}
	egressRule := driver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0"}

	t.Run("add ingress rule", func(t *testing.T) {
		err := m.AddIngressRule(ctx, sg.ID, ingressRule)
		require.NoError(t, err)

		sgs, _ := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		require.Len(t, sgs, 1)
		assert.Len(t, sgs[0].IngressRules, 1)
		assert.Equal(t, "tcp", sgs[0].IngressRules[0].Protocol)
		assert.Equal(t, 80, sgs[0].IngressRules[0].FromPort)
	})

	t.Run("add egress rule", func(t *testing.T) {
		err := m.AddEgressRule(ctx, sg.ID, egressRule)
		require.NoError(t, err)

		sgs, _ := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		require.Len(t, sgs, 1)
		assert.Len(t, sgs[0].EgressRules, 1)
	})

	t.Run("remove ingress rule", func(t *testing.T) {
		err := m.RemoveIngressRule(ctx, sg.ID, ingressRule)
		require.NoError(t, err)

		sgs, _ := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		require.Len(t, sgs, 1)
		assert.Empty(t, sgs[0].IngressRules)
	})

	t.Run("remove egress rule", func(t *testing.T) {
		err := m.RemoveEgressRule(ctx, sg.ID, egressRule)
		require.NoError(t, err)

		sgs, _ := m.DescribeSecurityGroups(ctx, []string{sg.ID})
		require.Len(t, sgs, 1)
		assert.Empty(t, sgs[0].EgressRules)
	})

	t.Run("add rule to nonexistent SG", func(t *testing.T) {
		err := m.AddIngressRule(ctx, "nsg-missing", ingressRule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("remove nonexistent ingress rule", func(t *testing.T) {
		err := m.RemoveIngressRule(ctx, sg.ID, driver.SecurityRule{Protocol: "udp", FromPort: 53, ToPort: 53, CIDR: "0.0.0.0/0"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("remove nonexistent egress rule", func(t *testing.T) {
		err := m.RemoveEgressRule(ctx, sg.ID, driver.SecurityRule{Protocol: "udp", FromPort: 53, ToPort: 53, CIDR: "0.0.0.0/0"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("add egress to nonexistent SG", func(t *testing.T) {
		err := m.AddEgressRule(ctx, "nsg-missing", egressRule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("remove ingress from nonexistent SG", func(t *testing.T) {
		err := m.RemoveIngressRule(ctx, "nsg-missing", ingressRule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("remove egress from nonexistent SG", func(t *testing.T) {
		err := m.RemoveEgressRule(ctx, "nsg-missing", egressRule)
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

	tests := []struct {
		name    string
		cfg     driver.PeeringConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID, Tags: map[string]string{"env": "test"}},
		},
		{
			name:    "missing requester",
			cfg:     driver.PeeringConfig{AccepterVPC: vpc2.ID},
			wantErr: true, errMsg: "both requester and accepter",
		},
		{
			name:    "requester not found",
			cfg:     driver.PeeringConfig{RequesterVPC: "vnet-missing", AccepterVPC: vpc2.ID},
			wantErr: true, errMsg: "not found",
		},
		{
			name:    "accepter not found",
			cfg:     driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: "vnet-missing"},
			wantErr: true, errMsg: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peering, err := m.CreatePeeringConnection(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, peering.ID)
				assert.Equal(t, "pending-acceptance", peering.Status)
				assert.Equal(t, vpc1.ID, peering.RequesterVPC)
				assert.Equal(t, vpc2.ID, peering.AccepterVPC)
			}
		})
	}
}

func TestAcceptPeeringConnection(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	vpc2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})

	peering, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID,
	})
	require.NoError(t, err)

	t.Run("accept pending peering", func(t *testing.T) {
		err := m.AcceptPeeringConnection(ctx, peering.ID)
		require.NoError(t, err)

		peers, _ := m.DescribePeeringConnections(ctx, []string{peering.ID})
		require.Len(t, peers, 1)
		assert.Equal(t, "active", peers[0].Status)
	})

	t.Run("accept already active peering fails", func(t *testing.T) {
		err := m.AcceptPeeringConnection(ctx, peering.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected")
	})

	t.Run("not found", func(t *testing.T) {
		err := m.AcceptPeeringConnection(ctx, "peering-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestCreateNATGateway(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	tests := []struct {
		name    string
		cfg     driver.NATGatewayConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.NATGatewayConfig{SubnetID: subnet.ID, Tags: map[string]string{"env": "test"}},
		},
		{
			name:    "empty subnet",
			cfg:     driver.NATGatewayConfig{},
			wantErr: true, errMsg: "subnet ID is required",
		},
		{
			name:    "subnet not found",
			cfg:     driver.NATGatewayConfig{SubnetID: "subnet-missing"},
			wantErr: true, errMsg: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nat, err := m.CreateNATGateway(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, nat.ID)
				assert.Equal(t, subnet.ID, nat.SubnetID)
				assert.Equal(t, vpcID, nat.VPCID)
				assert.Equal(t, "available", nat.State)
				assert.NotEmpty(t, nat.PublicIP)
			}
		})
	}
}

func TestCreateFlowLog(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	tests := []struct {
		name    string
		cfg     driver.FlowLogConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "VPC flow log",
			cfg:  driver.FlowLogConfig{ResourceID: vpcID, ResourceType: "VPC", TrafficType: "ALL"},
		},
		{
			name:    "empty resource ID",
			cfg:     driver.FlowLogConfig{ResourceType: "VPC"},
			wantErr: true, errMsg: "resource ID is required",
		},
		{
			name:    "VPC not found",
			cfg:     driver.FlowLogConfig{ResourceID: "vnet-missing", ResourceType: "VPC"},
			wantErr: true, errMsg: "not found",
		},
		{
			name:    "unsupported resource type",
			cfg:     driver.FlowLogConfig{ResourceID: vpcID, ResourceType: "Unknown"},
			wantErr: true, errMsg: "unsupported resource type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fl, err := m.CreateFlowLog(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, fl.ID)
				assert.Equal(t, vpcID, fl.ResourceID)
				assert.Equal(t, "ACTIVE", fl.Status)

				// Get records
				records, err := m.GetFlowLogRecords(ctx, fl.ID, 5)
				require.NoError(t, err)
				assert.Len(t, records, 5)
				assert.Equal(t, fl.ID, records[0].FlowLogID)
			}
		})
	}
}

func TestCreateRouteTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	tests := []struct {
		name    string
		cfg     driver.RouteTableConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.RouteTableConfig{VPCID: vpcID, Tags: map[string]string{"env": "test"}},
		},
		{
			name:    "empty VPC ID",
			cfg:     driver.RouteTableConfig{},
			wantErr: true, errMsg: "VNet ID is required",
		},
		{
			name:    "VPC not found",
			cfg:     driver.RouteTableConfig{VPCID: "vnet-missing"},
			wantErr: true, errMsg: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, err := m.CreateRouteTable(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, rt.ID)
				assert.Equal(t, vpcID, rt.VPCID)
				// Should have a local route by default
				require.Len(t, rt.Routes, 1)
				assert.Equal(t, "local", rt.Routes[0].TargetType)
			}
		})
	}
}

func TestCreateRoute(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpcID})
	require.NoError(t, err)

	t.Run("add route", func(t *testing.T) {
		err := m.CreateRoute(ctx, rt.ID, "0.0.0.0/0", "igw-123", "gateway")
		require.NoError(t, err)

		tables, _ := m.DescribeRouteTables(ctx, []string{rt.ID})
		require.Len(t, tables, 1)
		assert.Len(t, tables[0].Routes, 2) // local + new route
	})

	t.Run("duplicate route", func(t *testing.T) {
		err := m.CreateRoute(ctx, rt.ID, "0.0.0.0/0", "igw-456", "gateway")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("delete route", func(t *testing.T) {
		err := m.DeleteRoute(ctx, rt.ID, "0.0.0.0/0")
		require.NoError(t, err)

		tables, _ := m.DescribeRouteTables(ctx, []string{rt.ID})
		require.Len(t, tables, 1)
		assert.Len(t, tables[0].Routes, 1) // only local route
	})

	t.Run("route table not found", func(t *testing.T) {
		err := m.CreateRoute(ctx, "rt-missing", "0.0.0.0/0", "igw", "gateway")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestCreateNetworkACL(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	tests := []struct {
		name    string
		vpcID   string
		wantErr bool
		errMsg  string
	}{
		{name: "success", vpcID: vpcID},
		{name: "empty VPC ID", vpcID: "", wantErr: true, errMsg: "VNet ID is required"},
		{name: "VPC not found", vpcID: "vnet-missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acl, err := m.CreateNetworkACL(ctx, tt.vpcID, map[string]string{"env": "test"})

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, acl.ID)
				assert.Equal(t, vpcID, acl.VPCID)
				// Should have default allow-all rules
				assert.GreaterOrEqual(t, len(acl.Rules), 2)
			}
		})
	}
}

func TestAddNetworkACLRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	acl, err := m.CreateNetworkACL(ctx, vpcID, nil)
	require.NoError(t, err)

	t.Run("add ingress rule", func(t *testing.T) {
		rule := &driver.NetworkACLRule{
			RuleNumber: 50, Protocol: "tcp", Action: "allow",
			CIDR: "10.0.0.0/8", FromPort: 80, ToPort: 80, Egress: false,
		}
		err := m.AddNetworkACLRule(ctx, acl.ID, rule)
		require.NoError(t, err)

		acls, _ := m.DescribeNetworkACLs(ctx, []string{acl.ID})
		require.Len(t, acls, 1)
		// Should have default rules + new rule, sorted by rule number
		assert.GreaterOrEqual(t, len(acls[0].Rules), 3)
		assert.Equal(t, 50, acls[0].Rules[0].RuleNumber) // sorted first
	})

	t.Run("add egress rule", func(t *testing.T) {
		rule := &driver.NetworkACLRule{
			RuleNumber: 200, Protocol: "tcp", Action: "deny",
			CIDR: "0.0.0.0/0", FromPort: 443, ToPort: 443, Egress: true,
		}
		err := m.AddNetworkACLRule(ctx, acl.ID, rule)
		require.NoError(t, err)
	})

	t.Run("remove rule", func(t *testing.T) {
		err := m.RemoveNetworkACLRule(ctx, acl.ID, 50, false)
		require.NoError(t, err)
	})

	t.Run("ACL not found", func(t *testing.T) {
		err := m.AddNetworkACLRule(ctx, "acl-missing", &driver.NetworkACLRule{RuleNumber: 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("remove nonexistent rule", func(t *testing.T) {
		err := m.RemoveNetworkACLRule(ctx, acl.ID, 999, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeletePeeringConnection(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	vpc2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})

	peering, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID,
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		peeringID string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", peeringID: peering.ID},
		{name: "already deleted", peeringID: peering.ID, wantErr: true, errSubstr: "not found"},
		{name: "not found", peeringID: "peering-missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeletePeeringConnection(ctx, tt.peeringID)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				// After deletion it should no longer appear
				peers, _ := m.DescribePeeringConnections(ctx, []string{tt.peeringID})
				assert.Empty(t, peers)
			}
		})
	}
}

func TestRejectPeeringConnection(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	vpc2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})

	peering, err := m.CreatePeeringConnection(ctx, driver.PeeringConfig{
		RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID,
	})
	require.NoError(t, err)

	t.Run("reject pending peering", func(t *testing.T) {
		err := m.RejectPeeringConnection(ctx, peering.ID)
		require.NoError(t, err)

		peers, _ := m.DescribePeeringConnections(ctx, []string{peering.ID})
		require.Len(t, peers, 1)
		assert.Equal(t, "rejected", peers[0].Status)
	})

	t.Run("reject already rejected fails", func(t *testing.T) {
		err := m.RejectPeeringConnection(ctx, peering.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected")
	})

	t.Run("not found", func(t *testing.T) {
		err := m.RejectPeeringConnection(ctx, "peering-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDescribePeeringConnectionsWithIDs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpc1, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	vpc2, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "172.16.0.0/16"})
	vpc3, _ := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "192.168.0.0/16"})

	p1, _ := m.CreatePeeringConnection(ctx, driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: vpc2.ID})
	p2, _ := m.CreatePeeringConnection(ctx, driver.PeeringConfig{RequesterVPC: vpc1.ID, AccepterVPC: vpc3.ID})

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all peerings", ids: nil, wantCount: 2},
		{name: "by single ID", ids: []string{p1.ID}, wantCount: 1},
		{name: "by multiple IDs", ids: []string{p1.ID, p2.ID}, wantCount: 2},
		{name: "nonexistent ID", ids: []string{"peering-missing"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peers, err := m.DescribePeeringConnections(ctx, tt.ids)
			require.NoError(t, err)
			assert.Len(t, peers, tt.wantCount)
		})
	}
}

func TestDeleteNATGatewayNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	err := m.DeleteNATGateway(ctx, "natgw-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDescribeNATGatewaysWithIDs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	s1, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)
	s2, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.2.0/24"})
	require.NoError(t, err)

	nat1, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: s1.ID})
	require.NoError(t, err)
	nat2, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: s2.ID})
	require.NoError(t, err)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all NAT gateways", ids: nil, wantCount: 2},
		{name: "by single ID", ids: []string{nat1.ID}, wantCount: 1},
		{name: "by multiple IDs", ids: []string{nat1.ID, nat2.ID}, wantCount: 2},
		{name: "nonexistent ID", ids: []string{"natgw-missing"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nats, err := m.DescribeNATGateways(ctx, tt.ids)
			require.NoError(t, err)
			assert.Len(t, nats, tt.wantCount)
		})
	}
}

func TestDeleteFlowLogNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	err := m.DeleteFlowLog(ctx, "fl-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDescribeFlowLogsWithIDs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	fl1, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: vpcID, ResourceType: "VPC", TrafficType: "ALL"})
	require.NoError(t, err)
	fl2, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{ResourceID: vpcID, ResourceType: "VPC", TrafficType: "ACCEPT"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all flow logs", ids: nil, wantCount: 2},
		{name: "by single ID", ids: []string{fl1.ID}, wantCount: 1},
		{name: "by multiple IDs", ids: []string{fl1.ID, fl2.ID}, wantCount: 2},
		{name: "nonexistent ID", ids: []string{"fl-missing"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fls, err := m.DescribeFlowLogs(ctx, tt.ids)
			require.NoError(t, err)
			assert.Len(t, fls, tt.wantCount)
		})
	}
}

func TestDeleteRouteTableNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	err := m.DeleteRouteTable(ctx, "rt-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDescribeRouteTablesWithIDs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	rt1, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpcID})
	require.NoError(t, err)
	rt2, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpcID})
	require.NoError(t, err)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all route tables", ids: nil, wantCount: 2},
		{name: "by single ID", ids: []string{rt1.ID}, wantCount: 1},
		{name: "by multiple IDs", ids: []string{rt1.ID, rt2.ID}, wantCount: 2},
		{name: "nonexistent ID", ids: []string{"rt-missing"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rts, err := m.DescribeRouteTables(ctx, tt.ids)
			require.NoError(t, err)
			assert.Len(t, rts, tt.wantCount)
		})
	}
}

func TestDeleteNetworkACLNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	err := m.DeleteNetworkACL(ctx, "acl-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDescribeNetworkACLsWithIDs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	acl1, err := m.CreateNetworkACL(ctx, vpcID, map[string]string{"name": "acl1"})
	require.NoError(t, err)
	acl2, err := m.CreateNetworkACL(ctx, vpcID, map[string]string{"name": "acl2"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
	}{
		{name: "all ACLs", ids: nil, wantCount: 2},
		{name: "by single ID", ids: []string{acl1.ID}, wantCount: 1},
		{name: "by multiple IDs", ids: []string{acl1.ID, acl2.ID}, wantCount: 2},
		{name: "nonexistent ID", ids: []string{"acl-missing"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acls, err := m.DescribeNetworkACLs(ctx, tt.ids)
			require.NoError(t, err)
			assert.Len(t, acls, tt.wantCount)
		})
	}
}

// --- Internet Gateway tests ---

func TestInternetGateway(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

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
		err := m.AttachInternetGateway(ctx, igws[0].ID, vpcID)
		require.NoError(t, err)

		igws, _ = m.DescribeInternetGateways(ctx, []string{igws[0].ID})
		require.Len(t, igws, 1)
		assert.Equal(t, "attached", igws[0].State)
		assert.Equal(t, vpcID, igws[0].VpcID)
	})

	t.Run("detach IGW", func(t *testing.T) {
		igws, _ := m.DescribeInternetGateways(ctx, nil)
		err := m.DetachInternetGateway(ctx, igws[0].ID, vpcID)
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

		err = m.AttachInternetGateway(ctx, igw.ID, "vnet-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("attach nonexistent IGW", func(t *testing.T) {
		err := m.AttachInternetGateway(ctx, "igw-missing", vpcID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("detach nonexistent IGW", func(t *testing.T) {
		err := m.DetachInternetGateway(ctx, "igw-missing", vpcID)
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
	vpcID := createTestVPC(t, m)

	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpcID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: vpcID})
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

// --- GetFlowLogRecords tests ---

func TestGetFlowLogRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	vpcID := createTestVPC(t, m)

	fl, err := m.CreateFlowLog(ctx, driver.FlowLogConfig{
		ResourceID: vpcID, ResourceType: "VPC", TrafficType: "ALL",
	})
	require.NoError(t, err)

	t.Run("returns records", func(t *testing.T) {
		records, err := m.GetFlowLogRecords(ctx, fl.ID, 5)
		require.NoError(t, err)
		assert.Len(t, records, 5)
		assert.Equal(t, fl.ID, records[0].FlowLogID)
		assert.NotEmpty(t, records[0].SourceIP)
		assert.NotEmpty(t, records[0].DestIP)
	})

	t.Run("default limit", func(t *testing.T) {
		records, err := m.GetFlowLogRecords(ctx, fl.ID, 0)
		require.NoError(t, err)
		assert.Len(t, records, 10)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetFlowLogRecords(ctx, "fl-missing", 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}
