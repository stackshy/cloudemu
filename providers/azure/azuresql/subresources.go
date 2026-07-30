package azuresql

import (
	"bytes"
	"context"
	"net"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// validIPv4 reports whether s parses as an IPv4 address. Azure SQL firewall
// rules require IPv4 start/end addresses.
func validIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// ipv4LessOrEqual reports whether start <= end by unsigned 32-bit value. Both
// must already be valid IPv4 (checked by validIPv4).
func ipv4LessOrEqual(start, end string) bool {
	return bytes.Compare(net.ParseIP(start).To4(), net.ParseIP(end).To4()) <= 0
}

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

// elasticPoolName extracts the pool name from an elasticPoolId, which may be a
// bare name or a full ARM resource ID ending in ".../elasticPools/{name}".
func elasticPoolName(id string) string {
	if i := strings.LastIndex(id, "/elasticPools/"); i >= 0 {
		return id[i+len("/elasticPools/"):]
	}

	return id
}

// requireElasticPool returns NotFound when a non-empty elastic-pool reference
// doesn't resolve to an existing pool on the server. Empty id is a no-op (a
// standalone database). Callers hold the write lock.
func (m *Mock) requireElasticPool(server, poolID string) error {
	if poolID == "" {
		return nil
	}

	name := elasticPoolName(poolID)
	if _, ok := m.elasticPools.Get(subKey(server, name)); !ok {
		return cerrors.Newf(cerrors.NotFound, "elastic pool %q not found on server %q", name, server)
	}

	return nil
}

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

	if !validIPv4(cfg.StartIPAddress) || !validIPv4(cfg.EndIPAddress) {
		return nil, cerrors.New(cerrors.InvalidArgument, "startIpAddress and endIpAddress must be valid IPv4 addresses")
	}

	if !ipv4LessOrEqual(cfg.StartIPAddress, cfg.EndIPAddress) {
		return nil, cerrors.New(cerrors.InvalidArgument, "startIpAddress must be less than or equal to endIpAddress")
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

// DeleteElasticPool removes an elastic pool. Like real Azure, it fails with a
// precondition error while the pool still contains databases.
func (m *Mock) DeleteElasticPool(_ context.Context, server, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.elasticPools.Get(subKey(server, name)); !ok {
		return cerrors.Newf(cerrors.NotFound, "elastic pool %q not found", name)
	}

	suffix := "/elasticPools/" + name

	insts := m.instances.SortedValues()
	for i := range insts {
		if insts[i].ClusterID != server {
			continue
		}

		if insts[i].ElasticPoolID == name || strings.HasSuffix(insts[i].ElasticPoolID, suffix) {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"elastic pool %q cannot be deleted while it contains databases", name)
		}
	}

	m.elasticPools.Delete(subKey(server, name))

	return nil
}

// UpdateElasticPool applies the non-zero fields of cfg to an existing pool
// (PATCH merge semantics), leaving unspecified fields untouched.
//
//nolint:gocritic // cfg matches the ElasticPools capability interface signature.
func (m *Mock) UpdateElasticPool(_ context.Context, cfg rdsdriver.ElasticPoolConfig) (*rdsdriver.ElasticPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := subKey(cfg.Server, cfg.Name)

	pool, ok := m.elasticPools.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "elastic pool %q not found", cfg.Name)
	}

	if cfg.Location != "" {
		pool.Location = cfg.Location
	}

	if cfg.SKUName != "" {
		pool.SKUName = cfg.SKUName
	}

	if cfg.SKUTier != "" {
		pool.SKUTier = cfg.SKUTier
	}

	if cfg.MaxSizeBytes != 0 {
		pool.MaxSizeBytes = cfg.MaxSizeBytes
	}

	if cfg.MinCapacity != 0 {
		pool.MinCapacity = cfg.MinCapacity
	}

	if cfg.MaxCapacity != 0 {
		pool.MaxCapacity = cfg.MaxCapacity
	}

	m.elasticPools.Set(key, pool)

	out := pool

	return &out, nil
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

	// A failover only makes sense when there is a partner server to promote to;
	// otherwise a standalone group would ping-pong between Primary and Secondary
	// and leave a Secondary with no Primary.
	if len(fg.PartnerServers) == 0 {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"failover group %q has no partner server to fail over to", name)
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

// UpdateFailoverGroup applies the non-zero fields of cfg to an existing group
// (PATCH merge semantics). Partner/database lists are replaced only when the
// PATCH supplies them.
//
//nolint:gocritic // cfg matches the FailoverGroups capability interface signature.
func (m *Mock) UpdateFailoverGroup(
	_ context.Context, cfg rdsdriver.FailoverGroupConfig,
) (*rdsdriver.FailoverGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := subKey(cfg.Server, cfg.Name)

	fg, ok := m.failoverGroups.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "failover group %q not found", cfg.Name)
	}

	if cfg.FailoverPolicy != "" {
		fg.FailoverPolicy = cfg.FailoverPolicy
	}

	if cfg.GracePeriodMinutes != 0 {
		fg.GracePeriodMinutes = cfg.GracePeriodMinutes
	}

	if len(cfg.PartnerServers) > 0 {
		fg.PartnerServers = cloneStrings(cfg.PartnerServers)
	} else {
		fg.PartnerServers = cloneStrings(fg.PartnerServers)
	}

	if len(cfg.Databases) > 0 {
		fg.Databases = cloneStrings(cfg.Databases)
	} else {
		fg.Databases = cloneStrings(fg.Databases)
	}

	m.failoverGroups.Set(key, fg)

	return copyFailoverGroup(fg), nil
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
