package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
)

func newChaosNetworking(t *testing.T) (netdriver.Networking, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapNetworking(cloudemu.NewAWS().VPC, e), e
}

func TestWrapNetworkingCreateVPCChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()

	if _, err := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if _, err := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.1.0.0/16"}); err == nil {
		t.Error("expected chaos error on CreateVPC")
	}
}

func TestWrapNetworkingDeleteVPCChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()
	v, _ := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if err := n.DeleteVPC(ctx, v.ID); err == nil {
		t.Error("expected chaos error on DeleteVPC")
	}
}

func TestWrapNetworkingDescribeVPCsChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()

	if _, err := n.DescribeVPCs(ctx, nil); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if _, err := n.DescribeVPCs(ctx, nil); err == nil {
		t.Error("expected chaos error on DescribeVPCs")
	}
}

func TestWrapNetworkingCreateSubnetChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()
	v, _ := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if _, err := n.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"}); err == nil {
		t.Error("expected chaos error on CreateSubnet")
	}
}

func TestWrapNetworkingDeleteSubnetChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()
	v, _ := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	s, _ := n.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if err := n.DeleteSubnet(ctx, s.ID); err == nil {
		t.Error("expected chaos error on DeleteSubnet")
	}
}

func TestWrapNetworkingDescribeSubnetsChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if _, err := n.DescribeSubnets(ctx, nil); err == nil {
		t.Error("expected chaos error on DescribeSubnets")
	}
}

func TestWrapNetworkingCreateSecurityGroupChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()
	v, _ := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if _, err := n.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{Name: "sg", VPCID: v.ID}); err == nil {
		t.Error("expected chaos error on CreateSecurityGroup")
	}
}

func TestWrapNetworkingDeleteSecurityGroupChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()
	v, _ := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	sg, _ := n.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{Name: "sg", VPCID: v.ID})

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if err := n.DeleteSecurityGroup(ctx, sg.ID); err == nil {
		t.Error("expected chaos error on DeleteSecurityGroup")
	}
}

func TestWrapNetworkingDescribeSecurityGroupsChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	if _, err := n.DescribeSecurityGroups(ctx, nil); err == nil {
		t.Error("expected chaos error on DescribeSecurityGroups")
	}
}

func TestWrapNetworkingAddIngressRuleChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()
	v, _ := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	sg, _ := n.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{Name: "sg", VPCID: v.ID})

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	rule := netdriver.SecurityRule{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "0.0.0.0/0"}
	if err := n.AddIngressRule(ctx, sg.ID, rule); err == nil {
		t.Error("expected chaos error on AddIngressRule")
	}
}

func TestWrapNetworkingAddEgressRuleChaos(t *testing.T) {
	n, e := newChaosNetworking(t)
	ctx := context.Background()
	v, _ := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	sg, _ := n.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{Name: "sg", VPCID: v.ID})

	e.Apply(chaos.ServiceOutage("networking", time.Hour))

	rule := netdriver.SecurityRule{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0"}
	if err := n.AddEgressRule(ctx, sg.ID, rule); err == nil {
		t.Error("expected chaos error on AddEgressRule")
	}
}
