// Package gke provides an in-memory mock of GCP Kubernetes Engine (GKE).
//
// Wave 1 covers only the GKE control-plane: Clusters, NodePools, and the
// long-running Operations they emit. The Kubernetes data-plane API
// (Deployments, StatefulSets, Pods, Services) is intentionally out of scope
// and will be wired up in Wave 2 alongside a Kubernetes API server stub.
//
// To reflect that, GetCluster returns a stub Endpoint
// (https://GKE-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local) and a stub CA
// certificate so that tooling like `gcloud container clusters get-credentials`
// can render a kubeconfig — actual API calls against the kubeconfig will fail
// until Wave 2 lands.
package gke

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

// Stub values used in Cluster responses until the Kubernetes data-plane
// arrives in Wave 2.
const (
	StubEndpoint    = "GKE-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local"
	StubCACert      = "MIIBkTCB+wIJAOdjUjcyKZUyMA0GCSqGSIb3DQEBBQUAMA0xCzAJBgNVBAYTAlVT" // dummy base64 blob
	StubMasterVer   = "1.30.0-gke.0"
	stubNodeVersion = "1.30.0-gke.0"
)

const (
	cpuMetricRunning   = 0.25
	memMetricRunning   = 1024.0 * 1024.0 * 256.0 // 256 MiB
	defaultNodeCount   = 1
	defaultMachineType = "e2-medium"
	defaultDiskSizeGB  = 100
)

// Cluster is the in-memory representation of a GKE cluster. The shape mirrors
// only the fields the SDK round-trip cares about; the handler layer maps
// these to the wire shape google.golang.org/api/container/v1.Cluster expects.
type Cluster struct {
	Name              string
	Location          string
	Description       string
	Network           string
	Subnetwork        string
	InitialNodeCount  int64
	NodeIPv4CIDRSize  int64
	ClusterIPv4CIDR   string
	LoggingService    string
	MonitoringService string
	LegacyAbacEnabled bool
	NetworkPolicy     bool
	MasterUsername    string
	ResourceLabels    map[string]string
	MaintenanceWindow string // RFC-3339 daily window encoding; empty = none.
	IPRotationActive  bool
	NodePoolNames     []string
	Status            string
	CreatedAt         time.Time
}

// NodePool is the in-memory representation of a GKE node pool.
type NodePool struct {
	Name              string
	ClusterName       string
	Location          string
	NodeCount         int64
	MachineType       string
	DiskSizeGB        int64
	Version           string
	AutoscalingMin    int64
	AutoscalingMax    int64
	AutoscalingOn     bool
	AutoUpgrade       bool
	AutoRepair        bool
	Status            string
	UpgradeRolledBack bool
	CreatedAt         time.Time
}

// Operation tracks GKE long-running operations. The mock completes every
// operation immediately (status=DONE) so SDK pollers terminate on the first
// poll.
type Operation struct {
	Name          string
	Location      string
	OperationType string
	Status        string
	TargetLink    string
	StartTime     time.Time
	EndTime       time.Time
}

// Mock is the in-memory GKE backend.
type Mock struct {
	mu sync.RWMutex

	clusters   *memstore.Store[Cluster]
	nodePools  *memstore.Store[NodePool] // keyed by clusterName + "/" + nodePoolName
	operations *memstore.Store[Operation]

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new GKE mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:   memstore.New[Cluster](),
		nodePools:  memstore.New[NodePool](),
		operations: memstore.New[Operation](),
		opts:       opts,
	}
}

// SetMonitoring wires a Cloud Monitoring backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// emitClusterMetrics pushes container.googleapis.com metrics for a cluster.
// Real GKE samples per-container/per-node metrics; the mock emits a single
// representative datum per metric per cluster so SDK consumers see traffic.
func (m *Mock) emitClusterMetrics(clusterName string) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{
		"project_id":   m.opts.ProjectID,
		"cluster_name": clusterName,
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: "container.googleapis.com", MetricName: "container/cpu/usage_time",
			Value: cpuMetricRunning, Unit: "s", Dimensions: dims, Timestamp: now},
		{Namespace: "container.googleapis.com", MetricName: "container/memory/used_bytes",
			Value: memMetricRunning, Unit: "By", Dimensions: dims, Timestamp: now},
		{Namespace: "container.googleapis.com", MetricName: "node/count",
			Value: 1, Unit: "1", Dimensions: dims, Timestamp: now},
	})
}

