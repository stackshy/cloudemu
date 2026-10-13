package sagemaker

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// clusterTransition is the in-flight (read-time) state of a cluster that is
// Creating or Updating under --async-settle: the store holds the final state and
// reads overlay this window against the Clock. prevCounts is what each group was
// running before the change. Transitions are not snapshotted.
type clusterTransition struct {
	kind       string
	readyAt    time.Time
	prevCounts map[string]int
}

func (m *Mock) CreateCluster(_ context.Context, cfg driver.ClusterSpec) (*driver.Cluster, error) {
	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	if cfg.ClusterName == "" {
		return nil, errors.New(errors.InvalidArgument, "clusterName is required")
	}

	if err := validateInstanceGroups(cfg.InstanceGroups); err != nil {
		return nil, err
	}

	m.clMu.Lock()
	defer m.clMu.Unlock()

	arn := m.arn("cluster/" + cfg.ClusterName)
	c := &driver.Cluster{
		ClusterName:    cfg.ClusterName,
		ClusterARN:     arn,
		Status:         driver.ClusterInService,
		InstanceGroups: toInstanceGroups(cfg.InstanceGroups),
		CreationTime:   m.now(),
		Tags:           copyTags(cfg.Tags),
	}

	if !m.clusters.SetIfAbsent(cfg.ClusterName, c) {
		return nil, driver.InUse("cluster %q already exists", cfg.ClusterName)
	}

	m.setTags(arn, cfg.Tags)
	m.beginClusterTransition(cfg.ClusterName, driver.ClusterCreating, nil)

	return m.observeCluster(c), nil
}

// beginClusterTransition records an in-flight window; with --async-settle off
// nothing is recorded. The caller holds clMu.
func (m *Mock) beginClusterTransition(name, kind string, prev []driver.ClusterInstanceGroup) {
	dur := m.opts.SettleDuration(settle.DefaultClusterSettle)
	if dur <= 0 {
		delete(m.clTrans, name)

		return
	}

	counts := make(map[string]int, len(prev))
	for _, g := range prev {
		counts[g.GroupName] = g.CurrentCount
	}

	m.clTrans[name] = &clusterTransition{kind: kind, readyAt: m.opts.Clock.Now().Add(dur), prevCounts: counts}
}

// observeCluster returns a deep copy of c with the transition overlay applied.
func (m *Mock) observeCluster(c *driver.Cluster) *driver.Cluster {
	out := cloneCluster(c)

	tr, ok := m.clTrans[c.ClusterName]
	if !ok || !m.opts.Clock.Now().Before(tr.readyAt) {
		return out
	}

	out.Status = tr.kind

	for i := range out.InstanceGroups {
		g := &out.InstanceGroups[i]
		g.Status = driver.GroupCreating

		if tr.kind == driver.ClusterUpdating {
			g.Status = driver.GroupUpdating
		}

		g.CurrentCount = tr.prevCounts[g.GroupName]
	}

	return out
}

// validateInstanceGroups checks group specs: names, counts, threads per core,
// group count and the cluster-wide instance cap.
func validateInstanceGroups(groups []driver.ClusterInstanceGroupSpec) error {
	if len(groups) > driver.MaxClusterInstanceGroups {
		return errors.Newf(errors.InvalidArgument, "a cluster has at most %d instance groups", driver.MaxClusterInstanceGroups)
	}

	seen := make(map[string]bool, len(groups))
	total := 0

	for i := range groups {
		g := &groups[i]

		if seen[g.GroupName] {
			return errors.Newf(errors.InvalidArgument, "instance group %q is specified more than once", g.GroupName)
		}

		seen[g.GroupName] = true

		if err := validateGroup(g); err != nil {
			return err
		}

		total += g.InstanceCount
	}

	if total > driver.MaxClusterInstances {
		return errors.Newf(errors.ResourceExhausted, "a cluster supports at most %d instances in total", driver.MaxClusterInstances)
	}

	return nil
}

func validateGroup(g *driver.ClusterInstanceGroupSpec) error {
	if g.InstanceCount < 0 {
		return errors.Newf(errors.InvalidArgument, "instance group %q has negative InstanceCount", g.GroupName)
	}

	if g.InstanceCount > driver.MaxClusterInstancesPerGroup {
		return errors.Newf(errors.InvalidArgument,
			"instance group %q InstanceCount %d exceeds the maximum of %d",
			g.GroupName, g.InstanceCount, driver.MaxClusterInstancesPerGroup)
	}

	if g.MinInstanceCount < 0 || g.MinInstanceCount > g.InstanceCount {
		return errors.Newf(errors.InvalidArgument,
			"instance group %q MinInstanceCount must be between 0 and InstanceCount", g.GroupName)
	}

	if g.ThreadsPerCore != 0 && g.ThreadsPerCore != 1 && g.ThreadsPerCore != 2 {
		return errors.Newf(errors.InvalidArgument, "instance group %q ThreadsPerCore must be 1 or 2", g.GroupName)
	}

	return nil
}

