// Package cosmospostgresql provides an in-memory mock of Azure Cosmos DB for
// PostgreSQL (Microsoft.DBforPostgreSQL/serverGroupsv2), the Citus-based
// distributed-Postgres offering. It models server-group clusters and their
// firewall rules, roles, derived nodes, server parameters (configurations),
// private-endpoint connections/links, the start/stop/restart lifecycle, and
// read-replica promotion.
package cosmospostgresql

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

const (
	providerNamespace = "Microsoft.DBforPostgreSQL"
	clusterType       = "serverGroupsv2"

	defaultCitusVersion      = "12.1"
	defaultPostgresqlVersion = "16"
	defaultServerEdition     = "GeneralPurpose"
	defaultCoordinatorVCores = 4
	defaultNodeVCores        = 4
	defaultStorageQuotaInMb  = 131072
	maxNodeCount             = 20
)

var _ cpgdriver.CosmosPostgreSQL = (*Mock)(nil)

// Mock is the in-memory Cosmos DB for PostgreSQL implementation. All stores are
// keyed by full resource path segments (rg[/cluster[/child]]).
type Mock struct {
	mu sync.RWMutex

	clusters      *memstore.Store[cpgdriver.Cluster]                   // key = "rg/name"
	firewallRules *memstore.Store[cpgdriver.FirewallRule]              // key = "rg/cluster/name"
	roles         *memstore.Store[cpgdriver.Role]                      // key = "rg/cluster/name"
	privateEPs    *memstore.Store[cpgdriver.PrivateEndpointConnection] // key = "rg/cluster/name"
	serverConfigs *memstore.Store[cpgdriver.ServerConfiguration]       // key = "rg/cluster/role/name"

	opts *config.Options
}

// New creates a new Cosmos DB for PostgreSQL mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:      memstore.New[cpgdriver.Cluster](),
		firewallRules: memstore.New[cpgdriver.FirewallRule](),
		roles:         memstore.New[cpgdriver.Role](),
		privateEPs:    memstore.New[cpgdriver.PrivateEndpointConnection](),
		serverConfigs: memstore.New[cpgdriver.ServerConfiguration](),
		opts:          opts,
	}
}

func clusterKey(rg, name string) string { return rg + "/" + name }

func childKey(rg, cluster, name string) string { return rg + "/" + cluster + "/" + name }

func validName(kind, name string) error {
	if name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name is required", kind)
	}

	if strings.Contains(name, "/") {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name %q must not contain '/'", kind, name)
	}

	return nil
}

func copyTags(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	return append([]string(nil), s...)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}

	return v
}

func cloneMaintenanceWindow(in *cpgdriver.MaintenanceWindow) *cpgdriver.MaintenanceWindow {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func cloneCluster(in *cpgdriver.Cluster) cpgdriver.Cluster {
	c := *in
	c.Tags = copyTags(c.Tags)
	c.ReadReplicas = cloneStrings(c.ReadReplicas)
	c.MaintenanceWindow = cloneMaintenanceWindow(c.MaintenanceWindow)

	return c
}

// clusterResourceID builds the ARM resource ID of a cluster in this mock's
// subscription (AccountID).
func (m *Mock) clusterResourceID(rg, name string) string {
	return "/subscriptions/" + m.opts.AccountID +
		"/resourceGroups/" + rg +
		"/providers/" + providerNamespace + "/" + clusterType + "/" + name
}

// CreateOrUpdateCluster creates or replaces a server-group cluster.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateOrUpdateCluster(_ context.Context, cfg cpgdriver.CreateClusterConfig) (*cpgdriver.Cluster, error) {
	if err := validName("cluster", cfg.Name); err != nil {
		return nil, err
	}

	if cfg.NodeCount < 0 || cfg.NodeCount > maxNodeCount {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "nodeCount must be between 0 and %d", maxNodeCount)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(cfg.ResourceGroup, cfg.Name)

	c := cpgdriver.Cluster{
		Name:                            cfg.Name,
		ResourceGroup:                   cfg.ResourceGroup,
		Location:                        cfg.Location,
		Tags:                            copyTags(cfg.Tags),
		ProvisioningState:               cpgdriver.ProvisioningSucceeded,
		State:                           "Ready",
		AdministratorLogin:              "citus",
		CitusVersion:                    orDefault(cfg.CitusVersion, defaultCitusVersion),
		PostgresqlVersion:               orDefault(cfg.PostgresqlVersion, defaultPostgresqlVersion),
		CoordinatorServerEdition:        orDefault(cfg.CoordinatorServerEdition, defaultServerEdition),
		CoordinatorVCores:               orDefaultInt(cfg.CoordinatorVCores, defaultCoordinatorVCores),
		CoordinatorStorageQuotaInMb:     orDefaultInt(cfg.CoordinatorStorageQuotaInMb, defaultStorageQuotaInMb),
		CoordinatorEnablePublicIPAccess: cfg.CoordinatorEnablePublicIPAccess,
		EnableShardsOnCoordinator:       cfg.EnableShardsOnCoordinator,
		NodeServerEdition:               orDefault(cfg.NodeServerEdition, defaultServerEdition),
		NodeCount:                       cfg.NodeCount,
		NodeVCores:                      orDefaultInt(cfg.NodeVCores, defaultNodeVCores),
		NodeStorageQuotaInMb:            orDefaultInt(cfg.NodeStorageQuotaInMb, defaultStorageQuotaInMb),
		NodeEnablePublicIPAccess:        cfg.NodeEnablePublicIPAccess,
		EnableHa:                        cfg.EnableHa,
		PreferredPrimaryZone:            cfg.PreferredPrimaryZone,
		MaintenanceWindow:               cloneMaintenanceWindow(cfg.MaintenanceWindow),
		SourceResourceID:                cfg.SourceResourceID,
		SourceLocation:                  cfg.SourceLocation,
	}

	// Preserve service-computed fields across a re-PUT.
	if existing, ok := m.clusters.Get(key); ok {
		c.State = existing.State
		c.ReadReplicas = cloneStrings(existing.ReadReplicas)

		if c.SourceResourceID == "" {
			c.SourceResourceID = existing.SourceResourceID
			c.SourceLocation = existing.SourceLocation
		}
	} else if cfg.SourceResourceID != "" {
		// A newly-created replica registers itself on its source cluster.
		m.linkReplicaLocked(cfg.SourceResourceID, m.clusterResourceID(cfg.ResourceGroup, cfg.Name))
	}

	m.clusters.Set(key, c)

	out := cloneCluster(&c)

	return &out, nil
}

