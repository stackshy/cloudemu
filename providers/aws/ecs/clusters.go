package ecs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// CreateCluster creates a cluster, defaulting the name to "default".
func (m *Mock) CreateCluster(_ context.Context, in driver.CreateClusterInput) (*driver.Cluster, error) {
	name := in.Name
	if name == "" {
		name = defaultCluster
	}

	if m.clusters.Has(name) {
		return nil, errors.Newf(errors.AlreadyExists, "cluster %q already exists", name)
	}

	c := &driver.Cluster{
		ARN:      m.arn("cluster/" + name),
		Name:     name,
		Status:   statusActive,
		Tags:     copyTags(in.Tags),
		Settings: append([]driver.Setting(nil), in.Settings...),
	}
	m.clusters.Set(name, c)

	out := *c

	return &out, nil
}

// ListClusters returns all clusters in deterministic order.
func (m *Mock) ListClusters(_ context.Context) ([]driver.Cluster, error) {
	all := m.clusters.SortedValues()

	out := make([]driver.Cluster, 0, len(all))
	for _, c := range all {
		out = append(out, *c)
	}

	return out, nil
}

// DescribeClusters resolves each id to a cluster; unresolved ids become
// failures. An empty id list returns every cluster.
func (m *Mock) DescribeClusters(_ context.Context, ids []string) ([]driver.Cluster, []driver.Failure, error) {
	if len(ids) == 0 {
		all := m.clusters.SortedValues()

		clusters := make([]driver.Cluster, 0, len(all))
		for _, c := range all {
			clusters = append(clusters, *c)
		}

		return clusters, nil, nil
	}

	found := make([]driver.Cluster, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		name := resolveClusterName(id)
		if c, ok := m.clusters.Get(name); ok {
			found = append(found, *c)
			continue
		}

		failures = append(failures, driver.Failure{ARN: m.arn("cluster/" + name), Reason: "MISSING"})
	}

	return found, failures, nil
}

// DeleteCluster marks a cluster INACTIVE and returns it.
func (m *Mock) DeleteCluster(_ context.Context, id string) (*driver.Cluster, error) {
	name := resolveClusterName(id)

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cluster %q not found", name)
	}

	c.Status = statusInactive

	out := *c

	return &out, nil
}