func (m *Mock) recordOperation(opType, location, target string) Operation {
	now := m.opts.Clock.Now().UTC()
	op := Operation{
		Name:          idgen.GenerateID("operation-"),
		Location:      location,
		OperationType: opType,
		Status:        "DONE",
		TargetLink:    target,
		StartTime:     now,
		EndTime:       now,
	}
	m.operations.Set(op.Name, op)

	return op
}

// CreateClusterInput captures the subset of CreateCluster we honor.
type CreateClusterInput struct {
	Name              string
	Location          string
	Description       string
	Network           string
	Subnetwork        string
	InitialNodeCount  int64
	LoggingService    string
	MonitoringService string
	ResourceLabels    map[string]string
	NodePools         []NodePoolSpec
}

// NodePoolSpec captures the node-pool fields we keep when bootstrapping a
// cluster from a CreateClusterRequest.
type NodePoolSpec struct {
	Name             string
	InitialNodeCount int64
	MachineType      string
	DiskSizeGB       int64
	Version          string
	AutoscalingMin   int64
	AutoscalingMax   int64
	AutoscalingOn    bool
}

// CreateCluster registers a new cluster and any nested node pools.
func (m *Mock) CreateCluster(_ context.Context, input *CreateClusterInput) (*Cluster, *Operation, error) {
	if input.Name == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "cluster name is required")
	}

	if input.Location == "" {
		input.Location = m.opts.Region
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(input.Location, input.Name)
	if _, ok := m.clusters.Get(key); ok {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "cluster %q already exists", input.Name)
	}

	cluster := Cluster{
		Name:              input.Name,
		Location:          input.Location,
		Description:       input.Description,
		Network:           defaultIfEmpty(input.Network, "default"),
		Subnetwork:        defaultIfEmpty(input.Subnetwork, "default"),
		InitialNodeCount:  input.InitialNodeCount,
		NodeIPv4CIDRSize:  24,
		ClusterIPv4CIDR:   "10.0.0.0/14",
		LoggingService:    defaultIfEmpty(input.LoggingService, "logging.googleapis.com/kubernetes"),
		MonitoringService: defaultIfEmpty(input.MonitoringService, "monitoring.googleapis.com/kubernetes"),
		ResourceLabels:    copyLabels(input.ResourceLabels),
		Status:            "RUNNING",
		CreatedAt:         m.opts.Clock.Now().UTC(),
	}

	// Bootstrap default node pool when none specified — matches real GKE.
	pools := input.NodePools
	if len(pools) == 0 {
		count := input.InitialNodeCount
		if count == 0 {
			count = defaultNodeCount
		}

		pools = []NodePoolSpec{{
			Name:             "default-pool",
			InitialNodeCount: count,
			MachineType:      defaultMachineType,
			DiskSizeGB:       defaultDiskSizeGB,
			Version:          stubNodeVersion,
		}}
	}

	for i := range pools {
		np := nodePoolFromSpec(&pools[i], input.Name, input.Location, m.opts.Clock.Now().UTC())
		m.nodePools.Set(nodePoolKey(input.Location, input.Name, np.Name), np)
		cluster.NodePoolNames = append(cluster.NodePoolNames, np.Name)
	}

	m.clusters.Set(key, cluster)

	op := m.recordOperation("CREATE_CLUSTER", input.Location,
		"projects/"+m.opts.ProjectID+"/locations/"+input.Location+"/clusters/"+input.Name)

	m.emitClusterMetrics(input.Name)

	out := cluster

	return &out, &op, nil
}

