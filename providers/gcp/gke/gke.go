// Package gke provides an in-memory mock of GCP Kubernetes Engine (GKE).
//
// It covers the GKE control-plane (Clusters, NodePools, and the long-running
// Operations they emit) plus a live Kubernetes data plane. When a shared
// kubernetes.APIServer is wired in, GetCluster's Endpoint + masterAuth CA point
// at a real in-memory apiserver so `gcloud container clusters get-credentials`
// yields a working kubeconfig. Without one, GetCluster reports a deterministic
// control-plane IP (matching real GKE's bare-IP `endpoint` field) so kubeconfig
// rendering still works syntactically.
package gke

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Default versions reported by clusters/node pools and getServerConfig.
const (
	StubMasterVer   = "1.30.0-gke.0"
	stubNodeVersion = "1.30.0-gke.0"
)

const (
	cpuMetricRunning   = 0.25
	memMetricRunning   = 1024.0 * 1024.0 * 256.0 // 256 MiB
	defaultNodeCount   = 1
	defaultMachineType = "e2-medium"
	defaultDiskSizeGB  = 100

	// defaultServicesCIDR mirrors GKE's default Kubernetes Services IP range.
	defaultServicesCIDR = "34.118.224.0/20"
	// defaultClusterCIDR is the default pod IP range for the cluster.
	defaultClusterCIDR = "10.0.0.0/14"
	// defaultNodeIPv4CIDRSize is the per-node pod CIDR block size (/24).
	defaultNodeIPv4CIDRSize = 24

	// controlPlaneIPFirstOctet anchors synthesized control-plane IPs in a
	// public-looking /8, matching real GKE's public endpoint shape.
	controlPlaneIPFirstOctet = 35
	octetMod                 = 253
	octetShift8              = 8
	octetShift16             = 16
)

// Cluster is the in-memory representation of a GKE cluster. The shape mirrors
// only the fields the SDK round-trip cares about; the handler layer maps
// these to the wire shape google.golang.org/api/container/v1.Cluster expects.
type Cluster struct {
	Name              string
	ID                string
	Location          string
	Description       string
	Network           string
	Subnetwork        string
	InitialNodeCount  int64
	NodeIPv4CIDRSize  int64
	ClusterIPv4CIDR   string
	ServicesIPv4CIDR  string
	ControlPlaneIP    string
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
	MasterVersion     string
	NodeVersion       string
	CreatedAt         time.Time
}

// NodePool is the in-memory representation of a GKE node pool.
type NodePool struct {
	Name                  string
	ClusterName           string
	Location              string
	NodeCount             int64
	MachineType           string
	DiskSizeGB            int64
	OauthScopes           []string
	Version               string
	AutoscalingMin        int64
	AutoscalingMax        int64
	AutoscalingOn         bool
	AutoscalingConfigured bool
	AutoUpgrade           bool
	AutoRepair            bool
	Status                string
	UpgradeRolledBack     bool
	CreatedAt             time.Time
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

	// k8sAPI is the shared in-memory Kubernetes data-plane server. When set,
	// CreateCluster registers a fresh ClusterState with api and Endpoint()
	// returns the URL clients should hit. When nil, Endpoint() returns the
	// Wave 1 sentinel host so existing tests keep working.
	k8sAPI *kubernetes.APIServer
	// k8sUIDs maps "{location}/{name}" → the UID we registered with k8sAPI.
	k8sUIDs map[string]string
}

// New creates a new GKE mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:   memstore.New[Cluster](),
		nodePools:  memstore.New[NodePool](),
		operations: memstore.New[Operation](),
		opts:       opts,
		k8sUIDs:    make(map[string]string),
	}
}

// SetMonitoring wires a Cloud Monitoring backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetK8sAPI wires a shared in-memory Kubernetes data-plane server. After
// this is set, CreateCluster registers a fresh ClusterState with api and
// Endpoint() returns the URL of that state for the given cluster. Pass the
// same *kubernetes.APIServer as gcpserver.Drivers.K8sAPI when constructing
// the SDK-compat server, so kubeconfigs land on the right backend.
func (m *Mock) SetK8sAPI(api *kubernetes.APIServer) {
	m.mu.Lock()
	m.k8sAPI = api
	m.mu.Unlock()
}

// Endpoint returns the data-plane URL clients should target for a given
// cluster. If a Kubernetes APIServer is wired and the cluster has a
// registered UID, returns "<base>/k8s/<uid>" — the in-memory data plane.
// Otherwise returns the cluster's synthesized control-plane IP (a bare IPv4
// address, matching real GKE's `endpoint` field) so a kubeconfig renders to a
// well-formed, non-sentinel host.
func (m *Mock) Endpoint(location, name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := clusterKey(location, name)

	if m.k8sAPI != nil {
		if uid, ok := m.k8sUIDs[key]; ok {
			if base := m.k8sAPI.BaseURL(); base != "" {
				return base + "/k8s/" + uid
			}
		}
	}

	if c, ok := m.clusters.Get(key); ok && c.ControlPlaneIP != "" {
		return c.ControlPlaneIP
	}

	return controlPlaneIP(location, name)
}

// controlPlaneIP synthesizes a deterministic, public-looking IPv4 address for a
// cluster's control-plane endpoint. Real GKE returns such an IP in the Cluster
// `endpoint` field; the emulator has no real control-plane host without a wired
// data plane, so a stable per-cluster IP stands in.
func controlPlaneIP(location, name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(location + "/" + name))
	sum := h.Sum32()

	return fmt.Sprintf("%d.%d.%d.%d",
		controlPlaneIPFirstOctet,
		(sum>>octetShift16)%octetMod+1,
		(sum>>octetShift8)%octetMod+1,
		sum%octetMod+1,
	)
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
	NodeConfig        *NodeConfigSpec
	NodePools         []NodePoolSpec
}

