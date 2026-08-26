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
	dbengine "github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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

	// citusRole is Cosmos DB for PostgreSQL's fixed coordinator superuser
	// ("citus"), also used as the default database name a client connects to.
	// enginePostgres is the family handed to the shared Postgres DatabaseEngine —
	// Cosmos DB for PostgreSQL is Citus Postgres, so it reuses that backing.
	citusRole      = "citus"
	enginePostgres = "postgres"
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

	// coordinatorHosts maps clusterKey -> the reachable coordinator host when a
	// real DatabaseEngine backs the cluster. node() surfaces it as the
	// coordinator's FQDN so a real client connects using only the SDK response.
	// Guarded by mu.
	coordinatorHosts map[string]string

	opts *config.Options
}

// New creates a new Cosmos DB for PostgreSQL mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:         memstore.New[cpgdriver.Cluster](),
		firewallRules:    memstore.New[cpgdriver.FirewallRule](),
		roles:            memstore.New[cpgdriver.Role](),
		privateEPs:       memstore.New[cpgdriver.PrivateEndpointConnection](),
		serverConfigs:    memstore.New[cpgdriver.ServerConfiguration](),
		coordinatorHosts: map[string]string{},
		opts:             opts,
	}
}

func clusterKey(rg, name string) string { return rg + "/" + name }

func childKey(rg, cluster, name string) string { return rg + "/" + cluster + "/" + name }

// requireClusterLocked returns NotFound if the parent cluster doesn't exist.
// Real Azure returns 404 for a missing parent on every child operation. The
// caller holds a lock.
func (m *Mock) requireClusterLocked(rg, cluster string) error {
	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", cluster)
	}

	return nil
}

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

// CreateOrUpdateCluster creates or replaces a server-group cluster. When a real
// DatabaseEngine is wired in, a newly-created cluster is also backed by a real
// Postgres database (the engine work runs without the store lock held) so the
// coordinator endpoint a client reads is reachable.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateOrUpdateCluster(ctx context.Context, cfg cpgdriver.CreateClusterConfig) (*cpgdriver.Cluster, bool, error) {
	if err := validName("cluster", cfg.Name); err != nil {
		return nil, false, err
	}

	if err := validateSizing(&cfg); err != nil {
		return nil, false, err
	}

	c, created, err := m.storeCluster(&cfg)
	if err != nil {
		return nil, false, err
	}

	if err := m.backClusterWithEngine(ctx, &cfg, created); err != nil {
		return nil, false, err
	}

	out := cloneCluster(&c)

	return &out, created, nil
}