func nodePoolFromSpec(spec *NodePoolSpec, clusterName, location string, now time.Time) NodePool {
	count := spec.InitialNodeCount
	if count == 0 {
		count = defaultNodeCount
	}

	return NodePool{
		Name:           spec.Name,
		ClusterName:    clusterName,
		Location:       location,
		NodeCount:      count,
		MachineType:    defaultIfEmpty(spec.MachineType, defaultMachineType),
		DiskSizeGB:     defaultIfZero(spec.DiskSizeGB, defaultDiskSizeGB),
		Version:        defaultIfEmpty(spec.Version, stubNodeVersion),
		AutoscalingMin: spec.AutoscalingMin,
		AutoscalingMax: spec.AutoscalingMax,
		AutoscalingOn:  spec.AutoscalingOn,
		AutoUpgrade:    true,
		AutoRepair:     true,
		Status:         "RUNNING",
		CreatedAt:      now,
	}
}

// GetCluster returns a single cluster by location+name.
func (m *Mock) GetCluster(_ context.Context, location, name string) (*Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(location, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found in %q", name, location)
	}

	out := c

	return &out, nil
}

// ListClusters returns clusters in a location ("-" for all locations).
func (m *Mock) ListClusters(_ context.Context, location string) ([]Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.clusters.All()
	out := make([]Cluster, 0, len(all))

	//nolint:gocritic // map values are sized for accuracy.
	for _, c := range all {
		if location != "" && location != "-" && c.Location != location {
			continue
		}

		out = append(out, c)
	}

	return out, nil
}

// UpdateClusterInput is the subset of UpdateCluster we honor.
type UpdateClusterInput struct {
	LoggingService    string
	MonitoringService string
	NodeVersion       string
	MasterVersion     string
	ResourceLabels    map[string]string
}

// UpdateCluster applies a partial update.
func (m *Mock) UpdateCluster(
	_ context.Context, location, name string, input UpdateClusterInput,
) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		if input.LoggingService != "" {
			c.LoggingService = input.LoggingService
		}

		if input.MonitoringService != "" {
			c.MonitoringService = input.MonitoringService
		}

		if input.ResourceLabels != nil {
			c.ResourceLabels = copyLabels(input.ResourceLabels)
		}
	})
}

// DeleteCluster removes a cluster and its node pools.
func (m *Mock) DeleteCluster(_ context.Context, location, name string) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(location, name)
	if !m.clusters.Has(key) {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found in %q", name, location)
	}

	m.clusters.Delete(key)

	prefix := location + "/" + name + "/"
	for _, k := range m.nodePools.Keys() {
		if hasPrefix(k, prefix) {
			m.nodePools.Delete(k)
		}
	}

	op := m.recordOperation("DELETE_CLUSTER", location,
		"projects/"+m.opts.ProjectID+"/locations/"+location+"/clusters/"+name)

	return &op, nil
}

// SetClusterLogging implements :setLogging (logging-service URI).
func (m *Mock) SetClusterLogging(_ context.Context, location, name, service string) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.LoggingService = service
	})
}

// SetClusterMonitoring implements :setMonitoring (monitoring-service URI).
func (m *Mock) SetClusterMonitoring(_ context.Context, location, name, service string) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.MonitoringService = service
	})
}

// SetMasterAuth implements :setMasterAuth (basic-auth username/password).
func (m *Mock) SetMasterAuth(_ context.Context, location, name, username string) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.MasterUsername = username
	})
}

// SetLegacyAbac implements :setLegacyAbac.
func (m *Mock) SetLegacyAbac(_ context.Context, location, name string, enabled bool) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.LegacyAbacEnabled = enabled
	})
}

// SetNetworkPolicy implements :setNetworkPolicy.
func (m *Mock) SetNetworkPolicy(_ context.Context, location, name string, enabled bool) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.NetworkPolicy = enabled
	})
}

// SetMaintenancePolicy implements :setMaintenancePolicy.
func (m *Mock) SetMaintenancePolicy(_ context.Context, location, name, window string) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.MaintenanceWindow = window
	})
}

// SetResourceLabels implements :setResourceLabels.
func (m *Mock) SetResourceLabels(
	_ context.Context, location, name string, labels map[string]string,
) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.ResourceLabels = copyLabels(labels)
	})
}

// StartIPRotation implements :startIpRotation.
func (m *Mock) StartIPRotation(_ context.Context, location, name string) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.IPRotationActive = true
	})
}

