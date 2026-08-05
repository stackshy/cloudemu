package cosmospostgresql

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

// serverName returns the derived node name: "<cluster>-c" for the coordinator
// and "<cluster>-w<idx>" for worker nodes.
func serverName(cluster, role string, idx int) string {
	if role == cpgdriver.RoleCoordinator {
		return cluster + "-c"
	}

	return fmt.Sprintf("%s-w%d", cluster, idx)
}

// nodesForCluster derives the (read-only) node list from a cluster's shape: one
// coordinator plus NodeCount workers.
func (m *Mock) nodesForCluster(c *cpgdriver.Cluster) []cpgdriver.Server {
	// Clamp defensively: create/PATCH validate the bound, but a bad stored value
	// must never reach make() with a negative/huge cap.
	workers := c.NodeCount
	if workers < 0 {
		workers = 0
	}

	if workers > maxNodeCount {
		workers = maxNodeCount
	}

	out := make([]cpgdriver.Server, 0, workers+1)

	out = append(out, m.node(c, cpgdriver.RoleCoordinator, 0))
	for i := 0; i < workers; i++ {
		out = append(out, m.node(c, cpgdriver.RoleWorker, i))
	}

	return out
}

func (*Mock) node(c *cpgdriver.Cluster, role string, idx int) cpgdriver.Server {
	name := serverName(c.Name, role, idx)

	vcores, edition := c.NodeVCores, c.NodeServerEdition
	storage, public := c.NodeStorageQuotaInMb, c.NodeEnablePublicIPAccess

	if role == cpgdriver.RoleCoordinator {
		vcores, edition = c.CoordinatorVCores, c.CoordinatorServerEdition
		storage, public = c.CoordinatorStorageQuotaInMb, c.CoordinatorEnablePublicIPAccess
	}

	haState := ""
	if c.EnableHa {
		haState = "Healthy"
	}

	return cpgdriver.Server{
		Name:                     name,
		ClusterName:              c.Name,
		ResourceGroup:            c.ResourceGroup,
		Role:                     role,
		State:                    orDefault(c.State, "Ready"),
		HaState:                  haState,
		FullyQualifiedDomainName: name + "." + orDefault(c.Location, "eastus") + ".postgres.cosmos.azure.com",
		AdministratorLogin:       c.AdministratorLogin,
		ServerEdition:            edition,
		VCores:                   vcores,
		StorageQuotaInMb:         storage,
		CitusVersion:             c.CitusVersion,
		PostgresqlVersion:        c.PostgresqlVersion,
		EnableHa:                 c.EnableHa,
		EnablePublicIPAccess:     public,
		// Every node of a read replica is read-only (coordinator included).
		IsReadOnly: c.SourceResourceID != "",
	}
}

// GetServer returns a derived node by name.
func (m *Mock) GetServer(_ context.Context, rg, cluster, name string) (*cpgdriver.Server, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(rg, cluster))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	nodes := m.nodesForCluster(&c)
	for i := range nodes {
		if nodes[i].Name == name {
			out := nodes[i]

			return &out, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "server %q not found", name)
}

// ListServers returns the derived nodes of a cluster.
func (m *Mock) ListServers(_ context.Context, rg, cluster string) ([]cpgdriver.Server, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(rg, cluster))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cluster)
	}

	return m.nodesForCluster(&c), nil
}
