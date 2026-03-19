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

// --- test helpers ---

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
