// Package managedcassandra provides an in-memory mock of Azure Managed Instance
// for Apache Cassandra (Microsoft.DocumentDB/cassandraClusters). It models
// clusters and their datacenters (a parent/child relationship), the
// deallocate/start lifecycle, and the status / invoke-command actions.
package managedcassandra

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
)

const (
	defaultCassandraVersion = "3.11"
	defaultDataCenterSKU    = "Standard_DS14_v2"
	defaultDiskSKU          = "P30"
	defaultNodeCount        = 3
	defaultDiskCapacity     = 4
)

var _ mcdriver.ManagedCassandra = (*Mock)(nil)

// Mock is the in-memory Azure Managed Cassandra implementation.
type Mock struct {
	mu sync.RWMutex

	clusters    *memstore.Store[mcdriver.Cluster]    // key = "rg/name"
	dataCenters *memstore.Store[mcdriver.DataCenter] // key = "rg/cluster/name"

	opts *config.Options
}

// New creates a new Managed Cassandra mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:    memstore.New[mcdriver.Cluster](),
		dataCenters: memstore.New[mcdriver.DataCenter](),
		opts:        opts,
	}
}

func clusterKey(rg, name string) string { return rg + "/" + name }

func dcKey(rg, cluster, name string) string { return rg + "/" + cluster + "/" + name }

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

func cloneCluster(in *mcdriver.Cluster) mcdriver.Cluster {
	c := *in
	c.Tags = copyTags(c.Tags)
	c.ExternalSeedNodes = cloneStrings(c.ExternalSeedNodes)
	c.SeedNodes = cloneStrings(c.SeedNodes)
	c.ClientCertificates = cloneStrings(c.ClientCertificates)
	c.ExternalGossipCertificates = cloneStrings(c.ExternalGossipCertificates)
	c.GossipCertificates = cloneStrings(c.GossipCertificates)

	return c
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// CreateOrUpdateCluster creates or replaces a managed Cassandra cluster.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateOrUpdateCluster(_ context.Context, cfg mcdriver.CreateClusterConfig) (*mcdriver.Cluster, error) {
	if err := validName("cluster", cfg.Name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(cfg.ResourceGroup, cfg.Name)

	c := mcdriver.Cluster{
		Name:                         cfg.Name,
		ResourceGroup:                cfg.ResourceGroup,
		Location:                     cfg.Location,
		Tags:                         copyTags(cfg.Tags),
		ProvisioningState:            mcdriver.ProvisioningSucceeded,
		CassandraVersion:             orDefault(cfg.CassandraVersion, defaultCassandraVersion),
		ClusterNameOverride:          cfg.ClusterNameOverride,
		DelegatedManagementSubnetID:  cfg.DelegatedManagementSubnetID,
		AuthenticationMethod:         orDefault(cfg.AuthenticationMethod, "Cassandra"),
		HoursBetweenBackups:          cfg.HoursBetweenBackups,
		RepairEnabled:                cfg.RepairEnabled,
		CassandraAuditLoggingEnabled: cfg.CassandraAuditLoggingEnabled,
		ExternalSeedNodes:            cloneStrings(cfg.ExternalSeedNodes),
		ClientCertificates:           cloneStrings(cfg.ClientCertificates),
		ExternalGossipCertificates:   cloneStrings(cfg.ExternalGossipCertificates),
	}

	// Preserve service-computed fields (run state and gossip/prometheus
	// endpoints the caller can't set) across a re-PUT.
	if existing, ok := m.clusters.Get(key); ok {
		c.Deallocated = existing.Deallocated
		c.GossipCertificates = existing.GossipCertificates
		c.PrometheusEndpoint = existing.PrometheusEndpoint
	}

	c.SeedNodes = m.clusterSeedNodes(cfg.ResourceGroup, cfg.Name)
	m.clusters.Set(key, c)

	out := cloneCluster(&c)

	return &out, nil
}

// clusterSeedNodes returns the seed node addresses derived from the cluster's
// datacenters. The caller holds the lock.
func (m *Mock) clusterSeedNodes(rg, cluster string) []string {
	prefix := rg + "/" + cluster + "/"

	var seeds []string

	dcs := m.dataCenters.SortedValues()
	for i := range dcs {
		if strings.HasPrefix(dcKey(dcs[i].ResourceGroup, dcs[i].ClusterName, dcs[i].Name), prefix) {
			seeds = append(seeds, dcs[i].SeedNodes...)
		}
	}

	return seeds
}

// GetCluster returns a cluster by resource group + name.
func (m *Mock) GetCluster(_ context.Context, rg, name string) (*mcdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cassandra cluster %q not found", name)
	}

	out := cloneCluster(&c)

	return &out, nil
}

// ListClustersByResourceGroup returns all clusters in a resource group.
func (m *Mock) ListClustersByResourceGroup(_ context.Context, rg string) ([]mcdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.filterClusters(func(c *mcdriver.Cluster) bool { return strings.EqualFold(c.ResourceGroup, rg) }), nil
}

