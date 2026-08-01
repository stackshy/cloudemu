package cosmospostgresql

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

// configCatalog is the fixed set of well-known server parameters the mock
// exposes, with their default coordinator/node values. Real Cosmos DB for
// PostgreSQL surfaces hundreds; the mock models a representative subset so the
// Get/List/Update surface round-trips faithfully.
//
//nolint:gochecknoglobals // static server-parameter catalog
var configCatalog = map[string]struct {
	dataType     string
	defaultValue string
	allowed      string
	requiresRest bool
	description  string
}{
	"array_nulls":         {"Boolean", "on", "on,off", false, "Enable input of NULL elements in arrays."},
	"max_connections":     {"Integer", "300", "25-3000", true, "Maximum concurrent connections."},
	"citus.node_conninfo": {"String", "sslmode=require", "", false, "libpq connection parameters used between nodes."},
	"work_mem":            {"Integer", "4096", "64-2097151", false, "Memory for internal sort/hash operations (KB)."},
}

func catalogNames() []string {
	names := make([]string, 0, len(configCatalog))
	for name := range configCatalog {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

func configKey(rg, cluster, role, name string) string {
	return rg + "/" + cluster + "/" + role + "/" + name
}

// storedOrDefault returns the coordinator/node value for a parameter: an
// operator-set override if present, else the catalog default. The caller holds
// a read lock.
func (m *Mock) storedOrDefault(rg, cluster, role, name string) (value, source string) {
	entry := configCatalog[name]

	if sc, ok := m.serverConfigs.Get(configKey(rg, cluster, role, name)); ok {
		return sc.Value, "user-override"
	}

	return entry.defaultValue, "system-default"
}

// ListConfigurations returns the cluster-wide parameters with per-role values.
func (m *Mock) ListConfigurations(_ context.Context, rg, cluster string) ([]cpgdriver.Configuration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	names := catalogNames()
	out := make([]cpgdriver.Configuration, 0, len(names))

	for _, name := range names {
		out = append(out, m.configuration(rg, cluster, name))
	}

	return out, nil
}

// GetConfiguration returns a single cluster-wide parameter.
func (m *Mock) GetConfiguration(_ context.Context, rg, cluster, name string) (*cpgdriver.Configuration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	if _, ok := configCatalog[name]; !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "configuration %q not found", name)
	}

	c := m.configuration(rg, cluster, name)

	return &c, nil
}

func (m *Mock) configuration(rg, cluster, name string) cpgdriver.Configuration {
	entry := configCatalog[name]

	coordVal, coordSrc := m.storedOrDefault(rg, cluster, cpgdriver.RoleCoordinator, name)
	nodeVal, nodeSrc := m.storedOrDefault(rg, cluster, cpgdriver.RoleWorker, name)

	return cpgdriver.Configuration{
		Name:              name,
		ClusterName:       cluster,
		ResourceGroup:     rg,
		ProvisioningState: cpgdriver.ProvisioningSucceeded,
		Description:       entry.description,
		DataType:          entry.dataType,
		AllowedValues:     entry.allowed,
		RequiresRestart:   entry.requiresRest,
		RoleGroups: []cpgdriver.RoleGroupValue{
			{Role: cpgdriver.RoleCoordinator, Value: coordVal, DefaultValue: entry.defaultValue, Source: coordSrc},
			{Role: cpgdriver.RoleWorker, Value: nodeVal, DefaultValue: entry.defaultValue, Source: nodeSrc},
		},
	}
}

// GetCoordinatorConfiguration returns a parameter's coordinator-role value.
func (m *Mock) GetCoordinatorConfiguration(_ context.Context, rg, cluster, name string) (*cpgdriver.ServerConfiguration, error) {
	return m.getServerConfig(rg, cluster, cpgdriver.RoleCoordinator, name)
}

// GetNodeConfiguration returns a parameter's node-role value.
func (m *Mock) GetNodeConfiguration(_ context.Context, rg, cluster, name string) (*cpgdriver.ServerConfiguration, error) {
	return m.getServerConfig(rg, cluster, cpgdriver.RoleWorker, name)
}

func (m *Mock) getServerConfig(rg, cluster, role, name string) (*cpgdriver.ServerConfiguration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	entry, ok := configCatalog[name]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "configuration %q not found", name)
	}

	value, source := m.storedOrDefault(rg, cluster, role, name)

	return &cpgdriver.ServerConfiguration{
		Name:              name,
		ClusterName:       cluster,
		ResourceGroup:     rg,
		ServerName:        serverName(cluster, role, 0),
		ProvisioningState: cpgdriver.ProvisioningSucceeded,
		Value:             value,
		DefaultValue:      entry.defaultValue,
		Description:       entry.description,
		DataType:          entry.dataType,
		AllowedValues:     entry.allowed,
		Source:            source,
		RequiresRestart:   entry.requiresRest,
	}, nil
}

// ListServerConfigurations returns the parameters for a specific node.
func (m *Mock) ListServerConfigurations(_ context.Context, rg, cluster, server string) ([]cpgdriver.ServerConfiguration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	role := cpgdriver.RoleWorker
	if strings.HasSuffix(server, "-c") {
		role = cpgdriver.RoleCoordinator
	}

	names := catalogNames()
	out := make([]cpgdriver.ServerConfiguration, 0, len(names))

	for _, name := range names {
		entry := configCatalog[name]
		value, source := m.storedOrDefault(rg, cluster, role, name)
		out = append(out, cpgdriver.ServerConfiguration{
			Name:              name,
			ClusterName:       cluster,
			ResourceGroup:     rg,
			ServerName:        server,
			ProvisioningState: cpgdriver.ProvisioningSucceeded,
			Value:             value,
			DefaultValue:      entry.defaultValue,
			Description:       entry.description,
			DataType:          entry.dataType,
			AllowedValues:     entry.allowed,
			Source:            source,
			RequiresRestart:   entry.requiresRest,
		})
	}

	return out, nil
}

// UpdateCoordinatorConfiguration sets a parameter's coordinator-role value.
func (m *Mock) UpdateCoordinatorConfiguration(_ context.Context, rg, cluster, name, value string) (*cpgdriver.ServerConfiguration, error) {
	return m.updateServerConfig(rg, cluster, cpgdriver.RoleCoordinator, name, value)
}

// UpdateNodeConfiguration sets a parameter's node-role value.
func (m *Mock) UpdateNodeConfiguration(_ context.Context, rg, cluster, name, value string) (*cpgdriver.ServerConfiguration, error) {
	return m.updateServerConfig(rg, cluster, cpgdriver.RoleWorker, name, value)
}

func (m *Mock) updateServerConfig(rg, cluster, role, name, value string) (*cpgdriver.ServerConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	entry, ok := configCatalog[name]
	if !ok {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unknown configuration %q", name)
	}

	sc := cpgdriver.ServerConfiguration{
		Name:              name,
		ClusterName:       cluster,
		ResourceGroup:     rg,
		ServerName:        serverName(cluster, role, 0),
		ProvisioningState: cpgdriver.ProvisioningSucceeded,
		Value:             value,
		DefaultValue:      entry.defaultValue,
		Description:       entry.description,
		DataType:          entry.dataType,
		AllowedValues:     entry.allowed,
		Source:            "user-override",
		RequiresRestart:   entry.requiresRest,
	}
	m.serverConfigs.Set(configKey(rg, cluster, role, name), sc)

	out := sc

	return &out, nil
}
