// Package gcplb provides an in-memory mock implementation of GCP Cloud Load Balancing.
package gcplb

import (
	"context"
	"fmt"
	"sync"

	"github.com/NitinKumar004/cloudemu/config"
	cerrors "github.com/NitinKumar004/cloudemu/errors"
	"github.com/NitinKumar004/cloudemu/internal/idgen"
	"github.com/NitinKumar004/cloudemu/internal/memstore"
	"github.com/NitinKumar004/cloudemu/loadbalancer/driver"
)

// Compile-time check that Mock implements driver.LoadBalancer.
var _ driver.LoadBalancer = (*Mock)(nil)

// Mock is an in-memory mock implementation of the GCP Cloud Load Balancing service.
type Mock struct {
	lbs       *memstore.Store[driver.LBInfo]
	tgs       *memstore.Store[driver.TargetGroupInfo]
	listeners *memstore.Store[driver.ListenerInfo]
	opts      *config.Options

	healthMu sync.RWMutex
	health   map[string]map[string]*driver.TargetHealth // tgARN -> targetID -> health
}

// New creates a new Cloud Load Balancing mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		lbs:       memstore.New[driver.LBInfo](),
		tgs:       memstore.New[driver.TargetGroupInfo](),
		listeners: memstore.New[driver.ListenerInfo](),
		opts:      opts,
		health:    make(map[string]map[string]*driver.TargetHealth),
	}
}

// CreateLoadBalancer creates a new forwarding rule (load balancer).
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
	if len(arns) == 0 {
		all := m.lbs.All()
		results := make([]driver.LBInfo, 0, len(all))
		for _, lb := range all {
			results = append(results, lb)
		}
		return results, nil
	}

	results := make([]driver.LBInfo, 0, len(arns))
	for _, arn := range arns {
		lb, ok := m.lbs.Get(arn)
		if !ok {
			continue
		}
		results = append(results, lb)
	}

	return results, nil
}

// CreateTargetGroup creates a new backend service (target group).
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
	if len(arns) == 0 {
		all := m.tgs.All()
		results := make([]driver.TargetGroupInfo, 0, len(all))
		for _, tg := range all {
			results = append(results, tg)
		}
		return results, nil
	}

	results := make([]driver.TargetGroupInfo, 0, len(arns))
	for _, arn := range arns {
		tg, ok := m.tgs.Get(arn)
		if !ok {
			continue
		}
		results = append(results, tg)
	}

	return results, nil
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

	filtered := m.listeners.Filter(func(_ string, li driver.ListenerInfo) bool {
		return li.LBARN == lbARN
	})

	results := make([]driver.ListenerInfo, 0, len(filtered))
	for _, li := range filtered {
		results = append(results, li)
	}

	return results, nil
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
func (m *Mock) SetTargetHealth(_ context.Context, targetGroupARN string, targetID string, state string) error {
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
