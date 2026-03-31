// Package gcplb provides an in-memory mock implementation of GCP Cloud Load Balancing.
package gcplb

import (
	"context"
	"fmt"
	"sync"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/loadbalancer/driver"
)

// Compile-time check that Mock implements driver.LoadBalancer.
var _ driver.LoadBalancer = (*Mock)(nil)

// defaultIdleTimeoutSec is the default idle timeout for load balancers in seconds.
const defaultIdleTimeoutSec = 60

// Mock is an in-memory mock implementation of the GCP Cloud Load Balancing service.
type Mock struct {
	lbs       *memstore.Store[driver.LBInfo]
	tgs       *memstore.Store[driver.TargetGroupInfo]
	listeners *memstore.Store[driver.ListenerInfo]
	rules     *memstore.Store[driver.RuleInfo]
	opts      *config.Options

	healthMu sync.RWMutex
	health   map[string]map[string]*driver.TargetHealth // tgARN -> targetID -> health

	attrsMu sync.RWMutex
	attrs   map[string]driver.LBAttributes // lbARN -> attributes
}

// New creates a new Cloud Load Balancing mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		lbs:       memstore.New[driver.LBInfo](),
		tgs:       memstore.New[driver.TargetGroupInfo](),
		listeners: memstore.New[driver.ListenerInfo](),
		rules:     memstore.New[driver.RuleInfo](),
		opts:      opts,
		health:    make(map[string]map[string]*driver.TargetHealth),
		attrs:     make(map[string]driver.LBAttributes),
	}
}

// CreateLoadBalancer creates a new forwarding rule (load balancer).
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateLoadBalancer(_ context.Context, cfg driver.LBConfig) (*driver.LBInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "load balancer name is required")
	}

	id := idgen.GenerateID("lb-")
	arn := idgen.GCPID(m.opts.ProjectID, "forwardingRules", cfg.Name)
	dnsName := fmt.Sprintf("%s.%s.lb.gcp.example.com", cfg.Name, m.opts.Region)

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

// DeleteLoadBalancer deletes a forwarding rule (load balancer) by resource name (ARN).
func (m *Mock) DeleteLoadBalancer(_ context.Context, arn string) error {
	if !m.lbs.Delete(arn) {
		return cerrors.Newf(cerrors.NotFound, "load balancer %q not found", arn)
	}

	// Delete all listeners (URL maps) associated with this load balancer.
	all := m.listeners.All()
	for key, li := range all {
		if li.LBARN == arn {
			m.listeners.Delete(key)
		}
	}

	return nil
}

// DescribeLoadBalancers returns load balancers matching the given resource names (ARNs).
// If arns is empty, all load balancers are returned.
func (m *Mock) DescribeLoadBalancers(_ context.Context, arns []string) ([]driver.LBInfo, error) {
	return describeResources(m.lbs, arns), nil
}

// CreateTargetGroup creates a new backend service (target group).
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateTargetGroup(_ context.Context, cfg driver.TargetGroupConfig) (*driver.TargetGroupInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "backend service name is required")
	}

	id := idgen.GenerateID("bs-")
	arn := idgen.GCPID(m.opts.ProjectID, "backendServices", cfg.Name)

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

	// Initialize health map for this backend service.
	m.healthMu.Lock()
	m.health[arn] = make(map[string]*driver.TargetHealth)
	m.healthMu.Unlock()

	result := tg

	return &result, nil
}

// DeleteTargetGroup deletes a backend service (target group) by resource name (ARN).
func (m *Mock) DeleteTargetGroup(_ context.Context, arn string) error {
	if !m.tgs.Delete(arn) {
		return cerrors.Newf(cerrors.NotFound, "backend service %q not found", arn)
	}

	// Clean up health data.
	m.healthMu.Lock()
	delete(m.health, arn)
	m.healthMu.Unlock()

	return nil
}

// DescribeTargetGroups returns backend services (target groups) matching the given resource names.
// If arns is empty, all backend services are returned.
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

// filterToSlice returns a slice of values from the store that match the predicate.
func filterToSlice[T any](store *memstore.Store[T], pred func(string, T) bool) []T {
	filtered := store.Filter(pred)

	results := make([]T, 0, len(filtered))
	for _, item := range filtered {
		results = append(results, item)
	}

	return results
}

// CreateListener creates a new URL map / listener on a load balancer.
func (m *Mock) CreateListener(_ context.Context, cfg driver.ListenerConfig) (*driver.ListenerInfo, error) {
	if _, ok := m.lbs.Get(cfg.LBARN); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "load balancer %q not found", cfg.LBARN)
	}

	arn := idgen.GCPID(m.opts.ProjectID, "urlMaps",
		fmt.Sprintf("%s-%d", cfg.LBARN, cfg.Port))

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

// DeleteListener deletes a URL map / listener by resource name (ARN).
func (m *Mock) DeleteListener(_ context.Context, arn string) error {
	if !m.listeners.Delete(arn) {
		return cerrors.Newf(cerrors.NotFound, "listener %q not found", arn)
	}

	return nil
}

