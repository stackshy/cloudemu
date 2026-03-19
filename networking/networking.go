// Package networking provides a portable networking API with cross-cutting concerns.
package networking

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Networking is the portable networking type wrapping a driver.
type Networking struct {
	driver   driver.Networking
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

func NewNetworking(d driver.Networking, opts ...Option) *Networking {
	n := &Networking{driver: d}
	for _, opt := range opts {
		opt(n)
	}

	return n
}

type Option func(*Networking)

func WithRecorder(r *recorder.Recorder) Option     { return func(n *Networking) { n.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(n *Networking) { n.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(n *Networking) { n.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(n *Networking) { n.injector = i } }
func WithLatency(d time.Duration) Option           { return func(n *Networking) { n.latency = d } }

func (n *Networking) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if n.injector != nil {
		if err := n.injector.Check("networking", op); err != nil {
			n.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if n.limiter != nil {
		if err := n.limiter.Allow(); err != nil {
			n.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if n.latency > 0 {
		time.Sleep(n.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if n.metrics != nil {
		labels := map[string]string{"service": "networking", "operation": op}
		n.metrics.Counter("calls_total", 1, labels)
		n.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			n.metrics.Counter("errors_total", 1, labels)
		}
	}

	n.record(op, input, out, err, dur)

	return out, err
}

func (n *Networking) record(op string, input, output any, err error, dur time.Duration) {
	if n.recorder != nil {
		n.recorder.Record("networking", op, input, output, err, dur)
	}
}

func (n *Networking) CreateVPC(ctx context.Context, config driver.VPCConfig) (*driver.VPCInfo, error) {
	out, err := n.do(ctx, "CreateVPC", config, func() (any, error) { return n.driver.CreateVPC(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.VPCInfo), nil
}

func (n *Networking) DeleteVPC(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteVPC", id, func() (any, error) { return nil, n.driver.DeleteVPC(ctx, id) })
	return err
}

func (n *Networking) DescribeVPCs(ctx context.Context, ids []string) ([]driver.VPCInfo, error) {
	out, err := n.do(ctx, "DescribeVPCs", ids, func() (any, error) { return n.driver.DescribeVPCs(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.VPCInfo), nil
}

func (n *Networking) CreateSubnet(ctx context.Context, config driver.SubnetConfig) (*driver.SubnetInfo, error) {
	out, err := n.do(ctx, "CreateSubnet", config, func() (any, error) { return n.driver.CreateSubnet(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.SubnetInfo), nil
}

func (n *Networking) DeleteSubnet(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteSubnet", id, func() (any, error) { return nil, n.driver.DeleteSubnet(ctx, id) })
	return err
}

func (n *Networking) DescribeSubnets(ctx context.Context, ids []string) ([]driver.SubnetInfo, error) {
	out, err := n.do(ctx, "DescribeSubnets", ids, func() (any, error) { return n.driver.DescribeSubnets(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.SubnetInfo), nil
}

func (n *Networking) CreateSecurityGroup(ctx context.Context, config driver.SecurityGroupConfig) (*driver.SecurityGroupInfo, error) {
	out, err := n.do(ctx, "CreateSecurityGroup", config, func() (any, error) {
		return n.driver.CreateSecurityGroup(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.SecurityGroupInfo), nil
}

func (n *Networking) DeleteSecurityGroup(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteSecurityGroup", id, func() (any, error) {
		return nil, n.driver.DeleteSecurityGroup(ctx, id)
	})

	return err
}

func (n *Networking) DescribeSecurityGroups(ctx context.Context, ids []string) ([]driver.SecurityGroupInfo, error) {
	out, err := n.do(ctx, "DescribeSecurityGroups", ids, func() (any, error) {
		return n.driver.DescribeSecurityGroups(ctx, ids)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.SecurityGroupInfo), nil
}

func (n *Networking) AddIngressRule(ctx context.Context, groupID string, rule driver.SecurityRule) error {
	_, err := n.do(ctx, "AddIngressRule", rule, func() (any, error) {
		return nil, n.driver.AddIngressRule(ctx, groupID, rule)
	})

	return err
}

func (n *Networking) AddEgressRule(ctx context.Context, groupID string, rule driver.SecurityRule) error {
	_, err := n.do(ctx, "AddEgressRule", rule, func() (any, error) {
		return nil, n.driver.AddEgressRule(ctx, groupID, rule)
	})

	return err
}

func (n *Networking) RemoveIngressRule(ctx context.Context, groupID string, rule driver.SecurityRule) error {
	_, err := n.do(ctx, "RemoveIngressRule", rule, func() (any, error) {
		return nil, n.driver.RemoveIngressRule(ctx, groupID, rule)
	})

	return err
}

func (n *Networking) RemoveEgressRule(ctx context.Context, groupID string, rule driver.SecurityRule) error {
	_, err := n.do(ctx, "RemoveEgressRule", rule, func() (any, error) {
		return nil, n.driver.RemoveEgressRule(ctx, groupID, rule)
	})

	return err
}
