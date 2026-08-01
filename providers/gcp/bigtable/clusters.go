package bigtable

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

func cloneCluster(in *btdriver.Cluster) btdriver.Cluster {
	c := *in

	if in.Autoscaling != nil {
		a := *in.Autoscaling
		c.Autoscaling = &a
	}

	return c
}

// putClusterLocked validates and stores a cluster. The caller holds the lock.
func (m *Mock) putClusterLocked(cfg btdriver.CreateClusterConfig) error {
	if err := validateServeNodes(cfg.ServeNodes); err != nil {
		return err
	}

	instance := parentName(cfg.Name)
	if !m.instances.Has(instance) {
		return cerrors.Newf(cerrors.InvalidArgument, "instance %q not found", instance)
	}

	serve := cfg.ServeNodes
	if serve == 0 && cfg.Autoscaling == nil {
		serve = defaultServeNodes
	}

	m.clusters.Set(cfg.Name, btdriver.Cluster{
		Name:               cfg.Name,
		Location:           cfg.Location,
		ServeNodes:         serve,
		DefaultStorageType: orDefault(cfg.DefaultStorageType, defaultStorageType),
		State:              btdriver.StateReady,
		Autoscaling:        cloneAutoscaling(cfg.Autoscaling),
	})

	return nil
}

// cloneAutoscaling copies the caller's autoscaling struct so a later mutation
// can't reach into the store (clone-on-write).
func cloneAutoscaling(a *btdriver.Autoscaling) *btdriver.Autoscaling {
	if a == nil {
		return nil
	}

	out := *a

	return &out
}

func validateServeNodes(n int) error {
	if n < 0 || n > maxServeNodes {
		return cerrors.Newf(cerrors.InvalidArgument, "serveNodes must be between 0 and %d", maxServeNodes)
	}

	return nil
}

// CreateCluster adds a cluster to an existing instance.
func (m *Mock) CreateCluster(_ context.Context, cfg btdriver.CreateClusterConfig) (*btdriver.Cluster, *btdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.clusters.Has(cfg.Name) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "cluster %q already exists", cfg.Name)
	}

	if err := m.putClusterLocked(cfg); err != nil {
		return nil, nil, err
	}

	c, _ := m.clusters.Get(cfg.Name)
	op := m.newOp("create-cluster", cfg.Name)
	out := cloneCluster(&c)

	return &out, op, nil
}

// GetCluster returns a cluster by full name.
func (m *Mock) GetCluster(_ context.Context, name string) (*btdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	out := cloneCluster(&c)

	return &out, nil
}

// ListClusters returns the clusters of an instance.
func (m *Mock) ListClusters(_ context.Context, instance string) ([]btdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := instance + "/clusters/"
	all := m.clusters.SortedValues()
	out := make([]btdriver.Cluster, 0, len(all))

	for i := range all {
		// Exclude backups' cluster path false-positives by requiring exactly one
		// segment after /clusters/.
		if strings.HasPrefix(all[i].Name, prefix) && !strings.Contains(strings.TrimPrefix(all[i].Name, prefix), "/") {
			out = append(out, cloneCluster(&all[i]))
		}
	}

	return out, nil
}

// UpdateCluster changes a cluster's serve-node count or autoscaling.
func (m *Mock) UpdateCluster(
	_ context.Context, name string, serveNodes int, autoscaling *btdriver.Autoscaling,
) (*btdriver.Cluster, *btdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	if autoscaling != nil {
		c.Autoscaling = cloneAutoscaling(autoscaling)
	} else if serveNodes > 0 {
		if err := validateServeNodes(serveNodes); err != nil {
			return nil, nil, err
		}

		c.ServeNodes = serveNodes
		c.Autoscaling = nil
	}

	m.clusters.Set(name, c)

	op := m.newOp("update-cluster", name)
	out := cloneCluster(&c)

	return &out, op, nil
}

// DeleteCluster removes a cluster and its backups. An instance must keep at
// least one cluster, so deleting the last one is rejected.
func (m *Mock) DeleteCluster(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	if m.clusterCount(parentName(name)) <= 1 {
		return cerrors.Newf(cerrors.FailedPrecondition, "cannot delete the last cluster of instance %q", parentName(name))
	}

	m.clusters.Delete(name)
	deletePrefixed(m.backups, name+"/")

	return nil
}

// clusterCount returns the number of clusters directly under an instance. The
// caller holds a lock.
func (m *Mock) clusterCount(instance string) int {
	prefix := instance + "/clusters/"
	n := 0

	for _, k := range m.clusters.Keys() {
		if strings.HasPrefix(k, prefix) && !strings.Contains(strings.TrimPrefix(k, prefix), "/") {
			n++
		}
	}

	return n
}

// GetClusterMemoryLayer reports the cluster's memory-layer status. The mock has
// no memory layer, so it validates existence and returns an empty response.
func (m *Mock) GetClusterMemoryLayer(_ context.Context, name string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	return nil
}
