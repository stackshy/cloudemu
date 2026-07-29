package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var (
	_ rdsdriver.ClusterEndpoints = (*Mock)(nil)
	_ rdsdriver.ClusterFailover  = (*Mock)(nil)
	_ rdsdriver.GlobalClusters   = (*Mock)(nil)
)

func clusterEndpointARN(region, accountID, id string) string {
	return idgen.AWSARN("rds", region, accountID, "cluster-endpoint:"+id)
}

func globalClusterARN(accountID, id string) string {
	// Global clusters are region-less; AWS uses an empty region segment.
	return idgen.AWSARN("rds", "", accountID, "global-cluster:"+id)
}

// ---- custom cluster endpoints ----

func (m *Mock) CreateDBClusterEndpoint(_ context.Context, cfg rdsdriver.ClusterEndpointConfig) (*rdsdriver.ClusterEndpoint, error) {
	if cfg.EndpointID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "DBClusterEndpointIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(cfg.ClusterID) {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", cfg.ClusterID)
	}

	if m.clusterEndpoints.Has(cfg.EndpointID) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "DB cluster endpoint %q already exists", cfg.EndpointID)
	}

	ep := rdsdriver.ClusterEndpoint{
		EndpointID:         cfg.EndpointID,
		ClusterID:          cfg.ClusterID,
		ARN:                clusterEndpointARN(m.opts.Region, m.opts.AccountID, cfg.EndpointID),
		Endpoint:           endpointFor(cfg.EndpointID, m.opts.Region, "cluster-custom"),
		Status:             rdsdriver.StateAvailable,
		EndpointType:       "CUSTOM",
		CustomEndpointType: cfg.EndpointType,
		StaticMembers:      append([]string(nil), cfg.StaticMembers...),
		ExcludedMembers:    append([]string(nil), cfg.ExcludedMembers...),
	}
	m.clusterEndpoints.Set(cfg.EndpointID, ep)

	out := ep

	return &out, nil
}

func (m *Mock) DescribeDBClusterEndpoints(_ context.Context, clusterID, endpointID string) ([]rdsdriver.ClusterEndpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if endpointID != "" {
		ep, ok := m.clusterEndpoints.Get(endpointID)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB cluster endpoint %q not found", endpointID)
		}

		return []rdsdriver.ClusterEndpoint{ep}, nil
	}

	all := m.clusterEndpoints.SortedValues()
	if clusterID == "" {
		return all, nil
	}

	out := make([]rdsdriver.ClusterEndpoint, 0, len(all))
	for _, ep := range all {
		if ep.ClusterID == clusterID {
			out = append(out, ep)
		}
	}

	return out, nil
}

func (m *Mock) ModifyDBClusterEndpoint(_ context.Context, endpointID string, input rdsdriver.ModifyClusterEndpointInput) (*rdsdriver.ClusterEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ep, ok := m.clusterEndpoints.Get(endpointID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster endpoint %q not found", endpointID)
	}

	if input.EndpointType != "" {
		ep.CustomEndpointType = input.EndpointType
	}

	if input.StaticMembers != nil {
		ep.StaticMembers = append([]string(nil), input.StaticMembers...)
	}

	if input.ExcludedMembers != nil {
		ep.ExcludedMembers = append([]string(nil), input.ExcludedMembers...)
	}

	m.clusterEndpoints.Set(endpointID, ep)

	out := ep

	return &out, nil
}

func (m *Mock) DeleteDBClusterEndpoint(_ context.Context, endpointID string) (*rdsdriver.ClusterEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ep, ok := m.clusterEndpoints.Get(endpointID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster endpoint %q not found", endpointID)
	}

	ep.Status = rdsdriver.StateDeleting
	m.clusterEndpoints.Delete(endpointID)

	out := ep

	return &out, nil
}

// ---- failover ----

func (m *Mock) FailoverDBCluster(_ context.Context, clusterID, targetInstanceID string) (*rdsdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(clusterID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", clusterID)
	}

	if targetInstanceID != "" {
		idx := indexOf(cluster.Members, targetInstanceID)
		if idx < 0 {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"target %q is not a member of DB cluster %q", targetInstanceID, clusterID)
		}

		// Promote the target to writer (index 0), preserving the order of the rest.
		cluster.Members = append([]string{targetInstanceID},
			append(append([]string(nil), cluster.Members[:idx]...), cluster.Members[idx+1:]...)...)
	} else if len(cluster.Members) > 1 {
		// No target: promote the first reader.
		cluster.Members = append(cluster.Members[1:], cluster.Members[0])
	}

	m.clusters.Set(clusterID, cluster)

	out := cluster

	return &out, nil
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}

	return -1
}

// ---- global clusters ----

func (m *Mock) CreateGlobalCluster(_ context.Context, cfg rdsdriver.GlobalClusterConfig) (*rdsdriver.GlobalCluster, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "GlobalClusterIdentifier is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.globalClusters.Has(cfg.ID) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "global cluster %q already exists", cfg.ID)
	}

	gc := rdsdriver.GlobalCluster{
		ID:            cfg.ID,
		ARN:           globalClusterARN(m.opts.AccountID, cfg.ID),
		Engine:        cfg.Engine,
		EngineVersion: cfg.EngineVersion,
		Status:        rdsdriver.StateAvailable,
	}

	if cfg.SourceDBClusterID != "" {
		src, ok := m.clusters.Get(cfg.SourceDBClusterID)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", cfg.SourceDBClusterID)
		}

		gc.Engine = src.Engine
		gc.EngineVersion = src.EngineVersion
		gc.Members = []rdsdriver.GlobalClusterMember{{DBClusterARN: src.ARN, IsWriter: true}}
	}

	m.globalClusters.Set(cfg.ID, gc)

	out := gc

	return &out, nil
}

func (m *Mock) DescribeGlobalClusters(_ context.Context, ids []string) ([]rdsdriver.GlobalCluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		return m.globalClusters.SortedValues(), nil
	}

	out := make([]rdsdriver.GlobalCluster, 0, len(ids))

	for _, id := range ids {
		gc, ok := m.globalClusters.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "global cluster %q not found", id)
		}

		out = append(out, gc)
	}

	return out, nil
}

func (m *Mock) ModifyGlobalCluster(_ context.Context, id, newID, engineVersion string) (*rdsdriver.GlobalCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gc, ok := m.globalClusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "global cluster %q not found", id)
	}

	if engineVersion != "" {
		gc.EngineVersion = engineVersion
	}

	if newID != "" && newID != id {
		if m.globalClusters.Has(newID) {
			return nil, cerrors.Newf(cerrors.AlreadyExists, "global cluster %q already exists", newID)
		}

		m.globalClusters.Delete(id)
		gc.ID = newID
		gc.ARN = globalClusterARN(m.opts.AccountID, newID)
	}

	m.globalClusters.Set(gc.ID, gc)

	out := gc

	return &out, nil
}

func (m *Mock) DeleteGlobalCluster(_ context.Context, id string) (*rdsdriver.GlobalCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gc, ok := m.globalClusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "global cluster %q not found", id)
	}

	m.globalClusters.Delete(id)

	out := gc

	return &out, nil
}

func (m *Mock) RemoveFromGlobalCluster(_ context.Context, id, clusterARN string) (*rdsdriver.GlobalCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gc, ok := m.globalClusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "global cluster %q not found", id)
	}

	kept := gc.Members[:0]
	for _, mem := range gc.Members {
		if mem.DBClusterARN != clusterARN {
			kept = append(kept, mem)
		}
	}

	gc.Members = kept
	m.globalClusters.Set(id, gc)

	out := gc

	return &out, nil
}
