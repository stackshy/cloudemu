// Package dataproc provides an in-memory mock of the Google Cloud Dataproc
// cluster control plane (dataproc.googleapis.com/v1). It models clusters and the
// long-running operations their mutating RPCs return. It is control-plane only:
// job execution, cluster start/stop, workflow templates, autoscaling policies,
// and any real Hadoop/Spark runtime are out of scope.
package dataproc

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataproc/driver"
)

var _ dpdriver.Dataproc = (*Mock)(nil)

const (
	defaultMasterInstances = 1
	defaultWorkerInstances = 2
	defaultMachineType     = "n2-standard-4"
	defaultBootDiskType    = "pd-standard"
	defaultBootDiskSizeGb  = 500
)

// Mock is the in-memory Dataproc control-plane implementation. Clusters are keyed
// by their full resource name (projects/{p}/regions/{r}/clusters/{c}).
type Mock struct {
	mu sync.RWMutex

	clusters   *memstore.Store[dpdriver.Cluster]
	operations *memstore.Store[dpdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Dataproc mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:   memstore.New[dpdriver.Cluster](),
		operations: memstore.New[dpdriver.Operation](),
		opts:       opts,
	}
}

// clusterName builds the full cluster resource name.
func clusterName(project, region, name string) string {
	return "projects/" + project + "/regions/" + region + "/clusters/" + name
}

// newOp records a completed operation scoped to the project+region it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, region, opType, target string) *dpdriver.Operation {
	scope := "projects/" + project + "/regions/" + region
	op := dpdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/dataproc-%s-%d", scope, opType, m.opSeq.Add(1)),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// CreateCluster provisions a new cluster, RUNNING immediately.
func (m *Mock) CreateCluster(
	_ context.Context, cfg *dpdriver.CreateClusterConfig,
) (*dpdriver.Cluster, *dpdriver.Operation, error) {
	if cfg.ClusterName == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "clusterName is required")
	}

	if cfg.Region == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "region is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterName(cfg.ProjectID, cfg.Region, cfg.ClusterName)
	if m.clusters.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "cluster %q already exists", cfg.ClusterName)
	}

	now := m.opts.Clock.Now().UTC()

	cluster := dpdriver.Cluster{
		ProjectID:   cfg.ProjectID,
		Region:      cfg.Region,
		ClusterName: cfg.ClusterName,
		ClusterUUID: idgen.UUID(),
		Config:      normalizeConfig(&cfg.Config, cfg.ProjectID, cfg.Region, cfg.ClusterName),
		Labels:      copyLabels(cfg.Labels),
		Status: dpdriver.ClusterStatus{
			State:          dpdriver.StateRunning,
			StateStartTime: now,
		},
		StatusHistory: []dpdriver.ClusterStatus{
			{State: dpdriver.StateCreating, StateStartTime: now},
		},
	}
	m.clusters.Set(key, cluster)

	op := m.newOp(cfg.ProjectID, cfg.Region, "create", key)
	out := cloneCluster(&cluster)

	return &out, op, nil
}

// GetCluster returns a cluster by project/region/name.
func (m *Mock) GetCluster(_ context.Context, project, region, name string) (*dpdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterName(project, region, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found in region %q", name, region)
	}

	out := cloneCluster(&c)

	return &out, nil
}

// ListClusters returns every cluster in a project+region, ordered by name.
func (m *Mock) ListClusters(_ context.Context, project, region string) ([]dpdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/regions/" + region + "/clusters/"
	all := m.clusters.SortedValues()
	out := make([]dpdriver.Cluster, 0, len(all))

	for i := range all {
		key := clusterName(all[i].ProjectID, all[i].Region, all[i].ClusterName)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneCluster(&all[i]))
		}
	}

	return out, nil
}

// UpdateCluster applies a masked update to a cluster and returns the LRO.
func (m *Mock) UpdateCluster(
	_ context.Context, project, region, name string, cfg dpdriver.UpdateClusterConfig,
) (*dpdriver.Cluster, *dpdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterName(project, region, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found in region %q", name, region)
	}

	applyUpdate(&c, cfg, project, region)
	m.clusters.Set(key, c)

	op := m.newOp(project, region, "update", key)
	out := cloneCluster(&c)

	return &out, op, nil
}

// applyUpdate mutates c per cfg's field mask. Only the masked fields are written,
// matching real Dataproc, which requires an explicit updateMask.
func applyUpdate(c *dpdriver.Cluster, cfg dpdriver.UpdateClusterConfig, project, region string) {
	if maskHas(cfg.FieldMask, "config.worker_config.num_instances") {
		if c.Config.WorkerConfig == nil {
			c.Config.WorkerConfig = &dpdriver.InstanceGroupConfig{}
		}

		c.Config.WorkerConfig.NumInstances = cfg.WorkerNumInstances
		c.Config.WorkerConfig.InstanceNames = instanceNames(c.ClusterName, "w", cfg.WorkerNumInstances)
	}

	if maskHas(cfg.FieldMask, "config.secondary_worker_config.num_instances") {
		if c.Config.SecondaryWorkerConfig == nil {
			c.Config.SecondaryWorkerConfig = &dpdriver.InstanceGroupConfig{}
		}

		c.Config.SecondaryWorkerConfig.NumInstances = cfg.SecondaryWorkerNumInstances
		c.Config.SecondaryWorkerConfig.InstanceNames = instanceNames(c.ClusterName, "sw", cfg.SecondaryWorkerNumInstances)
	}

	if maskHas(cfg.FieldMask, "labels") {
		c.Labels = copyLabels(cfg.Labels)
	}

	_ = project
	_ = region
}

