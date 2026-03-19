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