// CompleteIPRotation implements :completeIpRotation.
func (m *Mock) CompleteIPRotation(_ context.Context, location, name string) (*Operation, error) {
	return m.mutateCluster(location, name, func(c *Cluster) {
		c.IPRotationActive = false
	})
}

func (m *Mock) mutateCluster(
	location, name string, fn func(*Cluster),
) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(location, name)

	c, ok := m.clusters.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found in %q", name, location)
	}

	fn(&c)

	m.clusters.Set(key, c)

	op := m.recordOperation("UPDATE_CLUSTER", location,
		"projects/"+m.opts.ProjectID+"/locations/"+location+"/clusters/"+name)

	return &op, nil
}

// CreateNodePool registers a new node pool inside an existing cluster.
func (m *Mock) CreateNodePool(
	_ context.Context, location, clusterName string, spec *NodePoolSpec,
) (*NodePool, *Operation, error) {
	if spec.Name == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "node pool name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cKey := clusterKey(location, clusterName)

	cluster, ok := m.clusters.Get(cKey)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found in %q", clusterName, location)
	}

	npKey := nodePoolKey(location, clusterName, spec.Name)
	if _, exists := m.nodePools.Get(npKey); exists {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "node pool %q already exists in cluster %q", spec.Name, clusterName)
	}

	np := nodePoolFromSpec(spec, clusterName, location, m.opts.Clock.Now().UTC())
	m.nodePools.Set(npKey, np)

	cluster.NodePoolNames = append(cluster.NodePoolNames, np.Name)
	m.clusters.Set(cKey, cluster)

	op := m.recordOperation("CREATE_NODE_POOL", location,
		"projects/"+m.opts.ProjectID+"/locations/"+location+"/clusters/"+clusterName+"/nodePools/"+spec.Name)

	out := np

	return &out, &op, nil
}

// GetNodePool returns one node pool.
func (m *Mock) GetNodePool(_ context.Context, location, clusterName, name string) (*NodePool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	np, ok := m.nodePools.Get(nodePoolKey(location, clusterName, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "node pool %q not found in cluster %q", name, clusterName)
	}

	out := np

	return &out, nil
}

// ListNodePools returns all node pools in a cluster.
func (m *Mock) ListNodePools(_ context.Context, location, clusterName string) ([]NodePool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := location + "/" + clusterName + "/"
	all := m.nodePools.All()

	out := make([]NodePool, 0, len(all))

	//nolint:gocritic // map values are sized for accuracy.
	for k, np := range all {
		if hasPrefix(k, prefix) {
			out = append(out, np)
		}
	}

	return out, nil
}

// UpdateNodePoolInput captures the subset of UpdateNodePool we honor.
type UpdateNodePoolInput struct {
	NodeVersion string
	MachineType string
	DiskSizeGB  int64
}

// UpdateNodePool applies a partial update.
func (m *Mock) UpdateNodePool(
	_ context.Context, location, clusterName, name string, input UpdateNodePoolInput,
) (*Operation, error) {
	return m.mutateNodePool(location, clusterName, name, "UPGRADE_NODES", func(np *NodePool) {
		if input.NodeVersion != "" {
			np.Version = input.NodeVersion
		}

		if input.MachineType != "" {
			np.MachineType = input.MachineType
		}

		if input.DiskSizeGB > 0 {
			np.DiskSizeGB = input.DiskSizeGB
		}
	})
}

// DeleteNodePool removes a node pool.
func (m *Mock) DeleteNodePool(_ context.Context, location, clusterName, name string) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	npKey := nodePoolKey(location, clusterName, name)
	if !m.nodePools.Has(npKey) {
		return nil, cerrors.Newf(cerrors.NotFound, "node pool %q not found in cluster %q", name, clusterName)
	}

	m.nodePools.Delete(npKey)

	if cluster, ok := m.clusters.Get(clusterKey(location, clusterName)); ok {
		cluster.NodePoolNames = removeString(cluster.NodePoolNames, name)
		m.clusters.Set(clusterKey(location, clusterName), cluster)
	}

	op := m.recordOperation("DELETE_NODE_POOL", location,
		"projects/"+m.opts.ProjectID+"/locations/"+location+"/clusters/"+clusterName+"/nodePools/"+name)

	return &op, nil
}

