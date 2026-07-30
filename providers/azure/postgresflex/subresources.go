package postgresflex

import (
	"context"
	"net"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// validIPv4 reports whether s parses as an IPv4 address.
func validIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// Postgres Flexible Server exposes databases, firewall rules and server
// configurations as child resources. These are optional relationaldb driver
// capabilities discovered by the ARM handler via type assertion. Unlike MySQL
// Flex, Postgres Flex has no failover action.
var (
	_ rdsdriver.Databases      = (*Mock)(nil)
	_ rdsdriver.FirewallRules  = (*Mock)(nil)
	_ rdsdriver.Configurations = (*Mock)(nil)
)

const (
	defaultCharset   = "UTF8"
	defaultCollation = "en_US.utf8"
)

// knownServerParameters is a representative subset of the PostgreSQL Flexible
// Server parameter catalog. Azure rejects SetConfiguration for a name outside
// the catalog with 404, so the mock validates against this set rather than
// accept-and-echo any name.
//
//nolint:gochecknoglobals // immutable parameter-name lookup table.
var knownServerParameters = map[string]bool{
	"max_connections":                     true,
	"shared_buffers":                      true,
	"work_mem":                            true,
	"maintenance_work_mem":                true,
	"effective_cache_size":                true,
	"log_statement":                       true,
	"log_min_duration_statement":          true,
	"autovacuum":                          true,
	"statement_timeout":                   true,
	"timezone":                            true,
	"max_wal_size":                        true,
	"wal_level":                           true,
	"max_prepared_transactions":           true,
	"idle_in_transaction_session_timeout": true,
}

func childKey(server, name string) string { return server + "/" + name }

func (m *Mock) childARN(server, subType, name string) string {
	return flexibleServerResourceID(m.opts.Region, server) + "/" + subType + "/" + name
}

// requireServer returns NotFound when server does not exist. Callers hold the
// lock appropriate to their operation.
func (m *Mock) requireServer(server string) error {
	if _, ok := m.instances.Get(server); !ok {
		return cerrors.Newf(cerrors.NotFound, "Postgres Flex server %q not found", server)
	}

	return nil
}

// ---- Databases ----

// CreateDatabase adds a logical database to a server.
func (m *Mock) CreateDatabase(_ context.Context, cfg rdsdriver.DatabaseConfig) (*rdsdriver.Database, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "database name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireServer(cfg.Server); err != nil {
		return nil, err
	}

	key := childKey(cfg.Server, cfg.Name)
	if _, ok := m.databases.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "database %q already exists", cfg.Name)
	}

	charset := cfg.Charset
	if charset == "" {
		charset = defaultCharset
	}

	collation := cfg.Collation
	if collation == "" {
		collation = defaultCollation
	}

	db := rdsdriver.Database{
		Server:    cfg.Server,
		Name:      cfg.Name,
		Charset:   charset,
		Collation: collation,
		ARN:       m.childARN(cfg.Server, "databases", cfg.Name),
	}

	m.databases.Set(key, db)

	out := db

	return &out, nil
}

// GetDatabase returns a single logical database.
func (m *Mock) GetDatabase(_ context.Context, server, name string) (*rdsdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	db, ok := m.databases.Get(childKey(server, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found", name)
	}

	out := db

	return &out, nil
}

// ListDatabases returns all logical databases in a server.
func (m *Mock) ListDatabases(_ context.Context, server string) ([]rdsdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireServer(server); err != nil {
		return nil, err
	}

	out := []rdsdriver.Database{}

	for _, db := range m.databases.SortedValues() {
		if db.Server == server {
			out = append(out, db)
		}
	}

	return out, nil
}

// DeleteDatabase removes a logical database.
func (m *Mock) DeleteDatabase(_ context.Context, server, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.databases.Delete(childKey(server, name)) {
		return cerrors.Newf(cerrors.NotFound, "database %q not found", name)
	}

	return nil
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

	m.firewallRules.Set(childKey(cfg.Server, cfg.Name), rule)

	out := rule

	return &out, nil
}

// GetFirewallRule returns a single firewall rule.
func (m *Mock) GetFirewallRule(_ context.Context, server, name string) (*rdsdriver.FirewallRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rule, ok := m.firewallRules.Get(childKey(server, name))
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

	if !m.firewallRules.Delete(childKey(server, name)) {
		return cerrors.Newf(cerrors.NotFound, "firewall rule %q not found", name)
	}

	return nil
}

// ---- Configurations (server parameters) ----

// SetConfiguration sets a server parameter value, recording it as a user
// override.
func (m *Mock) SetConfiguration(
	_ context.Context, cfg rdsdriver.ConfigurationConfig,
) (*rdsdriver.Configuration, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "configuration name is required")
	}

	if !knownServerParameters[cfg.Name] {
		return nil, cerrors.Newf(cerrors.NotFound, "unknown server parameter %q", cfg.Name)
	}

	if cfg.Value == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "configuration value is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireServer(cfg.Server); err != nil {
		return nil, err
	}

	key := childKey(cfg.Server, cfg.Name)

	conf, ok := m.configurations.Get(key)
	if !ok {
		conf = rdsdriver.Configuration{
			Server:   cfg.Server,
			Name:     cfg.Name,
			DataType: "String",
			ARN:      m.childARN(cfg.Server, "configurations", cfg.Name),
		}
	}

	conf.Value = cfg.Value
	conf.Source = "user-override"

	m.configurations.Set(key, conf)

	out := conf

	return &out, nil
}

// GetConfiguration returns a server parameter.
func (m *Mock) GetConfiguration(_ context.Context, server, name string) (*rdsdriver.Configuration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conf, ok := m.configurations.Get(childKey(server, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "configuration %q not found", name)
	}

	out := conf

	return &out, nil
}

// ListConfigurations returns the parameters that have been set on a server.
func (m *Mock) ListConfigurations(_ context.Context, server string) ([]rdsdriver.Configuration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireServer(server); err != nil {
		return nil, err
	}

	out := []rdsdriver.Configuration{}

	confs := m.configurations.SortedValues()
	for i := range confs {
		if confs[i].Server == server {
			out = append(out, confs[i])
		}
	}

	return out, nil
}