// storeCluster validates and writes the cluster row under the lock, reporting
// whether it was created (true) or updated (false).
func (m *Mock) storeCluster(cfg *cpgdriver.CreateClusterConfig) (cpgdriver.Cluster, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(cfg.ResourceGroup, cfg.Name)
	existing, isUpdate := m.clusters.Get(key)

	// A create (no existing cluster at this rg/name) must have a globally-unique
	// name and, for a replica, a valid primary source.
	if !isUpdate {
		if err := m.ensureNameAvailableLocked(cfg.Name); err != nil {
			return cpgdriver.Cluster{}, false, err
		}

		if cfg.SourceResourceID != "" {
			if err := m.validateReplicaSourceLocked(cfg.SourceResourceID); err != nil {
				return cpgdriver.Cluster{}, false, err
			}
		}
	}

	c := cpgdriver.Cluster{
		Name:                            cfg.Name,
		ResourceGroup:                   cfg.ResourceGroup,
		Location:                        cfg.Location,
		Tags:                            copyTags(cfg.Tags),
		ProvisioningState:               cpgdriver.ProvisioningSucceeded,
		State:                           "Ready",
		AdministratorLogin:              citusRole,
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

	if isUpdate {
		// Preserve service-computed fields, and treat the replica source as
		// immutable — a re-PUT must not re-point (or corrupt) the replica graph.
		c.State = existing.State
		c.ReadReplicas = cloneStrings(existing.ReadReplicas)
		c.SourceResourceID = existing.SourceResourceID
		c.SourceLocation = existing.SourceLocation
	} else if cfg.SourceResourceID != "" {
		// A newly-created replica registers itself on its source cluster.
		m.linkReplicaLocked(cfg.SourceResourceID, m.clusterResourceID(cfg.ResourceGroup, cfg.Name))
	}

	m.clusters.Set(key, c)

	return c, !isUpdate, nil
}

// backClusterWithEngine provisions a real Postgres database for a newly-created
// cluster when a DatabaseEngine is wired in, then records the reachable
// coordinator host so node() surfaces it as the coordinator FQDN. The engine
// work runs without the store lock held. It is a no-op without an engine, on an
// update (the endpoint is already backed), or for a read replica — a replica is
// not engine-backed in the emulator (no duplicate real database is provisioned),
// so its coordinator FQDN stays synthetic. On failure the just-created cluster is
// rolled back.
func (m *Mock) backClusterWithEngine(ctx context.Context, cfg *cpgdriver.CreateClusterConfig, created bool) error {
	if m.opts.DatabaseEngine == nil || !created || cfg.SourceResourceID != "" {
		return nil
	}

	// Cosmos DB for PostgreSQL is Citus Postgres: the coordinator node serves the
	// Postgres wire protocol as the fixed "citus" superuser. Reuse the shared
	// Postgres engine through a throwaway relational instance.
	inst := rdsdriver.Instance{ID: cfg.Name, Engine: enginePostgres}
	provCfg := rdsdriver.InstanceConfig{
		ID:                 cfg.Name,
		Engine:             enginePostgres,
		DBName:             citusRole,
		MasterUsername:     citusRole,
		MasterUserPassword: cfg.AdministratorLoginPassword,
	}

	if err := dbengine.Provision(ctx, m.opts.DatabaseEngine, &inst, &provCfg); err != nil {
		_ = m.DeleteCluster(ctx, cfg.ResourceGroup, cfg.Name)

		return err
	}

	m.setCoordinatorHost(clusterKey(cfg.ResourceGroup, cfg.Name), inst.Endpoint)

	return nil
}

// setCoordinatorHost records the reachable coordinator host for a cluster.
func (m *Mock) setCoordinatorHost(key, host string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.coordinatorHosts[key] = host
}

// validateSizing bounds the node count and rejects negative vCore/storage
// sizing. Zero means "use the default", so only negatives are rejected there.
func validateSizing(cfg *cpgdriver.CreateClusterConfig) error {
	if cfg.NodeCount < 0 || cfg.NodeCount > maxNodeCount {
		return cerrors.Newf(cerrors.InvalidArgument, "nodeCount must be between 0 and %d", maxNodeCount)
	}

	if cfg.CoordinatorVCores < 0 || cfg.NodeVCores < 0 {
		return cerrors.New(cerrors.InvalidArgument, "vCores must not be negative")
	}

	if cfg.CoordinatorStorageQuotaInMb < 0 || cfg.NodeStorageQuotaInMb < 0 {
		return cerrors.New(cerrors.InvalidArgument, "storageQuotaInMb must not be negative")
	}

	return nil
}

// ensureNameAvailableLocked rejects a create whose name is already used by any
// cluster in the subscription (Cosmos-PG names are globally unique — they form
// the coordinator FQDN). The caller holds the lock.
func (m *Mock) ensureNameAvailableLocked(name string) error {
	all := m.clusters.SortedValues()
	for i := range all {
		if all[i].Name == name {
			return cerrors.Newf(cerrors.AlreadyExists, "cluster name %q is already in use", name)
		}
	}

	return nil
}

// validateReplicaSourceLocked requires the replica's source to exist and itself
// be a primary (no replica-of-a-replica chains). The caller holds the lock.
func (m *Mock) validateReplicaSourceLocked(sourceID string) error {
	rg, name, ok := parseClusterID(sourceID)
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "malformed sourceResourceId %q", sourceID)
	}

	src, ok := m.clusters.Get(clusterKey(rg, name))
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "source cluster %q not found", name)
	}

	if src.SourceResourceID != "" {
		return cerrors.Newf(cerrors.InvalidArgument, "cannot create a read replica of a read replica (%q)", name)
	}

	return nil
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

	return m.filterClusters(func(c *cpgdriver.Cluster) bool { return strings.EqualFold(c.ResourceGroup, rg) }), nil
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

	if err := applyClusterPatch(&c, &patch); err != nil {
		return nil, err
	}

	m.clusters.Set(key, c)

	out := cloneCluster(&c)

	return &out, nil
}

func applyClusterPatch(c *cpgdriver.Cluster, patch *cpgdriver.ClusterPatch) error {
	// A PATCH must re-validate the same bounds as create — otherwise a negative
	// or huge nodeCount is stored and later crashes node derivation.
	if err := validatePatchSizing(patch); err != nil {
		return err
	}

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

	if patch.CoordinatorEnablePublicIPAccess != nil {
		c.CoordinatorEnablePublicIPAccess = *patch.CoordinatorEnablePublicIPAccess
	}

	if patch.NodeEnablePublicIPAccess != nil {
		c.NodeEnablePublicIPAccess = *patch.NodeEnablePublicIPAccess
	}

	if patch.EnableShardsOnCoordinator != nil {
		c.EnableShardsOnCoordinator = *patch.EnableShardsOnCoordinator
	}

	// AdministratorLoginPassword is a write-only secret: accepted here but never
	// stored or surfaced (the real API never returns it).
	_ = patch.AdministratorLoginPassword

	if patch.MaintenanceWindow != nil {
		c.MaintenanceWindow = cloneMaintenanceWindow(patch.MaintenanceWindow)
	}

	return nil
}

