package postgresflex

import (
	"bytes"
	"context"
	"net"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// validIPv4 reports whether s parses as an IPv4 address.
func validIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// ipv4LessOrEqual reports whether start <= end by unsigned 32-bit value. Both
// must already be valid IPv4 (checked by validIPv4).
func ipv4LessOrEqual(start, end string) bool {
	return bytes.Compare(net.ParseIP(start).To4(), net.ParseIP(end).To4()) <= 0
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
// Server parameter catalog mapped to its server default. Azure rejects
// SetConfiguration for a name outside the catalog with 404 (so the mock
// validates against this set rather than accept-and-echo any name), and returns
// the default via Get/List for a known-but-unset parameter rather than 404.
//
//nolint:gochecknoglobals // immutable parameter-name lookup table.
var knownServerParameters = map[string]string{
	"max_connections":                     "100",
	"shared_buffers":                      "32768",
	"work_mem":                            "4096",
	"maintenance_work_mem":                "65536",
	"effective_cache_size":                "524288",
	"effective_io_concurrency":            "1",
	"random_page_cost":                    "4",
	"log_statement":                       "none",
	"log_min_duration_statement":          "-1",
	"log_connections":                     "off",
	"log_disconnections":                  "off",
	"log_duration":                        "off",
	"log_lock_waits":                      "off",
	"log_checkpoints":                     "on",
	"autovacuum":                          "on",
	"autovacuum_max_workers":              "3",
	"autovacuum_naptime":                  "60",
	"autovacuum_vacuum_scale_factor":      "0.2",
	"autovacuum_analyze_scale_factor":     "0.1",
	"statement_timeout":                   "0",
	"lock_timeout":                        "0",
	"idle_in_transaction_session_timeout": "0",
	"timezone":                            "UTC",
	"datestyle":                           "ISO, MDY",
	"max_wal_size":                        "1024",
	"min_wal_size":                        "80",
	"wal_level":                           "replica",
	"wal_buffers":                         "-1",
	"checkpoint_timeout":                  "300",
	"checkpoint_completion_target":        "0.9",
	"max_prepared_transactions":           "0",
	"max_worker_processes":                "8",
	"max_parallel_workers":                "8",
	"max_parallel_workers_per_gather":     "2",
	"default_transaction_isolation":       "read committed",
	"deadlock_timeout":                    "1000",
	"temp_buffers":                        "8192",
	"max_locks_per_transaction":           "64",
	"ssl":                                 "on",
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
//
//nolint:gocritic // cfg matches the Databases capability interface signature.
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

	vals := m.databases.SortedValues()
	for i := range vals {
		if vals[i].Server == server {
			out = append(out, vals[i])
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

	if _, known := knownServerParameters[cfg.Name]; !known {
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
			Server:       cfg.Server,
			Name:         cfg.Name,
			DataType:     "String",
			DefaultValue: knownServerParameters[cfg.Name],
			ARN:          m.childARN(cfg.Server, "configurations", cfg.Name),
		}
	}

	conf.Value = cfg.Value
	conf.Source = "user-override"

	m.configurations.Set(key, conf)

	out := conf

	return &out, nil
}

// GetConfiguration returns a server parameter — the user override if one was
// set, otherwise the catalog default for a known parameter (real Azure returns
// the system default for an unset-but-valid parameter). Unknown parameters 404.
func (m *Mock) GetConfiguration(_ context.Context, server, name string) (*rdsdriver.Configuration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if conf, ok := m.configurations.Get(childKey(server, name)); ok {
		out := conf

		return &out, nil
	}

	def, known := knownServerParameters[name]
	if !known {
		return nil, cerrors.Newf(cerrors.NotFound, "configuration %q not found", name)
	}

	return m.defaultConfiguration(server, name, def), nil
}

// defaultConfiguration builds the system-default view of a known parameter.
func (m *Mock) defaultConfiguration(server, name, value string) *rdsdriver.Configuration {
	return &rdsdriver.Configuration{
		Server:       server,
		Name:         name,
		Value:        value,
		Source:       "system-default",
		DataType:     "String",
		DefaultValue: value,
		ARN:          m.childARN(server, "configurations", name),
	}
}

// ListConfigurations returns the full parameter catalog, with user overrides
// applied where present (real Azure lists the catalog with defaults, not just
// the parameters that have been written).
func (m *Mock) ListConfigurations(_ context.Context, server string) ([]rdsdriver.Configuration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireServer(server); err != nil {
		return nil, err
	}

	overrides := make(map[string]rdsdriver.Configuration)

	confs := m.configurations.SortedValues()
	for i := range confs {
		if confs[i].Server == server {
			overrides[confs[i].Name] = confs[i]
		}
	}

	names := make([]string, 0, len(knownServerParameters))
	for name := range knownServerParameters {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]rdsdriver.Configuration, 0, len(names))

	for _, name := range names {
		if conf, ok := overrides[name]; ok {
			out = append(out, conf)
			continue
		}

		out = append(out, *m.defaultConfiguration(server, name, knownServerParameters[name]))
	}

	return out, nil
}
