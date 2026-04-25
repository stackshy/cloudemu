package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
)

func newChaosLoadBalancer(t *testing.T) (lbdriver.LoadBalancer, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapLoadBalancer(cloudemu.NewAWS().ELB, e), e
}

func TestWrapLoadBalancerCreateLoadBalancerChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()

	if _, err := l.CreateLoadBalancer(ctx, lbdriver.LBConfig{Name: "ok", Type: "application"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	if _, err := l.CreateLoadBalancer(ctx, lbdriver.LBConfig{Name: "fail", Type: "application"}); err == nil {
		t.Error("expected chaos error on CreateLoadBalancer")
	}
}

func TestWrapLoadBalancerDeleteLoadBalancerChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()
	lb, _ := l.CreateLoadBalancer(ctx, lbdriver.LBConfig{Name: "del", Type: "application"})

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	if err := l.DeleteLoadBalancer(ctx, lb.ARN); err == nil {
		t.Error("expected chaos error on DeleteLoadBalancer")
	}
}

func TestWrapLoadBalancerDescribeLoadBalancersChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	if _, err := l.DescribeLoadBalancers(ctx, nil); err == nil {
		t.Error("expected chaos error on DescribeLoadBalancers")
	}
}

func TestWrapLoadBalancerCreateTargetGroupChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	cfg := lbdriver.TargetGroupConfig{Name: "tg", Protocol: "HTTP", Port: 80, VPCID: "vpc-1"}
	if _, err := l.CreateTargetGroup(ctx, cfg); err == nil {
		t.Error("expected chaos error on CreateTargetGroup")
	}
}

func TestWrapLoadBalancerDeleteTargetGroupChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()
	tg, _ := l.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{Name: "tgdel", Protocol: "HTTP", Port: 80, VPCID: "vpc-1"})

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	if err := l.DeleteTargetGroup(ctx, tg.ARN); err == nil {
		t.Error("expected chaos error on DeleteTargetGroup")
	}
}

func TestWrapLoadBalancerDescribeTargetGroupsChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	if _, err := l.DescribeTargetGroups(ctx, nil); err == nil {
		t.Error("expected chaos error on DescribeTargetGroups")
	}
}

func TestWrapLoadBalancerRegisterTargetsChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()
	tg, _ := l.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{Name: "tgreg", Protocol: "HTTP", Port: 80, VPCID: "vpc-1"})

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	if err := l.RegisterTargets(ctx, tg.ARN, []lbdriver.Target{{ID: "i-1", Port: 80}}); err == nil {
		t.Error("expected chaos error on RegisterTargets")
	}
}

func TestWrapLoadBalancerDeregisterTargetsChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()
	tg, _ := l.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{Name: "tgdereg", Protocol: "HTTP", Port: 80, VPCID: "vpc-1"})
	_ = l.RegisterTargets(ctx, tg.ARN, []lbdriver.Target{{ID: "i-1", Port: 80}})

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	if err := l.DeregisterTargets(ctx, tg.ARN, []lbdriver.Target{{ID: "i-1", Port: 80}}); err == nil {
		t.Error("expected chaos error on DeregisterTargets")
	}
}

func TestWrapLoadBalancerDescribeTargetHealthChaos(t *testing.T) {
	l, e := newChaosLoadBalancer(t)
	ctx := context.Background()
	tg, _ := l.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{Name: "tgh", Protocol: "HTTP", Port: 80, VPCID: "vpc-1"})

	e.Apply(chaos.ServiceOutage("loadbalancer", time.Hour))

	if _, err := l.DescribeTargetHealth(ctx, tg.ARN); err == nil {
		t.Error("expected chaos error on DescribeTargetHealth")
	}
}