// linkReplicaLocked adds replicaID to the ReadReplicas of the source cluster
// identified by sourceID (a full resource ID). The caller holds the lock.
func (m *Mock) linkReplicaLocked(sourceID, replicaID string) {
	rg, name, ok := parseClusterID(sourceID)
	if !ok {
		return
	}

	src, ok := m.clusters.Get(clusterKey(rg, name))
	if !ok {
		return
	}

	src.ReadReplicas = append(cloneStrings(src.ReadReplicas), replicaID)
	m.clusters.Set(clusterKey(rg, name), src)
}

// parseClusterID extracts (resourceGroup, name) from a serverGroupsv2 resource
// ID. Returns ok=false if the ID isn't shaped as expected.
func parseClusterID(id string) (rg, name string, ok bool) {
	parts := strings.Split(strings.Trim(id, "/"), "/")

	for i := 0; i+1 < len(parts); i++ {
		switch {
		case strings.EqualFold(parts[i], "resourceGroups"):
			rg = parts[i+1]
		case strings.EqualFold(parts[i], clusterType):
			name = parts[i+1]
		}
	}

	if rg == "" || name == "" {
		return "", "", false
	}

	return rg, name, true
}

// GetCluster returns a cluster by resource group + name.
func (m *Mock) GetCluster(_ context.Context, rg, name string) (*cpgdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", name)
	}

	out := cloneCluster(&c)

	return &out, nil
}

// ListClustersByResourceGroup returns all clusters in a resource group.
func (m *Mock) ListClustersByResourceGroup(_ context.Context, rg string) ([]cpgdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.filterClusters(func(c *cpgdriver.Cluster) bool { return c.ResourceGroup == rg }), nil
}

// ListClustersBySubscription returns all clusters (the mock serves one
// subscription).
func (m *Mock) ListClustersBySubscription(_ context.Context) ([]cpgdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.filterClusters(func(*cpgdriver.Cluster) bool { return true }), nil
}

func (m *Mock) filterClusters(keep func(*cpgdriver.Cluster) bool) []cpgdriver.Cluster {
	all := m.clusters.SortedValues()
	out := make([]cpgdriver.Cluster, 0, len(all))

	for i := range all {
		if keep(&all[i]) {
			out = append(out, cloneCluster(&all[i]))
		}
	}

	return out
}

// UpdateCluster applies a PATCH to a cluster.
//
//nolint:gocritic // patch matches the driver signature.
func (m *Mock) UpdateCluster(_ context.Context, rg, name string, patch cpgdriver.ClusterPatch) (*cpgdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", name)
	}

	applyClusterPatch(&c, &patch)
	m.clusters.Set(key, c)

	out := cloneCluster(&c)

	return &out, nil
}

