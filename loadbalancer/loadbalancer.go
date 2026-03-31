// Package loadbalancer provides a portable load balancer API with cross-cutting concerns.
package loadbalancer

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/loadbalancer/driver"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

type LB struct {
	driver   driver.LoadBalancer
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

func NewLB(d driver.LoadBalancer, opts ...Option) *LB {
	lb := &LB{driver: d}
	for _, opt := range opts {
		opt(lb)
	}

	return lb
}

type Option func(*LB)

func WithRecorder(r *recorder.Recorder) Option     { return func(lb *LB) { lb.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(lb *LB) { lb.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(lb *LB) { lb.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(lb *LB) { lb.injector = i } }
func WithLatency(d time.Duration) Option           { return func(lb *LB) { lb.latency = d } }

func (lb *LB) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if lb.injector != nil {
		if err := lb.injector.Check("loadbalancer", op); err != nil {
			lb.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if lb.limiter != nil {
		if err := lb.limiter.Allow(); err != nil {
			lb.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if lb.latency > 0 {
		time.Sleep(lb.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if lb.metrics != nil {
		labels := map[string]string{"service": "loadbalancer", "operation": op}
		lb.metrics.Counter("calls_total", 1, labels)
		lb.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			lb.metrics.Counter("errors_total", 1, labels)
		}
	}

	lb.rec(op, input, out, err, dur)

	return out, err
}

func (lb *LB) rec(op string, input, output any, err error, dur time.Duration) {
	if lb.recorder != nil {
		lb.recorder.Record("loadbalancer", op, input, output, err, dur)
	}
}

//nolint:gocritic // config passed by value to match driver.LoadBalancer interface pattern
func (lb *LB) CreateLoadBalancer(ctx context.Context, config driver.LBConfig) (*driver.LBInfo, error) {
	out, err := lb.do(ctx, "CreateLoadBalancer", config, func() (any, error) { return lb.driver.CreateLoadBalancer(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.LBInfo), nil
}

func (lb *LB) DeleteLoadBalancer(ctx context.Context, arn string) error {
	_, err := lb.do(ctx, "DeleteLoadBalancer", arn, func() (any, error) { return nil, lb.driver.DeleteLoadBalancer(ctx, arn) })
	return err
}

func (lb *LB) DescribeLoadBalancers(ctx context.Context, arns []string) ([]driver.LBInfo, error) {
	out, err := lb.do(ctx, "DescribeLoadBalancers", arns, func() (any, error) { return lb.driver.DescribeLoadBalancers(ctx, arns) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.LBInfo), nil
}

//nolint:gocritic // config passed by value to match driver.LoadBalancer interface pattern
func (lb *LB) CreateTargetGroup(ctx context.Context, config driver.TargetGroupConfig) (*driver.TargetGroupInfo, error) {
	out, err := lb.do(ctx, "CreateTargetGroup", config, func() (any, error) { return lb.driver.CreateTargetGroup(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.TargetGroupInfo), nil
}

func (lb *LB) DeleteTargetGroup(ctx context.Context, arn string) error {
	_, err := lb.do(ctx, "DeleteTargetGroup", arn, func() (any, error) { return nil, lb.driver.DeleteTargetGroup(ctx, arn) })
	return err
}

func (lb *LB) DescribeTargetGroups(ctx context.Context, arns []string) ([]driver.TargetGroupInfo, error) {
	out, err := lb.do(ctx, "DescribeTargetGroups", arns, func() (any, error) { return lb.driver.DescribeTargetGroups(ctx, arns) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.TargetGroupInfo), nil
}

func (lb *LB) CreateListener(ctx context.Context, config driver.ListenerConfig) (*driver.ListenerInfo, error) {
	out, err := lb.do(ctx, "CreateListener", config, func() (any, error) { return lb.driver.CreateListener(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.ListenerInfo), nil
}

func (lb *LB) DeleteListener(ctx context.Context, arn string) error {
	_, err := lb.do(ctx, "DeleteListener", arn, func() (any, error) { return nil, lb.driver.DeleteListener(ctx, arn) })
	return err
}

func (lb *LB) DescribeListeners(ctx context.Context, lbARN string) ([]driver.ListenerInfo, error) {
	out, err := lb.do(ctx, "DescribeListeners", lbARN, func() (any, error) { return lb.driver.DescribeListeners(ctx, lbARN) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.ListenerInfo), nil
}

func (lb *LB) CreateRule(ctx context.Context, config driver.RuleConfig) (*driver.RuleInfo, error) {
	out, err := lb.do(ctx, "CreateRule", config, func() (any, error) { return lb.driver.CreateRule(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.RuleInfo), nil
}

func (lb *LB) DeleteRule(ctx context.Context, ruleARN string) error {
	_, err := lb.do(ctx, "DeleteRule", ruleARN, func() (any, error) { return nil, lb.driver.DeleteRule(ctx, ruleARN) })
	return err
}

func (lb *LB) DescribeRules(ctx context.Context, listenerARN string) ([]driver.RuleInfo, error) {
	out, err := lb.do(ctx, "DescribeRules", listenerARN, func() (any, error) { return lb.driver.DescribeRules(ctx, listenerARN) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.RuleInfo), nil
}

func (lb *LB) ModifyListener(ctx context.Context, input driver.ModifyListenerInput) error {
	_, err := lb.do(ctx, "ModifyListener", input, func() (any, error) { return nil, lb.driver.ModifyListener(ctx, input) })
	return err
}

func (lb *LB) GetLBAttributes(ctx context.Context, lbARN string) (*driver.LBAttributes, error) {
	out, err := lb.do(ctx, "GetLBAttributes", lbARN, func() (any, error) { return lb.driver.GetLBAttributes(ctx, lbARN) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.LBAttributes), nil
}

func (lb *LB) PutLBAttributes(ctx context.Context, lbARN string, attrs driver.LBAttributes) error {
	_, err := lb.do(ctx, "PutLBAttributes", lbARN, func() (any, error) { return nil, lb.driver.PutLBAttributes(ctx, lbARN, attrs) })
	return err
}

func (lb *LB) RegisterTargets(ctx context.Context, tgARN string, targets []driver.Target) error {
	_, err := lb.do(ctx, "RegisterTargets", tgARN, func() (any, error) { return nil, lb.driver.RegisterTargets(ctx, tgARN, targets) })
	return err
}

func (lb *LB) DeregisterTargets(ctx context.Context, tgARN string, targets []driver.Target) error {
	_, err := lb.do(ctx, "DeregisterTargets", tgARN, func() (any, error) { return nil, lb.driver.DeregisterTargets(ctx, tgARN, targets) })
	return err
}

func (lb *LB) DescribeTargetHealth(ctx context.Context, tgARN string) ([]driver.TargetHealth, error) {
	out, err := lb.do(ctx, "DescribeTargetHealth", tgARN, func() (any, error) { return lb.driver.DescribeTargetHealth(ctx, tgARN) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.TargetHealth), nil
}

func (lb *LB) SetTargetHealth(ctx context.Context, tgARN, targetID, state string) error {
	_, err := lb.do(ctx, "SetTargetHealth", tgARN, func() (any, error) { return nil, lb.driver.SetTargetHealth(ctx, tgARN, targetID, state) })
	return err
}
