package azuresql

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Azure SQL exposes firewall rules, virtual-network rules, elastic pools,
// failover groups and an Azure AD administrator as server child resources.
// These are optional relationaldb driver capabilities discovered by the ARM
// handler via type assertion.
var (
	_ rdsdriver.FirewallRules  = (*Mock)(nil)
	_ rdsdriver.VNetRules      = (*Mock)(nil)
	_ rdsdriver.ElasticPools   = (*Mock)(nil)
	_ rdsdriver.FailoverGroups = (*Mock)(nil)
	_ rdsdriver.AADAdmins      = (*Mock)(nil)
)

const (
	aadAdminName  = "ActiveDirectory"
	rolePrimary   = "Primary"
	roleSecondary = "Secondary"
)

func subKey(server, name string) string { return server + "/" + name }

func (m *Mock) childARN(server, subType, name string) string {
	return idgen.AzureID(m.opts.Region, m.opts.Region, armProvider, "servers/"+server+"/"+subType, name)
}

func (m *Mock) requireServer(server string) error {
	if _, ok := m.clusters.Get(server); !ok {
		return cerrors.Newf(cerrors.NotFound, "Azure SQL server %q not found", server)
	}

	return nil
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	return append([]string(nil), s...)
}

// ---- Firewall rules ----

// CreateFirewallRule creates or replaces a server firewall rule.
func (m *Mock) CreateFirewallRule(
	_ context.Context, cfg rdsdriver.FirewallRuleConfig,
) (*rdsdriver.FirewallRule, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "firewall rule name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireServer(cfg.Server); err != nil {
		return nil, err
	}

	rule := rdsdriver.FirewallRule{
		Server:         cfg.Server,
		Name:           cfg.Name,
		StartIPAddress: cfg.StartIPAddress,
		EndIPAddress:   cfg.EndIPAddress,
		ARN:            m.childARN(cfg.Server, "firewallRules", cfg.Name),
	}

	m.firewallRules.Set(subKey(cfg.Server, cfg.Name), rule)

	out := rule

	return &out, nil
}

// GetFirewallRule returns a single firewall rule.
func (m *Mock) GetFirewallRule(_ context.Context, server, name string) (*rdsdriver.FirewallRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rule, ok := m.firewallRules.Get(subKey(server, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", name)
	}

	out := rule

	return &out, nil
}

// ListFirewallRules returns all firewall rules on a server.
func (m *Mock) ListFirewallRules(_ context.Context, server string) ([]rdsdriver.FirewallRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireServer(server); err != nil {
		return nil, err
	}

	out := []rdsdriver.FirewallRule{}

	for _, rule := range m.firewallRules.SortedValues() {
		if rule.Server == server {
			out = append(out, rule)
		}
	}

	return out, nil
}

// DeleteFirewallRule removes a firewall rule.
func (m *Mock) DeleteFirewallRule(_ context.Context, server, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.firewallRules.Delete(subKey(server, name)) {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", name)
	}

	return nil
}

// ---- Virtual network rules ----

// CreateVNetRule creates or replaces a virtual-network rule.
func (m *Mock) CreateVNetRule(_ context.Context, cfg rdsdriver.VNetRuleConfig) (*rdsdriver.VNetRule, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "vnet rule name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireServer(cfg.Server); err != nil {
		return nil, err
	}

	rule := rdsdriver.VNetRule{
		Server:                cfg.Server,
		Name:                  cfg.Name,
		SubnetID:              cfg.SubnetID,
		IgnoreMissingEndpoint: cfg.IgnoreMissingEndpoint,
		State:                 "Ready",
		ARN:                   m.childARN(cfg.Server, "virtualNetworkRules", cfg.Name),
	}

	m.vnetRules.Set(subKey(cfg.Server, cfg.Name), rule)

	out := rule

	return &out, nil
}

// GetVNetRule returns a single virtual-network rule.
func (m *Mock) GetVNetRule(_ context.Context, server, name string) (*rdsdriver.VNetRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rule, ok := m.vnetRules.Get(subKey(server, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "vnet rule %q not found", name)
	}

	out := rule

	return &out, nil
}