// validatePatchSizing bounds any sizing fields present in a PATCH.
func validatePatchSizing(patch *cpgdriver.ClusterPatch) error {
	if patch.NodeCount != nil && (*patch.NodeCount < 0 || *patch.NodeCount > maxNodeCount) {
		return cerrors.Newf(cerrors.InvalidArgument, "nodeCount must be between 0 and %d", maxNodeCount)
	}

	for _, v := range []*int{patch.CoordinatorVCores, patch.NodeVCores} {
		if v != nil && *v < 0 {
			return cerrors.New(cerrors.InvalidArgument, "vCores must not be negative")
		}
	}

	for _, s := range []*int{patch.CoordinatorStorageQuotaInMb, patch.NodeStorageQuotaInMb} {
		if s != nil && *s < 0 {
			return cerrors.New(cerrors.InvalidArgument, "storageQuotaInMb must not be negative")
		}
	}

	return nil
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

// DeleteCluster removes a cluster and cascade-deletes its children, tearing down
// the real Postgres database backing it when a DatabaseEngine is wired in.
func (m *Mock) DeleteCluster(ctx context.Context, rg, name string) error {
	key := clusterKey(rg, name)

	m.mu.Lock()
	if _, ok := m.clusters.Get(key); !ok {
		m.mu.Unlock()

		return cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", name)
	}

	// No engine wired: keep the original single-lock flow.
	if m.opts.DatabaseEngine == nil {
		defer m.mu.Unlock()
		m.deleteClusterLocked(rg, name)

		return nil
	}
	m.mu.Unlock()

	// Engine wired: tear down the real coordinator database WITHOUT holding the
	// provider lock (it is a real container/process teardown), then remove the
	// row under a re-acquired lock — mirroring the create path and the RDS
	// reserve→provision→finalize pattern so a delete never stalls concurrent reads.
	inst := rdsdriver.Instance{ID: name, Engine: enginePostgres}
	if err := dbengine.Deprovision(ctx, m.opts.DatabaseEngine, &inst); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteClusterLocked(rg, name)

	return nil
}

// deleteClusterLocked removes the cluster row + its children and fixes replica
// links. The caller holds the write lock. A cluster already gone (a concurrent
// delete won the race after the engine teardown) is a no-op.
func (m *Mock) deleteClusterLocked(rg, name string) {
	key := clusterKey(rg, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return
	}

	delete(m.coordinatorHosts, key)

	// Keep replica links consistent: if this is a replica, drop it from its
	// source's list; if it's a source, orphan its replicas (clear their link).
	if c.SourceResourceID != "" {
		m.unlinkReplicaLocked(c.SourceResourceID, m.clusterResourceID(rg, name))
	}

	m.clearReplicaSourcesLocked(c.ReadReplicas)

	prefix := rg + "/" + name + "/"

	deletePrefixed(m.firewallRules, prefix)
	deletePrefixed(m.roles, prefix)
	deletePrefixed(m.privateEPs, prefix)
	deletePrefixed(m.serverConfigs, prefix)
	m.clusters.Delete(key)
}

// clearReplicaSourcesLocked clears SourceResourceID/SourceLocation on each
// replica whose resource ID is listed, orphaning them when their source is
// deleted. The caller holds the lock.
func (m *Mock) clearReplicaSourcesLocked(replicaIDs []string) {
	for _, id := range replicaIDs {
		rg, name, ok := parseClusterID(id)
		if !ok {
			continue
		}

		rep, ok := m.clusters.Get(clusterKey(rg, name))
		if !ok {
			continue
		}

		rep.SourceResourceID = ""
		rep.SourceLocation = ""
		m.clusters.Set(clusterKey(rg, name), rep)
	}
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

// RestartCluster restarts a running cluster (must be Ready).
func (m *Mock) RestartCluster(_ context.Context, rg, name string) error {
	return m.transitionState(rg, name, "Ready", "Ready")
}

// StartCluster starts a stopped cluster (must be Stopped → Ready).
func (m *Mock) StartCluster(_ context.Context, rg, name string) error {
	return m.transitionState(rg, name, "Stopped", "Ready")
}

// StopCluster stops a running cluster (must be Ready → Stopped).
func (m *Mock) StopCluster(_ context.Context, rg, name string) error {
	return m.transitionState(rg, name, "Ready", "Stopped")
}

// transitionState moves a cluster from want to next, rejecting the action when
// the cluster isn't in the expected state (real Azure 409s these).
func (m *Mock) transitionState(rg, name, want, next string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "cosmos postgresql cluster %q not found", name)
	}

	if c.State != want {
		return cerrors.Newf(cerrors.FailedPrecondition, "cluster %q is %q; expected %q for this action", name, c.State, want)
	}

	c.State = next
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