func toInstanceGroups(in []driver.ClusterInstanceGroupSpec) []driver.ClusterInstanceGroup {
	out := make([]driver.ClusterInstanceGroup, 0, len(in))
	for i := range in {
		out = append(out, groupFromSpec(&in[i]))
	}

	return out
}

// groupFromSpec materializes a group: in synchronous mode it is running at its
// target immediately.
func groupFromSpec(g *driver.ClusterInstanceGroupSpec) driver.ClusterInstanceGroup {
	threads := g.ThreadsPerCore
	if threads == 0 {
		threads = driver.DefaultThreadsPerCore
	}

	var lc *driver.LifeCycleConfig

	if g.LifeCycleConfig != nil {
		c := *g.LifeCycleConfig
		lc = &c
	}

	return driver.ClusterInstanceGroup{
		GroupName:       g.GroupName,
		InstanceType:    g.InstanceType,
		CurrentCount:    g.InstanceCount,
		TargetCount:     g.InstanceCount,
		MinCount:        g.MinInstanceCount,
		ThreadsPerCore:  threads,
		ExecutionRole:   g.ExecutionRole,
		LifeCycleConfig: lc,
		Status:          driver.GroupInService,
	}
}

// cloneCluster deep-copies the instance groups (including each group's
// lifecycle-config pointer) and the tags.
func cloneCluster(in *driver.Cluster) *driver.Cluster {
	out := *in

	if in.InstanceGroups != nil {
		out.InstanceGroups = make([]driver.ClusterInstanceGroup, len(in.InstanceGroups))

		for i, g := range in.InstanceGroups {
			if g.LifeCycleConfig != nil {
				c := *g.LifeCycleConfig
				g.LifeCycleConfig = &c
			}

			out.InstanceGroups[i] = g
		}
	}

	out.Tags = copyTags(in.Tags)

	return &out
}

func (m *Mock) DescribeCluster(_ context.Context, name string) (*driver.Cluster, error) {
	m.clMu.Lock()
	defer m.clMu.Unlock()

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cluster %q not found", name)
	}

	return m.observeCluster(c), nil
}

//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) ListClusters(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Cluster], error) {
	m.clMu.Lock()
	stored := m.clusters.SortedValues()
	observed := make([]*driver.Cluster, 0, len(stored))

	for _, c := range stored {
		observed = append(observed, m.observeCluster(c))
	}
	m.clMu.Unlock()

	return listItems(observed, listSpec[driver.Cluster]{
		name:    func(t *driver.Cluster) string { return t.ClusterName },
		status:  func(t *driver.Cluster) string { return t.Status },
		created: func(t *driver.Cluster) string { return t.CreationTime },
	}, opts)
}

// UpdateCluster merges the update into the cluster: groups named in the request
// are updated (or added), groups listed for deletion are removed, all others are
// kept. The cluster must be InService.
func (m *Mock) UpdateCluster(_ context.Context, up driver.ClusterUpdate) (*driver.Cluster, error) {
	if len(up.InstanceGroups) == 0 && len(up.InstanceGroupsToDelete) == 0 {
		return nil, errors.New(errors.InvalidArgument, "specify instanceGroups, instanceGroupsToDelete, or both")
	}

	m.clMu.Lock()
	defer m.clMu.Unlock()

	c, ok := m.clusters.Get(up.ClusterName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cluster %q not found", up.ClusterName)
	}

	current := m.observeCluster(c)
	if current.Status != driver.ClusterInService {
		return nil, driver.Conflict("Cluster %q is %s and cannot be updated", up.ClusterName, current.Status)
	}

	groups, err := mergeInstanceGroups(current.InstanceGroups, &up)
	if err != nil {
		return nil, err
	}

	updated := *current
	updated.InstanceGroups = groups
	updated.Status = driver.ClusterInService
	m.clusters.Set(up.ClusterName, &updated)
	m.beginClusterTransition(up.ClusterName, driver.ClusterUpdating, current.InstanceGroups)

	return m.observeCluster(&updated), nil
}