func applyClusterPatch(c *cpgdriver.Cluster, patch *cpgdriver.ClusterPatch) {
	if patch.Tags != nil {
		c.Tags = copyTags(patch.Tags)
	}

	setStr(&c.CitusVersion, patch.CitusVersion)
	setStr(&c.PostgresqlVersion, patch.PostgresqlVersion)
	setStr(&c.CoordinatorServerEdition, patch.CoordinatorServerEdition)
	setInt(&c.CoordinatorVCores, patch.CoordinatorVCores)
	setInt(&c.CoordinatorStorageQuotaInMb, patch.CoordinatorStorageQuotaInMb)
	setStr(&c.NodeServerEdition, patch.NodeServerEdition)
	setInt(&c.NodeCount, patch.NodeCount)
	setInt(&c.NodeVCores, patch.NodeVCores)
	setInt(&c.NodeStorageQuotaInMb, patch.NodeStorageQuotaInMb)
	setStr(&c.PreferredPrimaryZone, patch.PreferredPrimaryZone)

	if patch.EnableHa != nil {
		c.EnableHa = *patch.EnableHa
	}

	if patch.MaintenanceWindow != nil {
		c.MaintenanceWindow = cloneMaintenanceWindow(patch.MaintenanceWindow)
	}
}

func setStr(dst, v *string) {
	if v != nil {
		*dst = *v
	}
}

func setInt(dst, v *int) {
	if v != nil {
		*dst = *v
	}
}

// DeleteCluster removes a cluster and cascade-deletes its children.
func (m *Mock) DeleteCluster(_ context.Context, rg, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)
	if !m.clusters.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", name)
	}

	prefix := rg + "/" + name + "/"

	deletePrefixed(m.firewallRules, prefix)
	deletePrefixed(m.roles, prefix)
	deletePrefixed(m.privateEPs, prefix)
	deletePrefixed(m.serverConfigs, prefix)
	m.clusters.Delete(key)

	return nil
}

func deletePrefixed[T any](store *memstore.Store[T], prefix string) {
	for _, k := range store.Keys() {
		if strings.HasPrefix(k, prefix) {
			store.Delete(k)
		}
	}
}

// listChildren returns the store's values whose key is under rg/cluster/,
// cloned via clone.
func listChildren[T any](store *memstore.Store[T], rg, cluster string, keyOf func(*T) string, clone func(*T) T) []T {
	prefix := rg + "/" + cluster + "/"
	all := store.SortedValues()
	out := make([]T, 0, len(all))

	for i := range all {
		if strings.HasPrefix(keyOf(&all[i]), prefix) {
			out = append(out, clone(&all[i]))
		}
	}

	return out
}

// RestartCluster validates the cluster exists (no state change in the mock).
func (m *Mock) RestartCluster(_ context.Context, rg, name string) error {
	return m.requireCluster(rg, name)
}

// StartCluster marks the cluster Ready.
func (m *Mock) StartCluster(_ context.Context, rg, name string) error {
	return m.setClusterState(rg, name, "Ready")
}

// StopCluster marks the cluster Stopped.
func (m *Mock) StopCluster(_ context.Context, rg, name string) error {
	return m.setClusterState(rg, name, "Stopped")
}

func (m *Mock) requireCluster(rg, name string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, name)) {
		return cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", name)
	}

	return nil
}

func (m *Mock) setClusterState(rg, name, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", name)
	}

	c.State = state
	m.clusters.Set(key, c)

	return nil
}

// PromoteReadReplica detaches a replica from its source, making it an
// independent cluster.
func (m *Mock) PromoteReadReplica(_ context.Context, rg, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", name)
	}

	if c.SourceResourceID == "" {
		return cerrors.Newf(cerrors.FailedPrecondition, "cluster %q is not a read replica", name)
	}

	m.unlinkReplicaLocked(c.SourceResourceID, m.clusterResourceID(rg, name))

	c.SourceResourceID = ""
	c.SourceLocation = ""
	m.clusters.Set(key, c)

	return nil
}

func (m *Mock) unlinkReplicaLocked(sourceID, replicaID string) {
	rg, name, ok := parseClusterID(sourceID)
	if !ok {
		return
	}

	src, ok := m.clusters.Get(clusterKey(rg, name))
	if !ok {
		return
	}

	kept := src.ReadReplicas[:0:0]

	for _, r := range src.ReadReplicas {
		if r != replicaID {
			kept = append(kept, r)
		}
	}

	src.ReadReplicas = kept
	m.clusters.Set(clusterKey(rg, name), src)
}

// CheckNameAvailability reports whether a cluster name is free in the
// subscription.
func (m *Mock) CheckNameAvailability(_ context.Context, name, typ string) (*cpgdriver.NameAvailability, error) {
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "name is required")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	out := &cpgdriver.NameAvailability{
		Name:          name,
		Type:          orDefault(typ, providerNamespace+"/"+clusterType),
		NameAvailable: true,
	}

	all := m.clusters.SortedValues()
	for i := range all {
		if all[i].Name == name {
			out.NameAvailable = false
			out.Message = "Name already in use."

			break
		}
	}

	return out, nil
}
