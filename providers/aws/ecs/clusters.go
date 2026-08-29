package ecs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// CreateCluster creates a cluster, defaulting the name to "default".
func (m *Mock) CreateCluster(ctx context.Context, in driver.CreateClusterInput) (*driver.Cluster, error) {
	name := in.Name
	if name == "" {
		name = defaultCluster
	}

	c := &driver.Cluster{
		ARN:      m.arnIn(regionctx.RegionOr(ctx, m.opts.Region), "cluster/"+name),
		Name:     name,
		Status:   statusActive,
		Tags:     copyTags(in.Tags),
		Settings: append([]driver.Setting(nil), in.Settings...),
	}

	// Serialize the reject-if-ACTIVE / create-or-reuse compare-and-set so two
	// concurrent creates of the same name can't both succeed. Only an ACTIVE
	// cluster of the same name is a conflict; a previously deleted (INACTIVE)
	// tombstone is overwritten, so a deleted cluster name can be recreated —
	// real ECS lets the name be reused once the old cluster is gone.
	m.clusterMu.Lock()
	if existing, ok := m.clusters.Get(name); ok && existing.Status == statusActive {
		m.clusterMu.Unlock()
		return nil, errors.Newf(errors.AlreadyExists, "cluster %q already exists", name)
	}

	m.clusters.Set(name, c)
	m.clusterMu.Unlock()

	m.recordTags(c.ARN, in.Tags)

	out := cloneCluster(c)

	return &out, nil
}

// ListClusters returns all clusters in deterministic order.
func (m *Mock) ListClusters(_ context.Context) ([]driver.Cluster, error) {
	all := m.clusters.SortedValues()

	out := make([]driver.Cluster, 0, len(all))
	for _, c := range all {
		out = append(out, m.describeCluster(c))
	}

	return out, nil
}

// DescribeClusters resolves each id to a cluster; unresolved ids become
// failures. An empty id list returns every cluster.
func (m *Mock) DescribeClusters(ctx context.Context, ids []string) ([]driver.Cluster, []driver.Failure, error) {
	if len(ids) == 0 {
		all := m.clusters.SortedValues()

		clusters := make([]driver.Cluster, 0, len(all))
		for _, c := range all {
			clusters = append(clusters, m.describeCluster(c))
		}

		return clusters, nil, nil
	}

	found := make([]driver.Cluster, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		name := resolveClusterName(id)
		if c, ok := m.clusters.Get(name); ok {
			found = append(found, m.describeCluster(c))
			continue
		}

		failures = append(failures, driver.Failure{
			ARN:    m.arnIn(regionctx.RegionOr(ctx, m.opts.Region), "cluster/"+name),
			Reason: "MISSING",
		})
	}

	return found, failures, nil
}

// describeCluster returns a deep copy of the stored cluster with its live
// resource counts computed from the task, service, and instance stores.
func (m *Mock) describeCluster(c *driver.Cluster) driver.Cluster {
	out := cloneCluster(c)
	out.ActiveServicesCount, out.RunningTasksCount, out.PendingTasksCount, out.RegisteredContainerInstancesCount =
		m.clusterCounts(c.Name)

	return out
}

// DeleteCluster marks a cluster INACTIVE and returns it. AWS refuses to delete a
// cluster that still contains active services, running/pending tasks, or
// registered container instances, so those are guarded here.
func (m *Mock) DeleteCluster(_ context.Context, id string) (*driver.Cluster, error) {
	name := resolveClusterName(id)

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", name)
	}

	if err := m.checkClusterEmpty(name); err != nil {
		return nil, err
	}

	// Copy-on-write: mutate a clone and Set it back so concurrent readers never
	// race the status write.
	updated := cloneCluster(c)
	updated.Status = statusInactive
	m.clusters.Set(name, &updated)

	out := m.describeCluster(&updated)

	return &out, nil
}

// UpdateCluster updates a cluster's settings and execute-command configuration.
// A nil Settings or Configuration leaves that field unchanged.
func (m *Mock) UpdateCluster(_ context.Context, in driver.UpdateClusterInput) (*driver.Cluster, error) {
	return m.mutateCluster(in.Cluster, func(c *driver.Cluster) {
		if in.Settings != nil {
			c.Settings = append([]driver.Setting(nil), in.Settings...)
		}

		if in.Configuration != nil {
			// Clone the caller's raw JSON so a later mutation of their byte
			// slice can't corrupt the stored record (read paths already clone).
			c.Configuration = cloneRaw(in.Configuration)
		}
	})
}

// UpdateClusterSettings replaces a cluster's settings and returns the cluster.
func (m *Mock) UpdateClusterSettings(_ context.Context, cluster string, settings []driver.Setting) (*driver.Cluster, error) {
	return m.mutateCluster(cluster, func(c *driver.Cluster) {
		c.Settings = append([]driver.Setting(nil), settings...)
	})
}

// PutClusterCapacityProviders associates capacity providers and a default
// strategy with a cluster (stored and echoed, not resolved to real resources).
func (m *Mock) PutClusterCapacityProviders(
	_ context.Context, cluster string, capacityProviders []string, defaultStrategy []driver.CapacityProviderStrategyItem,
) (*driver.Cluster, error) {
	return m.mutateCluster(cluster, func(c *driver.Cluster) {
		c.CapacityProviders = append([]string(nil), capacityProviders...)
		c.DefaultCapacityProviderStrategy =
			append([]driver.CapacityProviderStrategyItem(nil), defaultStrategy...)
	})
}

// mutateCluster resolves a cluster by name or ARN, applies fn to a clone under
// copy-on-write, stores it, and returns the described cluster. The
// read-modify-write runs atomically under the store lock (via memstore.Update)
// so two concurrent cluster mutations can't lose one another's changes. An
// unknown cluster surfaces a ClusterNotFoundException.
func (m *Mock) mutateCluster(id string, fn func(*driver.Cluster)) (*driver.Cluster, error) {
	name := resolveClusterName(id)

	var updated driver.Cluster

	ok := m.clusters.Update(name, func(c *driver.Cluster) *driver.Cluster {
		updated = cloneCluster(c)
		fn(&updated)

		return &updated
	})
	if !ok {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", name)
	}

	out := m.describeCluster(&updated)

	return &out, nil
}

// checkClusterEmpty returns a typed FailedPrecondition error if the cluster
// still contains active services, running/pending tasks, or registered
// container instances, mirroring AWS's ClusterContains* exceptions.
func (m *Mock) checkClusterEmpty(name string) error {
	services, running, pending, instances := m.clusterCounts(name)

	switch {
	case services > 0:
		return apiErrf(errors.FailedPrecondition, excClusterContainsServices,
			"cluster %q contains %d active service(s)", name, services)
	case running+pending > 0:
		return apiErrf(errors.FailedPrecondition, excClusterContainsTasks,
			"cluster %q contains %d task(s)", name, running+pending)
	case instances > 0:
		return apiErrf(errors.FailedPrecondition, excClusterContainsInstances,
			"cluster %q contains %d container instance(s)", name, instances)
	}

	return nil
}