// mergeInstanceGroups applies update/add/delete onto the existing groups and
// validates the result.
//
//nolint:gocyclo // validation branches mirror the service's rules
func mergeInstanceGroups(existing []driver.ClusterInstanceGroup, up *driver.ClusterUpdate) ([]driver.ClusterInstanceGroup, error) {
	if err := validateInstanceGroups(up.InstanceGroups); err != nil {
		return nil, err
	}

	index := make(map[string]int, len(existing))
	for i := range existing {
		index[existing[i].GroupName] = i
	}

	updating := make(map[string]bool, len(up.InstanceGroups))
	for i := range up.InstanceGroups {
		updating[up.InstanceGroups[i].GroupName] = true
	}

	deleting := make(map[string]bool, len(up.InstanceGroupsToDelete))

	for _, n := range up.InstanceGroupsToDelete {
		if _, ok := index[n]; !ok {
			return nil, errors.Newf(errors.InvalidArgument, "instance group %q to delete does not exist", n)
		}

		if updating[n] {
			return nil, errors.Newf(errors.InvalidArgument, "instance group %q is both updated and deleted", n)
		}

		deleting[n] = true
	}

	out := make([]driver.ClusterInstanceGroup, 0, len(existing)+len(up.InstanceGroups))

	for i := range existing {
		if !deleting[existing[i].GroupName] {
			out = append(out, existing[i])
		}
	}

	for i := range up.InstanceGroups {
		g := groupFromSpec(&up.InstanceGroups[i])
		if at, ok := indexOf(out, g.GroupName); ok {
			out[at] = g
		} else {
			out = append(out, g)
		}
	}

	if len(out) > driver.MaxClusterInstanceGroups {
		return nil, errors.Newf(errors.InvalidArgument, "a cluster has at most %d instance groups", driver.MaxClusterInstanceGroups)
	}

	total := 0
	for i := range out {
		total += out[i].TargetCount
	}

	if total > driver.MaxClusterInstances {
		return nil, errors.Newf(errors.ResourceExhausted, "a cluster supports at most %d instances in total", driver.MaxClusterInstances)
	}

	return out, nil
}

func indexOf(groups []driver.ClusterInstanceGroup, name string) (int, bool) {
	for i := range groups {
		if groups[i].GroupName == name {
			return i, true
		}
	}

	return 0, false
}

func (m *Mock) DeleteCluster(_ context.Context, name string) error {
	m.clMu.Lock()
	defer m.clMu.Unlock()

	if !m.clusters.Has(name) {
		return errors.Newf(errors.NotFound, "cluster %q not found", name)
	}

	m.clusters.Delete(name)
	delete(m.clTrans, name)
	m.dropTags(m.arn("cluster/" + name))

	return nil
}

// ListClusterNodes lists the cluster's nodes with the SDK's filters (group name
// contains, creation time), sort and paging.
//
//nolint:gocritic // opts matches the driver signature
func (m *Mock) ListClusterNodes(
	ctx context.Context, clusterName string, opts driver.ListOptions,
) (driver.ListPage[driver.ClusterNode], error) {
	nodes, err := m.clusterNodes(ctx, clusterName)
	if err != nil {
		return driver.ListPage[driver.ClusterNode]{}, err
	}

	items := make([]*driver.ClusterNode, 0, len(nodes))
	for i := range nodes {
		items = append(items, &nodes[i])
	}

	return listItems(items, listSpec[driver.ClusterNode]{
		name:    func(t *driver.ClusterNode) string { return t.NodeID },
		created: func(t *driver.ClusterNode) string { return t.LaunchTime },
		extra: func(t *driver.ClusterNode, f map[string]string) bool {
			return strings.Contains(t.GroupName, f["InstanceGroupNameContains"])
		},
	}, opts)
}

// clusterNodes synthesizes one node per instance, deterministically, from the
// cluster's instance-group configuration.
func (m *Mock) clusterNodes(ctx context.Context, clusterName string) ([]driver.ClusterNode, error) {
	c, err := m.DescribeCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}

	out := make([]driver.ClusterNode, 0)

	for _, g := range c.InstanceGroups {
		// Bounded by validation (MaxClusterInstances in total); the explicit
		// guards keep the allocation provably capped.
		count := g.CurrentCount
		if count > driver.MaxClusterInstancesPerGroup {
			count = driver.MaxClusterInstancesPerGroup
		}

		for i := 0; i < count && len(out) < driver.MaxClusterInstances; i++ {
			out = append(out, driver.ClusterNode{
				NodeID:       g.GroupName + "-" + strconv.Itoa(i),
				GroupName:    g.GroupName,
				InstanceType: g.InstanceType,
				Status:       nodeStatus(g.Status),
				LaunchTime:   c.CreationTime,
			})
		}
	}

	return out, nil
}

func (m *Mock) DescribeClusterNode(ctx context.Context, clusterName, nodeID string) (*driver.ClusterNode, error) {
	nodes, err := m.clusterNodes(ctx, clusterName)
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

// nodeStatus is the status of a node in a group with the given status.
func nodeStatus(groupStatus string) string {
	if groupStatus == driver.GroupInService {
		return "Running"
	}

	return "Pending"
}