// ListVNetRules returns all virtual-network rules on a server.
func (m *Mock) ListVNetRules(_ context.Context, server string) ([]rdsdriver.VNetRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireServer(server); err != nil {
		return nil, err
	}

	out := []rdsdriver.VNetRule{}

	for _, rule := range m.vnetRules.SortedValues() {
		if rule.Server == server {
			out = append(out, rule)
		}
	}

	return out, nil
}

// DeleteVNetRule removes a virtual-network rule.
func (m *Mock) DeleteVNetRule(_ context.Context, server, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.vnetRules.Delete(subKey(server, name)) {
		return cerrors.Newf(cerrors.NotFound, "vnet rule %q not found", name)
	}

	return nil
}

// ---- Elastic pools ----

// CreateElasticPool creates or replaces an elastic pool.
//
//nolint:gocritic // cfg matches the ElasticPools capability interface signature.
func (m *Mock) CreateElasticPool(_ context.Context, cfg rdsdriver.ElasticPoolConfig) (*rdsdriver.ElasticPool, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "elastic pool name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireServer(cfg.Server); err != nil {
		return nil, err
	}

	location := cfg.Location
	if location == "" {
		location = m.opts.Region
	}

	pool := rdsdriver.ElasticPool{
		Server:       cfg.Server,
		Name:         cfg.Name,
		Location:     location,
		SKUName:      cfg.SKUName,
		SKUTier:      cfg.SKUTier,
		MaxSizeBytes: cfg.MaxSizeBytes,
		MinCapacity:  cfg.MinCapacity,
		MaxCapacity:  cfg.MaxCapacity,
		State:        "Ready",
		ARN:          m.childARN(cfg.Server, "elasticPools", cfg.Name),
	}

	m.elasticPools.Set(subKey(cfg.Server, cfg.Name), pool)

	m.emitElasticPoolMetrics(cfg.Server, cfg.Name)

	out := pool

	return &out, nil
}

// emitElasticPoolMetrics pushes a representative datapoint set on the
// Microsoft.Sql/servers/elasticpools namespace, matching the pool-scoped
// metrics real Azure Monitor surfaces.
func (m *Mock) emitElasticPoolMetrics(server, name string) {
	if m.monitoring == nil {
		return
	}

	const ns = "Microsoft.Sql/servers/elasticpools"

	now := m.opts.Clock.Now()
	dims := map[string]string{"resourceId": m.childARN(server, "elasticPools", name)}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: ns, MetricName: "cpu_percent", Value: 25, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "storage_percent", Value: 25, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: ns, MetricName: "workers_percent", Value: 10, Unit: "Percent", Dimensions: dims, Timestamp: now},
	})
}

// GetElasticPool returns a single elastic pool.
func (m *Mock) GetElasticPool(_ context.Context, server, name string) (*rdsdriver.ElasticPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pool, ok := m.elasticPools.Get(subKey(server, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "elastic pool %q not found", name)
	}

	out := pool

	return &out, nil
}

// ListElasticPools returns all elastic pools on a server.
func (m *Mock) ListElasticPools(_ context.Context, server string) ([]rdsdriver.ElasticPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireServer(server); err != nil {
		return nil, err
	}

	out := []rdsdriver.ElasticPool{}

	pools := m.elasticPools.SortedValues()
	for i := range pools {
		if pools[i].Server == server {
			out = append(out, pools[i])
		}
	}

	return out, nil
}

// DeleteElasticPool removes an elastic pool.
func (m *Mock) DeleteElasticPool(_ context.Context, server, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.elasticPools.Delete(subKey(server, name)) {
		return cerrors.Newf(cerrors.NotFound, "elastic pool %q not found", name)
	}

	return nil
}

// ---- Failover groups ----

// CreateFailoverGroup creates or replaces a failover group with the local
// server as primary.
//
//nolint:gocritic // cfg matches the FailoverGroups capability interface signature.
func (m *Mock) CreateFailoverGroup(
	_ context.Context, cfg rdsdriver.FailoverGroupConfig,
) (*rdsdriver.FailoverGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "failover group name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireServer(cfg.Server); err != nil {
		return nil, err
	}

	fg := rdsdriver.FailoverGroup{
		Server:             cfg.Server,
		Name:               cfg.Name,
		FailoverPolicy:     cfg.FailoverPolicy,
		GracePeriodMinutes: cfg.GracePeriodMinutes,
		PartnerServers:     cloneStrings(cfg.PartnerServers),
		Databases:          cloneStrings(cfg.Databases),
		ReplicationRole:    rolePrimary,
		ARN:                m.childARN(cfg.Server, "failoverGroups", cfg.Name),
	}

	m.failoverGroups.Set(subKey(cfg.Server, cfg.Name), fg)

	return copyFailoverGroup(fg), nil
}

