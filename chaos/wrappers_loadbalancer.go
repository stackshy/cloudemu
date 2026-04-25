package chaos

import (
	"context"

	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
)

// chaosLoadBalancer wraps a load balancer driver. Hot-path: LB / target group /
// target registration + health. Listeners, rules, and attributes delegate
// through.
type chaosLoadBalancer struct {
	lbdriver.LoadBalancer
	engine *Engine
}

// WrapLoadBalancer returns a load balancer driver that consults engine on
// LB / target-group / target operations.
func WrapLoadBalancer(inner lbdriver.LoadBalancer, engine *Engine) lbdriver.LoadBalancer {
	return &chaosLoadBalancer{LoadBalancer: inner, engine: engine}
}

//nolint:gocritic // cfg is a value type by interface contract
func (c *chaosLoadBalancer) CreateLoadBalancer(
	ctx context.Context, cfg lbdriver.LBConfig,
) (*lbdriver.LBInfo, error) {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "CreateLoadBalancer"); err != nil {
		return nil, err
	}

	return c.LoadBalancer.CreateLoadBalancer(ctx, cfg)
}

func (c *chaosLoadBalancer) DeleteLoadBalancer(ctx context.Context, arn string) error {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "DeleteLoadBalancer"); err != nil {
		return err
	}

	return c.LoadBalancer.DeleteLoadBalancer(ctx, arn)
}

func (c *chaosLoadBalancer) DescribeLoadBalancers(ctx context.Context, arns []string) ([]lbdriver.LBInfo, error) {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "DescribeLoadBalancers"); err != nil {
		return nil, err
	}

	return c.LoadBalancer.DescribeLoadBalancers(ctx, arns)
}

//nolint:gocritic // cfg is a value type by interface contract
func (c *chaosLoadBalancer) CreateTargetGroup(
	ctx context.Context, cfg lbdriver.TargetGroupConfig,
) (*lbdriver.TargetGroupInfo, error) {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "CreateTargetGroup"); err != nil {
		return nil, err
	}

	return c.LoadBalancer.CreateTargetGroup(ctx, cfg)
}

func (c *chaosLoadBalancer) DeleteTargetGroup(ctx context.Context, arn string) error {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "DeleteTargetGroup"); err != nil {
		return err
	}

	return c.LoadBalancer.DeleteTargetGroup(ctx, arn)
}

func (c *chaosLoadBalancer) DescribeTargetGroups(
	ctx context.Context, arns []string,
) ([]lbdriver.TargetGroupInfo, error) {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "DescribeTargetGroups"); err != nil {
		return nil, err
	}

	return c.LoadBalancer.DescribeTargetGroups(ctx, arns)
}

func (c *chaosLoadBalancer) RegisterTargets(
	ctx context.Context, targetGroupARN string, targets []lbdriver.Target,
) error {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "RegisterTargets"); err != nil {
		return err
	}

	return c.LoadBalancer.RegisterTargets(ctx, targetGroupARN, targets)
}

func (c *chaosLoadBalancer) DeregisterTargets(
	ctx context.Context, targetGroupARN string, targets []lbdriver.Target,
) error {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "DeregisterTargets"); err != nil {
		return err
	}

	return c.LoadBalancer.DeregisterTargets(ctx, targetGroupARN, targets)
}

func (c *chaosLoadBalancer) DescribeTargetHealth(
	ctx context.Context, targetGroupARN string,
) ([]lbdriver.TargetHealth, error) {
	if err := applyChaos(ctx, c.engine, "loadbalancer", "DescribeTargetHealth"); err != nil {
		return nil, err
	}

	return c.LoadBalancer.DescribeTargetHealth(ctx, targetGroupARN)
}