// DeleteCluster removes a cluster and returns the LRO.
func (m *Mock) DeleteCluster(_ context.Context, project, region, name string) (*dpdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterName(project, region, name)
	if !m.clusters.Has(key) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found in region %q", name, region)
	}

	m.clusters.Delete(key)

	return m.newOp(project, region, "delete", key), nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op id
// an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*dpdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &dpdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// normalizeConfig fills the service-supplied defaults a real Dataproc create
// applies: a default master/worker instance group when the block is omitted, a
// default image version, generated staging/temp buckets, per-instance names, and
// default machine type / boot disk on each present group. Everything the caller
// supplied is echoed verbatim so a Terraform read does not drift.
func normalizeConfig(cfg *dpdriver.ClusterConfig, project, region, name string) dpdriver.ClusterConfig {
	out := *cfg

	if out.ConfigBucket == "" {
		out.ConfigBucket = generatedBucket("staging", region, project, name)
	}

	if out.TempBucket == "" {
		out.TempBucket = generatedBucket("temp", region, project, name)
	}

	out.SoftwareConfig = normalizeSoftware(out.SoftwareConfig)

	if out.MasterConfig == nil {
		out.MasterConfig = &dpdriver.InstanceGroupConfig{NumInstances: defaultMasterInstances}
	}

	if out.WorkerConfig == nil {
		out.WorkerConfig = &dpdriver.InstanceGroupConfig{NumInstances: defaultWorkerInstances}
	}

	out.MasterConfig = normalizeGroup(out.MasterConfig, name, "m")
	out.WorkerConfig = normalizeGroup(out.WorkerConfig, name, "w")

	if out.SecondaryWorkerConfig != nil {
		out.SecondaryWorkerConfig = normalizeGroup(out.SecondaryWorkerConfig, name, "sw")
	}

	return out
}

func normalizeSoftware(sw *dpdriver.SoftwareConfig) *dpdriver.SoftwareConfig {
	if sw == nil {
		sw = &dpdriver.SoftwareConfig{}
	}

	if sw.ImageVersion == "" {
		sw.ImageVersion = dpdriver.DefaultImageVersion
	}

	return sw
}

// normalizeGroup fills an instance group's service-supplied defaults and
// synthesizes its instanceNames from the final instance count.
func normalizeGroup(g *dpdriver.InstanceGroupConfig, cluster, role string) *dpdriver.InstanceGroupConfig {
	if g.MachineTypeURI == "" {
		g.MachineTypeURI = defaultMachineType
	}

	if g.DiskConfig == nil {
		g.DiskConfig = &dpdriver.DiskConfig{}
	}

	if g.DiskConfig.BootDiskType == "" {
		g.DiskConfig.BootDiskType = defaultBootDiskType
	}

	if g.DiskConfig.BootDiskSizeGb == 0 {
		g.DiskConfig.BootDiskSizeGb = defaultBootDiskSizeGb
	}

	g.InstanceNames = instanceNames(cluster, role, g.NumInstances)

	return g
}

// instanceNames synthesizes the deterministic per-VM names Dataproc reports for
// an instance group. A single master is "{cluster}-m"; every other case is
// "{cluster}-{role}-{i}".
func instanceNames(cluster, role string, count int64) []string {
	if count <= 0 {
		return nil
	}

	if role == "m" && count == 1 {
		return []string{cluster + "-m"}
	}

	// Bound the per-group instance count (originating from the request's
	// NumInstances) with an explicit comparison immediately before the allocation
	// it sizes. A real Dataproc cluster stays far under this; the ceiling only
	// stops a pathological value from driving an unbounded slice.
	const maxInstanceGroupSize = 10000
	if count > maxInstanceGroupSize {
		count = maxInstanceGroupSize
	}

	names := make([]string, 0, count)
	for i := int64(0); i < count; i++ {
		names = append(names, cluster+"-"+role+"-"+strconv.FormatInt(i, 10))
	}

	return names
}

// generatedBucket derives the deterministic staging/temp bucket name real
// Dataproc auto-creates for a cluster that specifies none. It is stable across
// reads so a Terraform refresh of the computed `bucket` field does not drift.
func generatedBucket(kind, region, project, cluster string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(project + "/" + region + "/" + cluster))

	return fmt.Sprintf("dataproc-%s-%s-%08x", kind, region, h.Sum32())
}

func maskHas(mask []string, field string) bool {
	for _, p := range mask {
		if p == field {
			return true
		}
	}

	return false
}

func copyLabels(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}
