package elbv2

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestCreateLoadBalancerResolvesVPCID proves that, with the networking mock
// wired in, a load balancer's VpcId is derived from its member subnets — as
// real ELBv2 does — instead of being left empty.
func TestCreateLoadBalancerResolvesVPCID(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	ctx := context.Background()

	vpcMock := vpc.New(opts)
	m := New(opts)
	m.SetSubnetResolver(vpcMock)

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	requireNoError(t, err)

	sn, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	requireNoError(t, err)

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "vpc-lb", Subnets: []string{sn.ID}})
	requireNoError(t, err)
	assertEqual(t, v.ID, lb.VPCID)
}

// TestCreateLoadBalancerNoResolverLeavesVPCIDEmpty proves the field stays empty
// (not a crash) when no resolver is wired.
func TestCreateLoadBalancerNoResolverLeavesVPCIDEmpty(t *testing.T) {
	m := newTestMock()
	lb, err := m.CreateLoadBalancer(context.Background(),
		driver.LBConfig{Name: "no-vpc-lb", Subnets: []string{"subnet-1"}})
	requireNoError(t, err)
	assertEqual(t, "", lb.VPCID)
}