// ListClustersBySubscription returns all clusters (the mock serves one
// subscription).
func (m *Mock) ListClustersBySubscription(_ context.Context) ([]mcdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.filterClusters(func(*mcdriver.Cluster) bool { return true }), nil
}

func (m *Mock) filterClusters(keep func(*mcdriver.Cluster) bool) []mcdriver.Cluster {
	all := m.clusters.SortedValues()
	out := make([]mcdriver.Cluster, 0, len(all))

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
func (m *Mock) UpdateCluster(_ context.Context, rg, name string, patch mcdriver.ClusterPatch) (*mcdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cassandra cluster %q not found", name)
	}

	if patch.Tags != nil {
		c.Tags = copyTags(patch.Tags)
	}

	if patch.RepairEnabled != nil {
		c.RepairEnabled = *patch.RepairEnabled
	}

	if patch.HoursBetweenBackups != nil {
		c.HoursBetweenBackups = *patch.HoursBetweenBackups
	}

	if patch.AuthenticationMethod != nil {
		c.AuthenticationMethod = *patch.AuthenticationMethod
	}

	if patch.ExternalSeedNodes != nil {
		c.ExternalSeedNodes = cloneStrings(patch.ExternalSeedNodes)
	}

	if patch.ClientCertificates != nil {
		c.ClientCertificates = cloneStrings(patch.ClientCertificates)
	}

	m.clusters.Set(key, c)

	out := cloneCluster(&c)

	return &out, nil
}

// DeleteCluster removes a cluster and cascade-deletes its datacenters.
func (m *Mock) DeleteCluster(_ context.Context, rg, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)
	if !m.clusters.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "managed cassandra cluster %q not found", name)
	}

	prefix := rg + "/" + name + "/"
	for _, dcK := range m.dataCenters.Keys() {
		if strings.HasPrefix(dcK, prefix) {
			m.dataCenters.Delete(dcK)
		}
	}

	m.clusters.Delete(key)

	return nil
}

// DeallocateCluster stops the cluster and its datacenters.
func (m *Mock) DeallocateCluster(_ context.Context, rg, name string) (*mcdriver.Cluster, error) {
	return m.setClusterDeallocated(rg, name, true)
}

// StartCluster restarts a deallocated cluster and its datacenters.
func (m *Mock) StartCluster(_ context.Context, rg, name string) (*mcdriver.Cluster, error) {
	return m.setClusterDeallocated(rg, name, false)
}

func (m *Mock) setClusterDeallocated(rg, name string, deallocated bool) (*mcdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cassandra cluster %q not found", name)
	}

	c.Deallocated = deallocated
	m.clusters.Set(key, c)

	prefix := rg + "/" + name + "/"
	for _, dcK := range m.dataCenters.Keys() {
		if !strings.HasPrefix(dcK, prefix) {
			continue
		}

		if dc, dok := m.dataCenters.Get(dcK); dok {
			dc.Deallocated = deallocated
			m.dataCenters.Set(dcK, dc)
		}
	}

	out := cloneCluster(&c)

	return &out, nil
}

// InvokeCommand runs a command on the cluster and returns its stdout. The mock
// echoes the request rather than executing anything.
func (m *Mock) InvokeCommand(_ context.Context, rg, name, command, host string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, name)) {
		return "", cerrors.Newf(cerrors.NotFound, "managed cassandra cluster %q not found", name)
	}

	return fmt.Sprintf("executed %q on %s", command, orDefault(host, "seed node")), nil
}

// ClusterStatus reports node health derived from the cluster's datacenters.
func (m *Mock) ClusterStatus(_ context.Context, rg, name string) (*mcdriver.ClusterStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cassandra cluster %q not found", name)
	}

	status := &mcdriver.ClusterStatus{ClusterName: name, ReaperStatus: c.RepairEnabled}

	prefix := rg + "/" + name + "/"

	dcs := m.dataCenters.SortedValues()
	for i := range dcs {
		dc := &dcs[i]
		if !strings.HasPrefix(dcKey(dc.ResourceGroup, dc.ClusterName, dc.Name), prefix) {
			continue
		}

		appendNodeStatuses(status, dc)
	}

	return status, nil
}

const racksPerDataCenter = 3

// appendNodeStatuses adds one NodeStatus per node in dc to status.
func appendNodeStatuses(status *mcdriver.ClusterStatus, dc *mcdriver.DataCenter) {
	state := "NORMAL"
	if dc.Deallocated {
		state = "STOPPED"
	}

	const firstNodeOctet = 4

	for n := 0; n < dc.NodeCount; n++ {
		status.Nodes = append(status.Nodes, mcdriver.NodeStatus{
			DataCenter: dc.Name,
			Address:    fmt.Sprintf("10.%d.%d.%d", len(status.Nodes)/oneByte, len(status.Nodes)%oneByte, n+firstNodeOctet),
			State:      state,
			Rack:       fmt.Sprintf("rack-%d", n%racksPerDataCenter+1),
			Load:       "1.5 GiB",
		})
	}
}

const oneByte = 256
