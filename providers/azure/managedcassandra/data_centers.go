package managedcassandra

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
)

func cloneDataCenter(in *mcdriver.DataCenter) mcdriver.DataCenter {
	d := *in
	d.SeedNodes = cloneStrings(d.SeedNodes)

	return d
}

func orInt(v, def int) int {
	if v <= 0 {
		return def
	}

	return v
}

// CreateOrUpdateDataCenter creates or replaces a datacenter under a cluster.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateOrUpdateDataCenter(_ context.Context, cfg mcdriver.CreateDataCenterConfig) (*mcdriver.DataCenter, error) {
	if err := validName("datacenter", cfg.Name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(clusterKey(cfg.ResourceGroup, cfg.ClusterName))
	if !ok {
		// A datacenter is a child of a cluster: real Azure returns 404
		// ParentResourceNotFound when the parent cluster does not exist.
		return nil, cerrors.Newf(cerrors.NotFound, "managed cassandra cluster %q not found", cfg.ClusterName)
	}

	if err := validateNodeCount(cfg.NodeCount); err != nil {
		return nil, err
	}

	key := dcKey(cfg.ResourceGroup, cfg.ClusterName, cfg.Name)
	nodeCount := orInt(cfg.NodeCount, defaultNodeCount)

	dc := mcdriver.DataCenter{
		Name:                               cfg.Name,
		ClusterName:                        cfg.ClusterName,
		ResourceGroup:                      cfg.ResourceGroup,
		ProvisioningState:                  mcdriver.ProvisioningSucceeded,
		DataCenterLocation:                 cfg.DataCenterLocation,
		DelegatedSubnetID:                  cfg.DelegatedSubnetID,
		NodeCount:                          nodeCount,
		DiskCapacity:                       orInt(cfg.DiskCapacity, defaultDiskCapacity),
		SKU:                                orDefault(cfg.SKU, defaultDataCenterSKU),
		DiskSKU:                            orDefault(cfg.DiskSKU, defaultDiskSKU),
		AvailabilityZone:                   cfg.AvailabilityZone,
		Base64EncodedCassandraYamlFragment: cfg.Base64EncodedCassandraYamlFragment,
		BackupStorageCustomerKeyURI:        cfg.BackupStorageCustomerKeyURI,
		ManagedDiskCustomerKeyURI:          cfg.ManagedDiskCustomerKeyURI,
		SeedNodes:                          seedNodes(cfg.Name, nodeCount),
		// A datacenter inherits the parent cluster's run state so a DC added to
		// (or replaced in) a deallocated cluster isn't reported NORMAL while its
		// siblings are STOPPED.
		Deallocated: cluster.Deallocated,
	}
	m.dataCenters.Set(key, dc)

	// Adding/refreshing a datacenter updates the parent cluster's seed nodes.
	m.refreshClusterSeeds(cfg.ResourceGroup, cfg.ClusterName)

	out := cloneDataCenter(&dc)

	return &out, nil
}

// maxNodesPerDataCenter bounds the node count so a caller-supplied value can't
// drive an unbounded allocation.
const maxNodesPerDataCenter = 100

// validateNodeCount rejects a node count outside the service limits.
func validateNodeCount(nodeCount int) error {
	if nodeCount < 0 || nodeCount > maxNodesPerDataCenter {
		return cerrors.Newf(cerrors.InvalidArgument, "nodeCount must be between 0 and %d", maxNodesPerDataCenter)
	}

	return nil
}

// seedNodes fabricates one seed address per node for a datacenter. The capacity
// is intentionally unhinted: nodeCount originates from caller input (a tainted
// make() size is an uncontrolled-allocation risk); the bound below keeps the
// append-driven growth in check.
func seedNodes(dc string, nodeCount int) []string {
	if nodeCount > maxNodesPerDataCenter {
		nodeCount = maxNodesPerDataCenter
	}

	var out []string
	for i := 0; i < nodeCount; i++ {
		out = append(out, fmt.Sprintf("%s-seed-%d.cassandra.cosmos.azure.com", dc, i))
	}

	return out
}

// refreshClusterSeeds recomputes a cluster's derived seed nodes. Caller holds
// the write lock.
func (m *Mock) refreshClusterSeeds(rg, cluster string) {
	c, ok := m.clusters.Get(clusterKey(rg, cluster))
	if !ok {
		return
	}

	c.SeedNodes = m.clusterSeedNodes(rg, cluster)
	m.clusters.Set(clusterKey(rg, cluster), c)
}

// GetDataCenter returns a datacenter by resource group + cluster + name.
func (m *Mock) GetDataCenter(_ context.Context, rg, cluster, name string) (*mcdriver.DataCenter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dc, ok := m.dataCenters.Get(dcKey(rg, cluster, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "datacenter %q not found in cluster %q", name, cluster)
	}

	out := cloneDataCenter(&dc)

	return &out, nil
}

// ListDataCenters returns the datacenters of a cluster.
func (m *Mock) ListDataCenters(_ context.Context, rg, cluster string) ([]mcdriver.DataCenter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cassandra cluster %q not found", cluster)
	}

	prefix := rg + "/" + cluster + "/"
	all := m.dataCenters.SortedValues()
	out := make([]mcdriver.DataCenter, 0, len(all))

	for i := range all {
		if strings.HasPrefix(dcKey(all[i].ResourceGroup, all[i].ClusterName, all[i].Name), prefix) {
			out = append(out, cloneDataCenter(&all[i]))
		}
	}

	return out, nil
}

// UpdateDataCenter applies a PATCH to a datacenter.
func (m *Mock) UpdateDataCenter(
	_ context.Context, rg, cluster, name string, patch mcdriver.DataCenterPatch,
) (*mcdriver.DataCenter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := dcKey(rg, cluster, name)

	dc, ok := m.dataCenters.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "datacenter %q not found in cluster %q", name, cluster)
	}

	// A 0 node count means "unchanged" — consistent with create, where 0 means
	// "use the default" rather than a real zero-node datacenter.
	if patch.NodeCount != nil && *patch.NodeCount > 0 {
		if err := validateNodeCount(*patch.NodeCount); err != nil {
			return nil, err
		}

		dc.NodeCount = *patch.NodeCount
		dc.SeedNodes = seedNodes(name, dc.NodeCount)
	}

	if patch.DiskCapacity != nil {
		dc.DiskCapacity = *patch.DiskCapacity
	}

	if patch.Base64EncodedCassandraYamlFragment != nil {
		dc.Base64EncodedCassandraYamlFragment = *patch.Base64EncodedCassandraYamlFragment
	}

	m.dataCenters.Set(key, dc)
	m.refreshClusterSeeds(rg, cluster)

	out := cloneDataCenter(&dc)

	return &out, nil
}

// DeleteDataCenter removes a datacenter.
func (m *Mock) DeleteDataCenter(_ context.Context, rg, cluster, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := dcKey(rg, cluster, name)
	if !m.dataCenters.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "datacenter %q not found in cluster %q", name, cluster)
	}

	m.dataCenters.Delete(key)
	m.refreshClusterSeeds(rg, cluster)

	return nil
}
