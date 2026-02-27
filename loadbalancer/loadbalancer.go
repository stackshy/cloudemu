// Package loadbalancer provides a portable load balancer API with cross-cutting concerns.
package loadbalancer

import (
	"context"
	"time"

	"github.com/NitinKumar004/cloudemu/inject"
	"github.com/NitinKumar004/cloudemu/loadbalancer/driver"
	"github.com/NitinKumar004/cloudemu/metrics"
	"github.com/NitinKumar004/cloudemu/ratelimit"
	"github.com/NitinKumar004/cloudemu/recorder"
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
	for _, opt := range opts { opt(lb) }
	return lb
}

type Option func(*LB)

func WithRecorder(r *recorder.Recorder) Option    { return func(lb *LB) { lb.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(lb *LB) { lb.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(lb *LB) { lb.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(lb *LB) { lb.injector = i } }
func WithLatency(d time.Duration) Option           { return func(lb *LB) { lb.latency = d } }

func (lb *LB) do(ctx context.Context, op string, input interface{}, fn func() (interface{}, error)) (interface{}, error) {
	start := time.Now()
	if lb.injector != nil { if err := lb.injector.Check("loadbalancer", op); err != nil { lb.rec(op, input, nil, err, time.Since(start)); return nil, err } }
	if lb.limiter != nil { if err := lb.limiter.Allow(); err != nil { lb.rec(op, input, nil, err, time.Since(start)); return nil, err } }
	if lb.latency > 0 { time.Sleep(lb.latency) }
	out, err := fn()
	dur := time.Since(start)
	if lb.metrics != nil {
		labels := map[string]string{"service": "loadbalancer", "operation": op}
		lb.metrics.Counter("calls_total", 1, labels)
		lb.metrics.Histogram("call_duration", dur, labels)
		if err != nil { lb.metrics.Counter("errors_total", 1, labels) }
	}
	lb.rec(op, input, out, err, dur)
	return out, err
}

func (lb *LB) rec(op string, input, output interface{}, err error, dur time.Duration) {
	if lb.recorder != nil { lb.recorder.Record("loadbalancer", op, input, output, err, dur) }
}

func (lb *LB) CreateLoadBalancer(ctx context.Context, config driver.LBConfig) (*driver.LBInfo, error) {
	out, err := lb.do(ctx, "CreateLoadBalancer", config, func() (interface{}, error) { return lb.driver.CreateLoadBalancer(ctx, config) })
	if err != nil { return nil, err }
	return out.(*driver.LBInfo), nil
}
func (lb *LB) DeleteLoadBalancer(ctx context.Context, arn string) error { _, err := lb.do(ctx, "DeleteLoadBalancer", arn, func() (interface{}, error) { return nil, lb.driver.DeleteLoadBalancer(ctx, arn) }); return err }
func (lb *LB) DescribeLoadBalancers(ctx context.Context, arns []string) ([]driver.LBInfo, error) {
	out, err := lb.do(ctx, "DescribeLoadBalancers", arns, func() (interface{}, error) { return lb.driver.DescribeLoadBalancers(ctx, arns) })
	if err != nil { return nil, err }
	return out.([]driver.LBInfo), nil
}
func (lb *LB) CreateTargetGroup(ctx context.Context, config driver.TargetGroupConfig) (*driver.TargetGroupInfo, error) {
	out, err := lb.do(ctx, "CreateTargetGroup", config, func() (interface{}, error) { return lb.driver.CreateTargetGroup(ctx, config) })
	if err != nil { return nil, err }
	return out.(*driver.TargetGroupInfo), nil
}
func (lb *LB) DeleteTargetGroup(ctx context.Context, arn string) error { _, err := lb.do(ctx, "DeleteTargetGroup", arn, func() (interface{}, error) { return nil, lb.driver.DeleteTargetGroup(ctx, arn) }); return err }
func (lb *LB) DescribeTargetGroups(ctx context.Context, arns []string) ([]driver.TargetGroupInfo, error) {
	out, err := lb.do(ctx, "DescribeTargetGroups", arns, func() (interface{}, error) { return lb.driver.DescribeTargetGroups(ctx, arns) })
	if err != nil { return nil, err }
	return out.([]driver.TargetGroupInfo), nil
}
func (lb *LB) CreateListener(ctx context.Context, config driver.ListenerConfig) (*driver.ListenerInfo, error) {
	out, err := lb.do(ctx, "CreateListener", config, func() (interface{}, error) { return lb.driver.CreateListener(ctx, config) })
	if err != nil { return nil, err }
	return out.(*driver.ListenerInfo), nil
}
func (lb *LB) DeleteListener(ctx context.Context, arn string) error { _, err := lb.do(ctx, "DeleteListener", arn, func() (interface{}, error) { return nil, lb.driver.DeleteListener(ctx, arn) }); return err }
func (lb *LB) DescribeListeners(ctx context.Context, lbARN string) ([]driver.ListenerInfo, error) {
	out, err := lb.do(ctx, "DescribeListeners", lbARN, func() (interface{}, error) { return lb.driver.DescribeListeners(ctx, lbARN) })
	if err != nil { return nil, err }
	return out.([]driver.ListenerInfo), nil
}
func (lb *LB) RegisterTargets(ctx context.Context, tgARN string, targets []driver.Target) error { _, err := lb.do(ctx, "RegisterTargets", tgARN, func() (interface{}, error) { return nil, lb.driver.RegisterTargets(ctx, tgARN, targets) }); return err }
func (lb *LB) DeregisterTargets(ctx context.Context, tgARN string, targets []driver.Target) error { _, err := lb.do(ctx, "DeregisterTargets", tgARN, func() (interface{}, error) { return nil, lb.driver.DeregisterTargets(ctx, tgARN, targets) }); return err }
func (lb *LB) DescribeTargetHealth(ctx context.Context, tgARN string) ([]driver.TargetHealth, error) {
	out, err := lb.do(ctx, "DescribeTargetHealth", tgARN, func() (interface{}, error) { return lb.driver.DescribeTargetHealth(ctx, tgARN) })
	if err != nil { return nil, err }
	return out.([]driver.TargetHealth), nil
}
func (lb *LB) SetTargetHealth(ctx context.Context, tgARN, targetID, state string) error { _, err := lb.do(ctx, "SetTargetHealth", tgARN, func() (interface{}, error) { return nil, lb.driver.SetTargetHealth(ctx, tgARN, targetID, state) }); return err }
