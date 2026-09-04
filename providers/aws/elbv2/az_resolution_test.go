package elbv2

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestCreateLoadBalancerResolvesSubnetAZs proves a load balancer's SubnetAZs
// reflects each member subnet's real availability zone rather than a single
// stand-in zone repeated for every subnet. Real ELBv2 reports the actual AZ a
// subnet sits in, which matters for a multi-AZ load balancer spanning
// different zones: reporting the same zone for every subnet is a divergence
// Terraform's aws_lb resource surfaces as a perpetual plan diff.
func TestCreateLoadBalancerResolvesSubnetAZs(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	ctx := context.Background()

	vpcMock := vpc.New(opts)
	m := New(opts)
	m.SetSubnetResolver(vpcMock)

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	requireNoError(t, err)

	sn1, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	requireNoError(t, err)

	sn2, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.2.0/24", AvailabilityZone: "us-east-1b",
	})
	requireNoError(t, err)

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{
		Name: "az-lb", Type: "application", Subnets: []string{sn1.ID, sn2.ID},
	})
	requireNoError(t, err)

	assertEqual(t, "us-east-1a", lb.SubnetAZs[sn1.ID])
	assertEqual(t, "us-east-1b", lb.SubnetAZs[sn2.ID])
}

// TestCreateLoadBalancerNoResolverLeavesSubnetAZsNil proves the field stays
// nil (not a crash) when no subnet resolver is wired.
func TestCreateLoadBalancerNoResolverLeavesSubnetAZsNil(t *testing.T) {
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(context.Background(),
		driver.LBConfig{Name: "no-resolver-lb", Subnets: []string{"subnet-1"}})
	requireNoError(t, err)

	if lb.SubnetAZs != nil {
		t.Fatalf("SubnetAZs = %v, want nil with no resolver wired", lb.SubnetAZs)
	}
}

// TestSetSubnetsResolvesSubnetAZs proves SetSubnets re-resolves SubnetAZs for
// the replacement subnet list, not just the original set from create time.
func TestSetSubnetsResolvesSubnetAZs(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	ctx := context.Background()

	vpcMock := vpc.New(opts)
	m := New(opts)
	m.SetSubnetResolver(vpcMock)

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	requireNoError(t, err)

	sn1, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	requireNoError(t, err)

	sn2, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.2.0/24", AvailabilityZone: "us-east-1c",
	})
	requireNoError(t, err)

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{
		Name: "set-subnets-lb", Type: "application", Subnets: []string{sn1.ID},
	})
	requireNoError(t, err)

	_, err = m.SetSubnets(ctx, lb.ARN, []string{sn1.ID, sn2.ID})
	requireNoError(t, err)

	lbs, err := m.DescribeLoadBalancers(ctx, []string{lb.ARN})
	requireNoError(t, err)
	if len(lbs) != 1 {
		t.Fatalf("DescribeLoadBalancers = %d results, want 1", len(lbs))
	}

	assertEqual(t, "us-east-1a", lbs[0].SubnetAZs[sn1.ID])
	assertEqual(t, "us-east-1c", lbs[0].SubnetAZs[sn2.ID])
}