// SetNodePoolSize resizes a node pool to count nodes.
func (m *Mock) SetNodePoolSize(
	_ context.Context, location, clusterName, name string, count int64,
) (*Operation, error) {
	return m.mutateNodePool(location, clusterName, name, "SET_NODE_POOL_SIZE", func(np *NodePool) {
		np.NodeCount = count
	})
}

// SetNodePoolAutoscaling toggles autoscaling and bounds.
func (m *Mock) SetNodePoolAutoscaling(
	_ context.Context, location, clusterName, name string, on bool, minNodes, maxNodes int64,
) (*Operation, error) {
	return m.mutateNodePool(location, clusterName, name, "UPDATE_CLUSTER", func(np *NodePool) {
		np.AutoscalingOn = on
		np.AutoscalingMin = minNodes
		np.AutoscalingMax = maxNodes
	})
}

// SetNodePoolManagement toggles auto-upgrade and auto-repair.
func (m *Mock) SetNodePoolManagement(
	_ context.Context, location, clusterName, name string, autoUpgrade, autoRepair bool,
) (*Operation, error) {
	return m.mutateNodePool(location, clusterName, name, "SET_NODE_POOL_MANAGEMENT", func(np *NodePool) {
		np.AutoUpgrade = autoUpgrade
		np.AutoRepair = autoRepair
	})
}

// RollbackNodePool flags an in-progress upgrade as rolled back.
func (m *Mock) RollbackNodePool(_ context.Context, location, clusterName, name string) (*Operation, error) {
	return m.mutateNodePool(location, clusterName, name, "UPGRADE_NODES", func(np *NodePool) {
		np.UpgradeRolledBack = true
	})
}

func (m *Mock) mutateNodePool(
	location, clusterName, name, opType string, fn func(*NodePool),
) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := nodePoolKey(location, clusterName, name)

	np, ok := m.nodePools.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "node pool %q not found in cluster %q", name, clusterName)
	}

	fn(&np)
	m.nodePools.Set(key, np)

	op := m.recordOperation(opType, location,
		"projects/"+m.opts.ProjectID+"/locations/"+location+"/clusters/"+clusterName+"/nodePools/"+name)

	return &op, nil
}

// GetOperation returns one previously-recorded operation. The location is
// part of the SDK URL but operation names are globally unique in the mock,
// so the parameter is unused — kept for parity with the SDK signature.
func (m *Mock) GetOperation(_ context.Context, _, name string) (*Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "operation %q not found", name)
	}

	out := op

	return &out, nil
}

// ListOperations returns all operations in a location ("-" for all).
func (m *Mock) ListOperations(_ context.Context, location string) ([]Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.operations.All()
	out := make([]Operation, 0, len(all))

	//nolint:gocritic // map values are sized for accuracy.
	for _, op := range all {
		if location != "" && location != "-" && op.Location != location {
			continue
		}

		out = append(out, op)
	}

	return out, nil
}

// CancelOperation marks a recorded operation as canceled. Real GKE cancels
// long-running ops; the mock's ops are already DONE so this is a no-op for
// state purposes — we still record the request returned an OK envelope. The
// location is part of the SDK URL but operation names are globally unique in
// the mock, so the parameter is unused — kept for parity with the SDK.
func (m *Mock) CancelOperation(_ context.Context, _, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "operation %q not found", name)
	}

	op.Status = "ABORTING"
	m.operations.Set(name, op)

	return nil
}

// helpers

func clusterKey(location, name string) string {
	return location + "/" + name
}

func nodePoolKey(location, clusterName, name string) string {
	return location + "/" + clusterName + "/" + name
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func defaultIfEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

func defaultIfZero(v, fallback int64) int64 {
	if v == 0 {
		return fallback
	}

	return v
}

func copyLabels(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func removeString(items []string, target string) []string {
	out := items[:0]

	for _, s := range items {
		if s != target {
			out = append(out, s)
		}
	}

	return out
}
