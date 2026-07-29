package rds

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.DBProxies = (*Mock)(nil)

const defaultTargetGroup = "default"

func proxyARN(region, accountID, name string) string {
	return idgen.AWSARN("rds", region, accountID, "db-proxy:"+name)
}

func proxyTargetGroupARN(region, accountID, proxy string) string {
	return idgen.AWSARN("rds", region, accountID, "target-group:"+proxy+"/"+defaultTargetGroup)
}

func errProxyNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "DB proxy %q not found", name)
}

//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateDBProxy(_ context.Context, cfg rdsdriver.DBProxyConfig) (*rdsdriver.DBProxy, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBProxyName is required")
	}

	if cfg.EngineFamily == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "EngineFamily is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.proxies.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB proxy %q already exists", cfg.Name)
	}

	proxy := rdsdriver.DBProxy{
		Name:                cfg.Name,
		ARN:                 proxyARN(m.opts.Region, m.opts.AccountID, cfg.Name),
		Status:              "available",
		EngineFamily:        cfg.EngineFamily,
		RoleARN:             cfg.RoleARN,
		Endpoint:            fmt.Sprintf("%s.proxy-abcd1234.%s.rds.amazonaws.com", cfg.Name, m.opts.Region),
		VPCSubnetIDs:        append([]string(nil), cfg.VPCSubnetIDs...),
		VPCSecurityGroupIDs: append([]string(nil), cfg.VPCSecurityGroupIDs...),
		RequireTLS:          cfg.RequireTLS,
		IdleClientTimeout:   cfg.IdleClientTimeout,
		DebugLogging:        cfg.DebugLogging,
		Auth:                append([]rdsdriver.ProxyAuth(nil), cfg.Auth...),
		CreatedAt:           m.opts.Clock.Now().UTC(),
	}
	m.proxies.Set(cfg.Name, proxy)

	out := proxy

	return &out, nil
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) DescribeDBProxies(_ context.Context, names []string) ([]rdsdriver.DBProxy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		return m.proxies.SortedValues(), nil
	}

	out := make([]rdsdriver.DBProxy, 0, len(names))

	for _, name := range names {
		p, ok := m.proxies.Get(name)
		if !ok {
			return nil, errProxyNotFound(name)
		}

		out = append(out, p)
	}

	return out, nil
}

func (m *Mock) ModifyDBProxy(_ context.Context, name string, input rdsdriver.ModifyDBProxyInput) (*rdsdriver.DBProxy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.proxies.Get(name)
	if !ok {
		return nil, errProxyNotFound(name)
	}

	if input.RequireTLS != nil {
		p.RequireTLS = *input.RequireTLS
	}

	if input.IdleClientTimeout != nil {
		p.IdleClientTimeout = *input.IdleClientTimeout
	}

	if input.DebugLogging != nil {
		p.DebugLogging = *input.DebugLogging
	}

	if input.RoleARN != "" {
		p.RoleARN = input.RoleARN
	}

	m.proxies.Set(name, p)

	out := p

	return &out, nil
}

func (m *Mock) DeleteDBProxy(_ context.Context, name string) (*rdsdriver.DBProxy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.proxies.Get(name)
	if !ok {
		return nil, errProxyNotFound(name)
	}

	p.Status = "deleting"

	m.proxies.Delete(name)

	out := p

	return &out, nil
}

func (m *Mock) RegisterDBProxyTargets(
	_ context.Context, name, _ string, instanceIDs, clusterIDs []string,
) ([]rdsdriver.ProxyTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.proxies.Get(name)
	if !ok {
		return nil, errProxyNotFound(name)
	}

	added := make([]rdsdriver.ProxyTarget, 0, len(instanceIDs)+len(clusterIDs))

	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB instance %q not found", id)
		}

		t := rdsdriver.ProxyTarget{Type: "RDS_INSTANCE", RDSResourceID: id, Endpoint: inst.Endpoint, Port: inst.Port}
		p.Targets = append(p.Targets, t)
		added = append(added, t)
	}

	for _, id := range clusterIDs {
		cluster, ok := m.clusters.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", id)
		}

		t := rdsdriver.ProxyTarget{Type: "TRACKED_CLUSTER", RDSResourceID: id, Endpoint: cluster.Endpoint, Port: cluster.Port}
		p.Targets = append(p.Targets, t)
		added = append(added, t)
	}

	m.proxies.Set(name, p)

	return added, nil
}

func (m *Mock) DeregisterDBProxyTargets(_ context.Context, name, _ string, instanceIDs, clusterIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.proxies.Get(name)
	if !ok {
		return errProxyNotFound(name)
	}

	drop := stringSet(append(append([]string(nil), instanceIDs...), clusterIDs...))

	kept := p.Targets[:0]

	for _, t := range p.Targets {
		if _, remove := drop[t.RDSResourceID]; !remove {
			kept = append(kept, t)
		}
	}

	p.Targets = kept
	m.proxies.Set(name, p)

	return nil
}

func (m *Mock) DescribeDBProxyTargets(_ context.Context, name, _ string) ([]rdsdriver.ProxyTarget, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.proxies.Get(name)
	if !ok {
		return nil, errProxyNotFound(name)
	}

	return append([]rdsdriver.ProxyTarget(nil), p.Targets...), nil
}

func (m *Mock) DescribeDBProxyTargetGroups(_ context.Context, name string) ([]rdsdriver.ProxyTargetGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.proxies.Has(name) {
		return nil, errProxyNotFound(name)
	}

	return []rdsdriver.ProxyTargetGroup{{
		Name:      defaultTargetGroup,
		ProxyName: name,
		ARN:       proxyTargetGroupARN(m.opts.Region, m.opts.AccountID, name),
		IsDefault: true,
	}}, nil
}