// GetFailoverGroup returns a single failover group.
func (m *Mock) GetFailoverGroup(_ context.Context, server, name string) (*rdsdriver.FailoverGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fg, ok := m.failoverGroups.Get(subKey(server, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "failover group %q not found", name)
	}

	return copyFailoverGroup(fg), nil
}

// ListFailoverGroups returns all failover groups on a server.
func (m *Mock) ListFailoverGroups(_ context.Context, server string) ([]rdsdriver.FailoverGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireServer(server); err != nil {
		return nil, err
	}

	out := []rdsdriver.FailoverGroup{}

	fgs := m.failoverGroups.SortedValues()
	for i := range fgs {
		if fgs[i].Server == server {
			out = append(out, *copyFailoverGroup(fgs[i]))
		}
	}

	return out, nil
}

// DeleteFailoverGroup removes a failover group.
func (m *Mock) DeleteFailoverGroup(_ context.Context, server, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.failoverGroups.Delete(subKey(server, name)) {
		return cerrors.Newf(cerrors.NotFound, "failover group %q not found", name)
	}

	return nil
}

// FailoverFailoverGroup flips the local replication role between Primary and
// Secondary, modeling a planned failover.
func (m *Mock) FailoverFailoverGroup(_ context.Context, server, name string) (*rdsdriver.FailoverGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := subKey(server, name)

	fg, ok := m.failoverGroups.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "failover group %q not found", name)
	}

	if fg.ReplicationRole == rolePrimary {
		fg.ReplicationRole = roleSecondary
	} else {
		fg.ReplicationRole = rolePrimary
	}

	fg.PartnerServers = cloneStrings(fg.PartnerServers)
	fg.Databases = cloneStrings(fg.Databases)
	m.failoverGroups.Set(key, fg)

	return copyFailoverGroup(fg), nil
}

//nolint:gocritic // fg is copied by value to produce an isolated result.
func copyFailoverGroup(fg rdsdriver.FailoverGroup) *rdsdriver.FailoverGroup {
	fg.PartnerServers = cloneStrings(fg.PartnerServers)
	fg.Databases = cloneStrings(fg.Databases)

	return &fg
}

// ---- Azure AD administrator ----

// SetAADAdmin sets the server's Azure AD administrator (there is at most one).
func (m *Mock) SetAADAdmin(_ context.Context, cfg rdsdriver.AADAdminConfig) (*rdsdriver.AADAdmin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireServer(cfg.Server); err != nil {
		return nil, err
	}

	admin := rdsdriver.AADAdmin{
		Server:   cfg.Server,
		Name:     aadAdminName,
		Login:    cfg.Login,
		SID:      cfg.SID,
		TenantID: cfg.TenantID,
		ARN:      m.childARN(cfg.Server, "administrators", aadAdminName),
	}

	m.aadAdmins.Set(cfg.Server, admin)

	out := admin

	return &out, nil
}

// GetAADAdmin returns the server's Azure AD administrator.
func (m *Mock) GetAADAdmin(_ context.Context, server, _ string) (*rdsdriver.AADAdmin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	admin, ok := m.aadAdmins.Get(server)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Azure AD administrator not set on server %q", server)
	}

	out := admin

	return &out, nil
}

// ListAADAdmins returns the server's Azure AD administrator as a list (0 or 1).
func (m *Mock) ListAADAdmins(_ context.Context, server string) ([]rdsdriver.AADAdmin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireServer(server); err != nil {
		return nil, err
	}

	admin, ok := m.aadAdmins.Get(server)
	if !ok {
		return []rdsdriver.AADAdmin{}, nil
	}

	return []rdsdriver.AADAdmin{admin}, nil
}

// DeleteAADAdmin removes the server's Azure AD administrator.
func (m *Mock) DeleteAADAdmin(_ context.Context, server, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.aadAdmins.Delete(server) {
		return cerrors.Newf(cerrors.NotFound, "Azure AD administrator not set on server %q", server)
	}

	return nil
}
