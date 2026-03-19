// Package elb provides an in-memory mock implementation of AWS Elastic Load Balancing.
package elb

import (
	"context"
	"fmt"
	"sync"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/loadbalancer/driver"
)

// Compile-time check that Mock implements driver.LoadBalancer.
var _ driver.LoadBalancer = (*Mock)(nil)

// Mock is an in-memory mock implementation of the AWS ELB service.
type Mock struct {
	lbs       *memstore.Store[driver.LBInfo]
	tgs       *memstore.Store[driver.TargetGroupInfo]
	listeners *memstore.Store[driver.ListenerInfo]
	opts      *config.Options

	healthMu sync.RWMutex
	health   map[string]map[string]*driver.TargetHealth // tgARN -> targetID -> health
}

// New creates a new ELB mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		lbs:       memstore.New[driver.LBInfo](),
		tgs:       memstore.New[driver.TargetGroupInfo](),
		listeners: memstore.New[driver.ListenerInfo](),
		opts:      opts,
		health:    make(map[string]map[string]*driver.TargetHealth),
	}
}

// CreateLoadBalancer creates a new load balancer.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateLoadBalancer(_ context.Context, cfg driver.LBConfig) (*driver.LBInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "load balancer name is required")
	}

	id := idgen.GenerateID("lb-")
	arn := idgen.AWSARN("elasticloadbalancing", m.opts.Region, m.opts.AccountID, "loadbalancer/"+cfg.Name)
	dnsName := fmt.Sprintf("%s.%s.elb.amazonaws.com", cfg.Name, m.opts.Region)

	subnets := make([]string, len(cfg.Subnets))
	copy(subnets, cfg.Subnets)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	lb := driver.LBInfo{
		ID:      id,
		ARN:     arn,
		Name:    cfg.Name,
		Type:    cfg.Type,
		Scheme:  cfg.Scheme,
		State:   "active",
		DNSName: dnsName,
		Subnets: subnets,
		Tags:    tags,
	}

	m.lbs.Set(arn, lb)

	result := lb

	return &result, nil
}

// DeleteLoadBalancer deletes a load balancer by ARN.
func (m *Mock) DeleteLoadBalancer(_ context.Context, arn string) error {
	if !m.lbs.Delete(arn) {
		return errors.Newf(errors.NotFound, "load balancer %q not found", arn)
	}

	// Delete all listeners associated with this load balancer.
	all := m.listeners.All()
	for key, li := range all {
		if li.LBARN == arn {
			m.listeners.Delete(key)
		}
	}

	return nil
}

// DescribeLoadBalancers returns load balancers matching the given ARNs.
// If arns is empty, all load balancers are returned.
func (m *Mock) DescribeLoadBalancers(_ context.Context, arns []string) ([]driver.LBInfo, error) {
	return describeResources(m.lbs, arns), nil
}

// CreateTargetGroup creates a new target group.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateTargetGroup(_ context.Context, cfg driver.TargetGroupConfig) (*driver.TargetGroupInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "target group name is required")
	}

	id := idgen.GenerateID("tg-")
	arn := idgen.AWSARN("elasticloadbalancing", m.opts.Region, m.opts.AccountID, "targetgroup/"+cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	tg := driver.TargetGroupInfo{
		ID:         id,
		ARN:        arn,
		Name:       cfg.Name,
		Protocol:   cfg.Protocol,
		Port:       cfg.Port,
		VPCID:      cfg.VPCID,
		HealthPath: cfg.HealthPath,
		Tags:       tags,
	}

	m.tgs.Set(arn, tg)

	// Initialize health map for this target group.
	m.healthMu.Lock()
	m.health[arn] = make(map[string]*driver.TargetHealth)
	m.healthMu.Unlock()

	result := tg

	return &result, nil
}

// DeleteTargetGroup deletes a target group by ARN.
func (m *Mock) DeleteTargetGroup(_ context.Context, arn string) error {
	if !m.tgs.Delete(arn) {
		return errors.Newf(errors.NotFound, "target group %q not found", arn)
	}

	// Clean up health data.
	m.healthMu.Lock()
	delete(m.health, arn)
	m.healthMu.Unlock()

	return nil
}

