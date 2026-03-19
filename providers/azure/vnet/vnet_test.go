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
