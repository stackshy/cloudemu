package memorydb

import (
	"context"
	"fmt"

	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

// defaultServiceUpdate is the single security update the mock advertises as
// available for every cluster.
const defaultServiceUpdate = "memorydb-20240101-001"

// serviceUpdateFor projects the available service update for one cluster.
func (m *Mock) serviceUpdateFor(c *mdbdriver.Cluster) mdbdriver.ServiceUpdate {
	now := m.opts.Clock.Now().UTC()

	return mdbdriver.ServiceUpdate{
		ClusterName:         c.Name,
		ServiceUpdateName:   defaultServiceUpdate,
		ReleaseDate:         now,
		AutoUpdateStartDate: now,
		Description:         "Security and stability update",
		Status:              "available",
		Type:                "security-update",
		Engine:              c.Engine,
		NodesUpdated:        fmt.Sprintf("0/%d", c.NumberOfShards),
	}
}

// DescribeServiceUpdates lists the service updates available across clusters,
// optionally filtered by update name, cluster names, and status.
func (m *Mock) DescribeServiceUpdates(
	_ context.Context, serviceUpdateName string, clusterNames, status []string,
) ([]mdbdriver.ServiceUpdate, error) {
	if serviceUpdateName != "" && serviceUpdateName != defaultServiceUpdate {
		return nil, nil
	}

	if len(status) > 0 && !containsStr(status, "available") {
		return nil, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	clusters := m.clusters.SortedValues()
	out := make([]mdbdriver.ServiceUpdate, 0, len(clusters))

	for i := range clusters {
		if len(clusterNames) > 0 && !containsStr(clusterNames, clusters[i].Name) {
			continue
		}

		out = append(out, m.serviceUpdateFor(&clusters[i]))
	}

	return out, nil
}

// BatchUpdateCluster applies a service update to the named clusters, returning
// the processed clusters and, for any name that does not exist, an entry in the
// unprocessed list (rather than failing the whole batch).
func (m *Mock) BatchUpdateCluster(
	_ context.Context, clusterNames []string, _ string,
) ([]mdbdriver.Cluster, []mdbdriver.UnprocessedCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	processed := make([]mdbdriver.Cluster, 0, len(clusterNames))
	unprocessed := make([]mdbdriver.UnprocessedCluster, 0)

	for _, name := range clusterNames {
		c, ok := m.clusters.Get(name)
		if !ok {
			unprocessed = append(unprocessed, mdbdriver.UnprocessedCluster{
				ClusterName:  name,
				ErrorType:    "ClusterNotFoundFault",
				ErrorMessage: fmt.Sprintf("cluster %q not found", name),
			})

			continue
		}

		m.recordClusterEvent(name, "Service update "+defaultServiceUpdate+" applied")

		processed = append(processed, cloneCluster(&c))
	}

	return processed, unprocessed, nil
}

var _ mdbdriver.ServiceUpdates = (*Mock)(nil)