// DescribeListeners returns all listeners (URL maps) for the specified load balancer.
func (m *Mock) DescribeListeners(_ context.Context, lbARN string) ([]driver.ListenerInfo, error) {
	if _, ok := m.lbs.Get(lbARN); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "load balancer %q not found", lbARN)
	}

	return filterToSlice(m.listeners, func(_ string, li driver.ListenerInfo) bool {
		return li.LBARN == lbARN
	}), nil
}

// CreateRule creates a new URL map path rule for a listener.
func (m *Mock) CreateRule(_ context.Context, cfg driver.RuleConfig) (*driver.RuleInfo, error) {
	if _, ok := m.listeners.Get(cfg.ListenerARN); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "listener %q not found", cfg.ListenerARN)
	}

	arn := idgen.GCPID(m.opts.ProjectID, "pathRules", idgen.GenerateID("rule-"))

	conditions := make([]driver.RuleCondition, len(cfg.Conditions))
	copy(conditions, cfg.Conditions)

	actions := make([]driver.RuleAction, len(cfg.Actions))
	copy(actions, cfg.Actions)

	rule := driver.RuleInfo{
		ARN:         arn,
		ListenerARN: cfg.ListenerARN,
		Priority:    cfg.Priority,
		Conditions:  conditions,
		Actions:     actions,
		IsDefault:   false,
	}

	m.rules.Set(arn, rule)

	result := rule

	return &result, nil
}

// DeleteRule deletes a URL map path rule by resource name (ARN).
func (m *Mock) DeleteRule(_ context.Context, ruleARN string) error {
	if !m.rules.Delete(ruleARN) {
		return cerrors.Newf(cerrors.NotFound, "rule %q not found", ruleARN)
	}

	return nil
}

// DescribeRules returns all path rules for the specified listener.
func (m *Mock) DescribeRules(_ context.Context, listenerARN string) ([]driver.RuleInfo, error) {
	if _, ok := m.listeners.Get(listenerARN); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "listener %q not found", listenerARN)
	}

	return filterToSlice(m.rules, func(_ string, r driver.RuleInfo) bool {
		return r.ListenerARN == listenerARN
	}), nil
}

// ModifyListener modifies an existing URL map listener's port, protocol, or default actions.
func (m *Mock) ModifyListener(_ context.Context, input driver.ModifyListenerInput) error {
	li, ok := m.listeners.Get(input.ListenerARN)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "listener %q not found", input.ListenerARN)
	}

	if input.Port != 0 {
		li.Port = input.Port
	}

	if input.Protocol != "" {
		li.Protocol = input.Protocol
	}

	if len(input.DefaultActions) > 0 {
		li.TargetGroupARN = input.DefaultActions[0].TargetGroupARN
	}

	m.listeners.Set(input.ListenerARN, li)

	return nil
}

// GetLBAttributes returns the attributes for a load balancer.
func (m *Mock) GetLBAttributes(_ context.Context, lbARN string) (*driver.LBAttributes, error) {
	if _, ok := m.lbs.Get(lbARN); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "load balancer %q not found", lbARN)
	}

	m.attrsMu.RLock()
	defer m.attrsMu.RUnlock()

	attrs, ok := m.attrs[lbARN]
	if !ok {
		attrs = driver.LBAttributes{IdleTimeout: defaultIdleTimeoutSec}
	}

	return &attrs, nil
}

// PutLBAttributes sets the attributes for a load balancer.
func (m *Mock) PutLBAttributes(_ context.Context, lbARN string, attrs driver.LBAttributes) error {
	if _, ok := m.lbs.Get(lbARN); !ok {
		return cerrors.Newf(cerrors.NotFound, "load balancer %q not found", lbARN)
	}

	m.attrsMu.Lock()
	m.attrs[lbARN] = attrs
	m.attrsMu.Unlock()

	return nil
}

// RegisterTargets adds instances to a backend service (target group).
func (m *Mock) RegisterTargets(_ context.Context, targetGroupARN string, targets []driver.Target) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return cerrors.Newf(cerrors.NotFound, "backend service %q not found", targetGroupARN)
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

// DeregisterTargets removes instances from a backend service (target group).
func (m *Mock) DeregisterTargets(_ context.Context, targetGroupARN string, targets []driver.Target) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return cerrors.Newf(cerrors.NotFound, "backend service %q not found", targetGroupARN)
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

// DescribeTargetHealth returns the health status of all instances in a backend service.
func (m *Mock) DescribeTargetHealth(_ context.Context, targetGroupARN string) ([]driver.TargetHealth, error) {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "backend service %q not found", targetGroupARN)
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

// SetTargetHealth sets the health state of a specific instance in a backend service.
func (m *Mock) SetTargetHealth(_ context.Context, targetGroupARN, targetID, state string) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return cerrors.Newf(cerrors.NotFound, "backend service %q not found", targetGroupARN)
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "no targets registered in backend service %q", targetGroupARN)
	}

	th, ok := tgHealth[targetID]
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "target %q not found in backend service %q", targetID, targetGroupARN)
	}

	th.State = state
	th.Reason = ""

	return nil
}