// NodeConfigSpec captures the cluster-level nodeConfig that seeds the
// auto-created default node pool when no explicit node pools are given.
type NodeConfigSpec struct {
	MachineType string
	DiskSizeGB  int64
	OauthScopes []string
}

// NodePoolManagement captures the node auto-management flags a create request
// may set. A nil pointer means the request omitted the management block, so the
// mock applies GKE's true/true defaults.
type NodePoolManagement struct {
	AutoUpgrade bool
	AutoRepair  bool
}

// NodePoolSpec captures the node-pool fields we keep when bootstrapping a
// cluster from a CreateClusterRequest.
type NodePoolSpec struct {
	Name             string
	InitialNodeCount int64
	MachineType      string
	DiskSizeGB       int64
	OauthScopes      []string
	Version          string
	AutoscalingMin   int64
	AutoscalingMax   int64
	AutoscalingOn    bool
	Management       *NodePoolManagement
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
		ID:                idgen.SyntheticGUID(key),
		Location:          input.Location,
		Description:       input.Description,
		Network:           defaultIfEmpty(input.Network, "default"),
		Subnetwork:        defaultIfEmpty(input.Subnetwork, "default"),
		InitialNodeCount:  input.InitialNodeCount,
		NodeIPv4CIDRSize:  defaultNodeIPv4CIDRSize,
		ClusterIPv4CIDR:   defaultClusterCIDR,
		ServicesIPv4CIDR:  defaultServicesCIDR,
		ControlPlaneIP:    controlPlaneIP(input.Location, input.Name),
		LoggingService:    defaultIfEmpty(input.LoggingService, "logging.googleapis.com/kubernetes"),
		MonitoringService: defaultIfEmpty(input.MonitoringService, "monitoring.googleapis.com/kubernetes"),
		ResourceLabels:    copyLabels(input.ResourceLabels),
		Status:            "RUNNING",
		CreatedAt:         m.opts.Clock.Now().UTC(),
	}

	// Bootstrap default node pool when none specified — matches real GKE. The
	// cluster-level nodeConfig (when present) configures that pool; absent
	// fields fall back to defaults via nodePoolFromSpec.
	pools := input.NodePools
	if len(pools) == 0 {
		count := input.InitialNodeCount
		if count == 0 {
			count = defaultNodeCount
		}

		spec := NodePoolSpec{
			Name:             "default-pool",
			InitialNodeCount: count,
			Version:          stubNodeVersion,
		}

		if nc := input.NodeConfig; nc != nil {
			spec.MachineType = nc.MachineType
			spec.DiskSizeGB = nc.DiskSizeGB
			spec.OauthScopes = nc.OauthScopes
		}

		pools = []NodePoolSpec{spec}
	}

	for i := range pools {
		np := nodePoolFromSpec(&pools[i], input.Name, input.Location, m.opts.Clock.Now().UTC())
		m.nodePools.Set(nodePoolKey(input.Location, input.Name, np.Name), np)
		cluster.NodePoolNames = append(cluster.NodePoolNames, np.Name)
	}

	// Wave 2: if a Kubernetes data-plane server is wired, register a fresh
	// ClusterState and remember the UID so Endpoint() can return a working
	// URL.
	if m.k8sAPI != nil {
		uid, _ := m.k8sAPI.RegisterCluster()
		m.k8sUIDs[key] = uid
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

	// GKE defaults auto-upgrade/auto-repair to true; an explicit management
	// block (even one setting them false) must survive the round-trip.
	autoUpgrade, autoRepair := true, true
	if spec.Management != nil {
		autoUpgrade = spec.Management.AutoUpgrade
		autoRepair = spec.Management.AutoRepair
	}

	return NodePool{
		Name:           spec.Name,
		ClusterName:    clusterName,
		Location:       location,
		NodeCount:      count,
		MachineType:    defaultIfEmpty(spec.MachineType, defaultMachineType),
		DiskSizeGB:     defaultIfZero(spec.DiskSizeGB, defaultDiskSizeGB),
		OauthScopes:    spec.OauthScopes,
		Version:        defaultIfEmpty(spec.Version, stubNodeVersion),
		AutoscalingMin: spec.AutoscalingMin,
		AutoscalingMax: spec.AutoscalingMax,
		AutoscalingOn:  spec.AutoscalingOn,
		AutoUpgrade:    autoUpgrade,
		AutoRepair:     autoRepair,
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

		if input.MasterVersion != "" {
			c.MasterVersion = input.MasterVersion
		}

		if input.NodeVersion != "" {
			c.NodeVersion = input.NodeVersion
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

	// Wave 2: tear down the cluster's Kubernetes data-plane state alongside
	// the control-plane record.
	if uid, ok := m.k8sUIDs[key]; ok && m.k8sAPI != nil {
		m.k8sAPI.DeregisterCluster(uid)
		delete(m.k8sUIDs, key)
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
		np.AutoscalingConfigured = true
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

// HasOperation reports whether an operation with the given name was recorded by
// this GKE mock. The handler uses it to claim only its own operation polls,
// letting foreign location operations (artifactregistry, eventarc, …) fall
// through to the shared LRO handler.
func (m *Mock) HasOperation(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.operations.Has(name)
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
