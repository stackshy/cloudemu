package sagemaker

import (
	"context"
	"strconv"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// maxClusterInstancesPerGroup bounds the per-group instance count so an
// oversized (copy-paste or hostile) create can't make a later ListClusterNodes
// allocate unbounded node structs. The real HyperPod ceiling is in this range.
const maxClusterInstancesPerGroup = 6758

func (m *Mock) CreateCluster(_ context.Context, cfg driver.ClusterSpec) (*driver.Cluster, error) {
	if cfg.ClusterName == "" {
		return nil, errors.New(errors.InvalidArgument, "clusterName is required")
	}

	if err := validateInstanceGroups(cfg.InstanceGroups); err != nil {
		return nil, err
	}

	if m.clusters.Has(cfg.ClusterName) {
		return nil, errors.Newf(errors.AlreadyExists, "cluster %q already exists", cfg.ClusterName)
	}

	arn := m.arn("cluster/" + cfg.ClusterName)
	c := &driver.Cluster{
		ClusterName:    cfg.ClusterName,
		ClusterARN:     arn,
		Status:         driver.ClusterInService, // synchronous Creating -> InService
		InstanceGroups: toInstanceGroups(cfg.InstanceGroups),
		CreationTime:   m.now(),
		Tags:           copyTags(cfg.Tags),
	}
	m.clusters.Set(cfg.ClusterName, c)
	m.setTags(arn, cfg.Tags)
	m.emitResourceCreated("Cluster")

	return cloneCluster(c), nil
}

// validateInstanceGroups rejects negative or over-cap instance counts before
// they are stored.
func validateInstanceGroups(groups []driver.ClusterInstanceGroupSpec) error {
	for _, g := range groups {
		if g.InstanceCount < 0 {
			return errors.Newf(errors.InvalidArgument, "instance group %q has negative InstanceCount", g.GroupName)
		}

		if g.InstanceCount > maxClusterInstancesPerGroup {
			return errors.Newf(errors.InvalidArgument,
				"instance group %q InstanceCount %d exceeds the maximum of %d",
				g.GroupName, g.InstanceCount, maxClusterInstancesPerGroup)
		}
	}

	return nil
}

func toInstanceGroups(in []driver.ClusterInstanceGroupSpec) []driver.ClusterInstanceGroup {
	out := make([]driver.ClusterInstanceGroup, 0, len(in))
	for _, g := range in {
		out = append(out, driver.ClusterInstanceGroup(g))
	}

	return out
}

// cloneCluster deep-copies the instance-group and tag slices.
func cloneCluster(in *driver.Cluster) *driver.Cluster {
	out := *in
	if in.InstanceGroups != nil {
		out.InstanceGroups = make([]driver.ClusterInstanceGroup, len(in.InstanceGroups))
		copy(out.InstanceGroups, in.InstanceGroups)
	}

	out.Tags = copyTags(in.Tags)

	return &out
}

func (m *Mock) DescribeCluster(_ context.Context, name string) (*driver.Cluster, error) {
	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cluster %q not found", name)
	}

	return cloneCluster(c), nil
}

func (m *Mock) ListClusters(_ context.Context) ([]driver.Cluster, error) {
	all := m.clusters.All()
	out := make([]driver.Cluster, 0, len(all))

	for _, v := range all {
		out = append(out, *cloneCluster(v))
	}

	return out, nil
}

func (m *Mock) UpdateCluster(_ context.Context, name string, groups []driver.ClusterInstanceGroupSpec) (*driver.Cluster, error) {
	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cluster %q not found", name)
	}

	if err := validateInstanceGroups(groups); err != nil {
		return nil, err
	}

	updated := *c
	updated.InstanceGroups = toInstanceGroups(groups)
	updated.Status = driver.ClusterInService
	m.clusters.Set(name, &updated)

	return cloneCluster(&updated), nil
}

func (m *Mock) DeleteCluster(_ context.Context, name string) error {
	if !m.clusters.Has(name) {
		return errors.Newf(errors.NotFound, "cluster %q not found", name)
	}

	m.clusters.Delete(name)

	return nil
}

// ListClusterNodes synthesizes one node per instance, deterministically, from
// the cluster's instance-group configuration.
func (m *Mock) ListClusterNodes(_ context.Context, clusterName string) ([]driver.ClusterNode, error) {
	c, ok := m.clusters.Get(clusterName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cluster %q not found", clusterName)
	}

	out := make([]driver.ClusterNode, 0)

	for _, g := range c.InstanceGroups {
		for i := 0; i < g.InstanceCount; i++ {
			out = append(out, driver.ClusterNode{
				NodeID:       g.GroupName + "-" + strconv.Itoa(i),
				GroupName:    g.GroupName,
				InstanceType: g.InstanceType,
				Status:       "Running",
				LaunchTime:   c.CreationTime,
			})
		}
	}

	return out, nil
}

func (m *Mock) DescribeClusterNode(ctx context.Context, clusterName, nodeID string) (*driver.ClusterNode, error) {
	nodes, err := m.ListClusterNodes(ctx, clusterName)
	if err != nil {
		return nil, err
	}

	for i := range nodes {
		if nodes[i].NodeID == nodeID {
			out := nodes[i]

			return &out, nil
		}
	}

	return nil, errors.Newf(errors.NotFound, "cluster node %q not found", nodeID)
}