// DescribeTargetGroups returns target groups matching the given ARNs.
// If arns is empty, all target groups are returned.
func (m *Mock) DescribeTargetGroups(_ context.Context, arns []string) ([]driver.TargetGroupInfo, error) {
	return describeResources(m.tgs, arns), nil
}

// describeResources is a generic helper for Describe* methods that list or filter by keys.
func describeResources[T any](store *memstore.Store[T], keys []string) []T {
	if len(keys) == 0 {
		all := store.All()
		results := make([]T, 0, len(all))

		for _, item := range all {
			results = append(results, item)
		}

		return results
	}

	results := make([]T, 0, len(keys))

	for _, key := range keys {
		item, ok := store.Get(key)
		if !ok {
			continue
		}

		results = append(results, item)
	}

	return results
}

// CreateListener creates a new listener on a load balancer.
func (m *Mock) CreateListener(_ context.Context, cfg driver.ListenerConfig) (*driver.ListenerInfo, error) {
	if _, ok := m.lbs.Get(cfg.LBARN); !ok {
		return nil, errors.Newf(errors.NotFound, "load balancer %q not found", cfg.LBARN)
	}

	arn := idgen.AWSARN("elasticloadbalancing", m.opts.Region, m.opts.AccountID,
		fmt.Sprintf("listener/%s/%08x", cfg.LBARN, cfg.Port))

	li := driver.ListenerInfo{
		ARN:            arn,
		LBARN:          cfg.LBARN,
		Protocol:       cfg.Protocol,
		Port:           cfg.Port,
		TargetGroupARN: cfg.TargetGroupARN,
	}

	m.listeners.Set(arn, li)

	result := li

	return &result, nil
}

// DeleteListener deletes a listener by ARN.
func (m *Mock) DeleteListener(_ context.Context, arn string) error {
	if !m.listeners.Delete(arn) {
		return errors.Newf(errors.NotFound, "listener %q not found", arn)
	}

	return nil
}

// DescribeListeners returns all listeners for the specified load balancer.
func (m *Mock) DescribeListeners(_ context.Context, lbARN string) ([]driver.ListenerInfo, error) {
	if _, ok := m.lbs.Get(lbARN); !ok {
		return nil, errors.Newf(errors.NotFound, "load balancer %q not found", lbARN)
	}

	filtered := m.listeners.Filter(func(_ string, li driver.ListenerInfo) bool {
		return li.LBARN == lbARN
	})

	results := make([]driver.ListenerInfo, 0, len(filtered))
	for _, li := range filtered {
		results = append(results, li)
	}

	return results, nil
}

// RegisterTargets registers targets with a target group.
func (m *Mock) RegisterTargets(_ context.Context, targetGroupARN string, targets []driver.Target) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		tgHealth = make(map[string]*driver.TargetHealth)
		m.health[targetGroupARN] = tgHealth
	}

	for _, t := range targets {
		tgHealth[t.ID] = &driver.TargetHealth{
			Target: driver.Target{
				ID:   t.ID,
				Port: t.Port,
			},
			State:  "initial",
			Reason: "Target registration is in progress",
		}
	}

	return nil
}

// DeregisterTargets removes targets from a target group.
func (m *Mock) DeregisterTargets(_ context.Context, targetGroupARN string, targets []driver.Target) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		return nil
	}

	for _, t := range targets {
		delete(tgHealth, t.ID)
	}

	return nil
}

// DescribeTargetHealth returns the health status of all targets in a target group.
func (m *Mock) DescribeTargetHealth(_ context.Context, targetGroupARN string) ([]driver.TargetHealth, error) {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return nil, errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.healthMu.RLock()
	defer m.healthMu.RUnlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		return []driver.TargetHealth{}, nil
	}

	results := make([]driver.TargetHealth, 0, len(tgHealth))
	for _, th := range tgHealth {
		results = append(results, *th)
	}

	return results, nil
}

// SetTargetHealth sets the health state of a specific target in a target group.
func (m *Mock) SetTargetHealth(_ context.Context, targetGroupARN, targetID, state string) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		return errors.Newf(errors.NotFound, "no targets registered in target group %q", targetGroupARN)
	}

	th, ok := tgHealth[targetID]
	if !ok {
		return errors.Newf(errors.NotFound, "target %q not found in target group %q", targetID, targetGroupARN)
	}

	th.State = state
	th.Reason = ""

	return nil
}
