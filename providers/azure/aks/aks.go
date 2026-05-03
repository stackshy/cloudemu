// Package aks provides an in-memory mock of Microsoft.ContainerService
// (Azure Kubernetes Service) — control-plane only.
//
// Wave 1 scope: ManagedClusters + AgentPools + MaintenanceConfigurations
// CRUD and credential listing. The Kubernetes data-plane API (Deployments,
// Pods, Services, …) is intentionally NOT modeled here; that is deferred to
// Wave 2. The mock returns a stub kubeconfig pointing at a sentinel host so
// callers can detect that the data plane is not implemented.
package aks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

const (
	armProvider          = "Microsoft.ContainerService"
	resourceManagedClstr = "managedClusters"
	defaultK8sVersion    = "1.29.0"
	defaultNodeCount     = 3
	defaultVMSize        = "Standard_DS2_v2"
	defaultOSDiskGB      = 128
	cpuMetricRunning     = 0.35
	memMetricRunning     = 0.50
	podMetricRunning     = 12.0
)

// ManagedCluster is the in-memory representation of an AKS cluster.
type ManagedCluster struct {
	Name              string
	ResourceGroup     string
	Location          string
	KubernetesVersion string
	DNSPrefix         string
	FQDN              string
	NodeResourceGroup string
	ProvisioningState string
	PowerState        string
	Tags              map[string]string
	AgentPoolNames    []string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// AgentPool is a node pool attached to a managed cluster.
type AgentPool struct {
	Name              string
	ClusterName       string
	ResourceGroup     string
	Count             int32
	VMSize            string
	OSDiskSizeGB      int32
	OSType            string
	Mode              string
	OrchestratorVer   string
	ProvisioningState string
	NodeLabels        map[string]string
	NodeTaints        []string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// MaintenanceConfig is a maintenance window attached to a managed cluster.
type MaintenanceConfig struct {
	Name          string
	ClusterName   string
	ResourceGroup string
	Properties    map[string]any

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Mock is the in-memory AKS implementation.
type Mock struct {
	mu sync.RWMutex

	// clusters key = "{rg}/{name}"
	clusters *memstore.Store[ManagedCluster]
	// pools key = "{rg}/{cluster}/{pool}"
	pools *memstore.Store[AgentPool]
	// maintenance key = "{rg}/{cluster}/{config}"
	maintenance *memstore.Store[MaintenanceConfig]

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new AKS mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:    memstore.New[ManagedCluster](),
		pools:       memstore.New[AgentPool](),
		maintenance: memstore.New[MaintenanceConfig](),
		opts:        opts,
	}
}

// SetMonitoring wires an Azure-Monitor-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// clusterKey is the storage key for a managed cluster: "{rg}/{name}".
func clusterKey(rg, name string) string {
	return rg + "/" + name
}

// poolKey is the storage key for an agent pool: "{rg}/{cluster}/{pool}".
func poolKey(rg, cluster, pool string) string {
	return rg + "/" + cluster + "/" + pool
}

// maintenanceKey is the storage key for a maintenance config:
// "{rg}/{cluster}/{name}".
func maintenanceKey(rg, cluster, name string) string {
	return rg + "/" + cluster + "/" + name
}

// ClusterResourceID constructs the canonical ARM resource ID for a managed
// cluster.
func ClusterResourceID(subscription, rg, name string) string {
	return idgen.AzureID(subscription, rg, armProvider, resourceManagedClstr, name)
}

// AgentPoolResourceID constructs the canonical ARM resource ID for an
// agent pool nested under a managed cluster.
func AgentPoolResourceID(subscription, rg, cluster, pool string) string {
	return ClusterResourceID(subscription, rg, cluster) + "/agentPools/" + pool
}

// MaintenanceConfigResourceID constructs the canonical ARM resource ID for a
// maintenance configuration nested under a managed cluster.
func MaintenanceConfigResourceID(subscription, rg, cluster, name string) string {
	return ClusterResourceID(subscription, rg, cluster) + "/maintenanceConfigurations/" + name
}

func copyTags(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func copyLabels(src map[string]string) map[string]string {
	return copyTags(src)
}

func copyTaints(src []string) []string {
	if src == nil {
		return nil
	}

	out := make([]string, len(src))
	copy(out, src)

	return out
}

// emitClusterMetrics emits Microsoft.ContainerService/managedClusters metrics.
func (m *Mock) emitClusterMetrics(subscription, rg, cluster string, nodeCount, podCount float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	resourceID := ClusterResourceID(subscription, rg, cluster)
	dims := map[string]string{"resourceId": resourceID}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: "Microsoft.ContainerService/managedClusters", MetricName: "cpu_usage",
			Value: cpuMetricRunning * 100, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.ContainerService/managedClusters", MetricName: "memory_usage",
			Value: memMetricRunning * 100, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.ContainerService/managedClusters", MetricName: "node_count",
			Value: nodeCount, Unit: "Count", Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.ContainerService/managedClusters", MetricName: "pod_count",
			Value: podCount, Unit: "Count", Dimensions: dims, Timestamp: now},
	})
}

// ClusterInput captures the mutable fields of a CreateOrUpdate call.
type ClusterInput struct {
	Subscription      string
	ResourceGroup     string
	Name              string
	Location          string
	KubernetesVersion string
	DNSPrefix         string
	NodeResourceGroup string
	Tags              map[string]string
	// AgentPools may be nil for an empty cluster; otherwise these are the
	// pools shipped inline at create time (system pool typically).
	AgentPools []AgentPoolInput
}

// AgentPoolInput captures the mutable fields of an AgentPool CreateOrUpdate.
type AgentPoolInput struct {
	Name            string
	Count           int32
	VMSize          string
	OSDiskSizeGB    int32
	OSType          string
	Mode            string
	OrchestratorVer string
	NodeLabels      map[string]string
	NodeTaints      []string
}

// CreateOrUpdateCluster creates a new managed cluster or updates an existing
// one. The semantics mirror Azure ARM PUT: full replace, idempotent on no-op.
//
//nolint:gocritic // input mirrors the public API surface; pointer would invite caller mutation.
func (m *Mock) CreateOrUpdateCluster(_ context.Context, input ClusterInput) (*ManagedCluster, error) {
	if input.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "cluster name is required")
	}

	if input.ResourceGroup == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "resource group is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	key := clusterKey(input.ResourceGroup, input.Name)

	cluster, existing := m.clusters.Get(key)
	if !existing {
		cluster = ManagedCluster{
			Name:          input.Name,
			ResourceGroup: input.ResourceGroup,
			CreatedAt:     now,
		}
	}

	cluster.Location = input.Location
	cluster.KubernetesVersion = defaultIfEmpty(input.KubernetesVersion, defaultK8sVersion)
	cluster.DNSPrefix = defaultIfEmpty(input.DNSPrefix, input.Name+"-dns")
	cluster.NodeResourceGroup = defaultIfEmpty(input.NodeResourceGroup, "MC_"+input.ResourceGroup+"_"+input.Name+"_"+input.Location)
	cluster.FQDN = cluster.DNSPrefix + ".hcp." + defaultIfEmpty(input.Location, "eastus") + ".azmk8s.io"
	cluster.ProvisioningState = "Succeeded"
	cluster.PowerState = "Running"
	cluster.Tags = copyTags(input.Tags)
	cluster.UpdatedAt = now

	// Inline pools — replace what we have for this cluster.
	cluster.AgentPoolNames = m.replaceInlinePools(input, now)

	m.clusters.Set(key, cluster)

	m.emitClusterMetrics(input.Subscription, input.ResourceGroup, input.Name,
		float64(totalNodeCount(input.AgentPools)), podMetricRunning)

	out := cluster

	return &out, nil
}

// replaceInlinePools wipes any pre-existing pools for the cluster and re-adds
// each input pool. Caller must hold m.mu (write).
//
//nolint:gocritic // input is a value-type mirror of the public CreateOrUpdate body.
func (m *Mock) replaceInlinePools(input ClusterInput, now time.Time) []string {
	// Drop existing pools for this cluster.
	prefix := input.ResourceGroup + "/" + input.Name + "/"

	for _, k := range m.pools.Keys() {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			m.pools.Delete(k)
		}
	}

	names := make([]string, 0, len(input.AgentPools))

	//nolint:gocritic // pool is a value mirror of the SDK input; copy is intentional.
	for _, pool := range input.AgentPools {
		ap := buildAgentPool(input.ResourceGroup, input.Name, pool, now)
		m.pools.Set(poolKey(input.ResourceGroup, input.Name, pool.Name), ap)
		names = append(names, pool.Name)
	}

	return names
}

//nolint:gocritic // in is a value mirror of the SDK AgentPool body; pointer would invite caller mutation.
func buildAgentPool(rg, cluster string, in AgentPoolInput, now time.Time) AgentPool {
	count := in.Count
	if count <= 0 {
		count = defaultNodeCount
	}

	disk := in.OSDiskSizeGB
	if disk <= 0 {
		disk = defaultOSDiskGB
	}

	return AgentPool{
		Name:              in.Name,
		ClusterName:       cluster,
		ResourceGroup:     rg,
		Count:             count,
		VMSize:            defaultIfEmpty(in.VMSize, defaultVMSize),
		OSDiskSizeGB:      disk,
		OSType:            defaultIfEmpty(in.OSType, "Linux"),
		Mode:              defaultIfEmpty(in.Mode, "User"),
		OrchestratorVer:   defaultIfEmpty(in.OrchestratorVer, defaultK8sVersion),
		ProvisioningState: "Succeeded",
		NodeLabels:        copyLabels(in.NodeLabels),
		NodeTaints:        copyTaints(in.NodeTaints),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func totalNodeCount(pools []AgentPoolInput) int32 {
	var total int32

	for i := range pools {
		c := pools[i].Count
		if c <= 0 {
			c = defaultNodeCount
		}

		total += c
	}

	if total == 0 {
		total = defaultNodeCount
	}

	return total
}

// GetCluster returns a managed cluster by name within a resource group.
func (m *Mock) GetCluster(_ context.Context, rg, name string) (*ManagedCluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cluster, ok := m.clusters.Get(clusterKey(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", name, rg)
	}

	out := cluster

	return &out, nil
}

// UpdateClusterTags replaces the tags on a managed cluster (PATCH semantics).
func (m *Mock) UpdateClusterTags(_ context.Context, rg, name string, tags map[string]string) (*ManagedCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	cluster, ok := m.clusters.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", name, rg)
	}

	cluster.Tags = copyTags(tags)
	cluster.UpdatedAt = m.opts.Clock.Now().UTC()
	m.clusters.Set(key, cluster)

	out := cluster

	return &out, nil
}

// DeleteCluster removes a managed cluster and all its sub-resources.
func (m *Mock) DeleteCluster(_ context.Context, rg, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)
	if !m.clusters.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", name, rg)
	}

	prefix := rg + "/" + name + "/"
	for _, k := range m.pools.Keys() {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			m.pools.Delete(k)
		}
	}

	for _, k := range m.maintenance.Keys() {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			m.maintenance.Delete(k)
		}
	}

	m.clusters.Delete(key)

	return nil
}

// ListClustersByResourceGroup returns all clusters in a resource group.
func (m *Mock) ListClustersByResourceGroup(_ context.Context, rg string) ([]ManagedCluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.clusters.Filter(func(_ string, c ManagedCluster) bool {
		return c.ResourceGroup == rg
	})

	out := make([]ManagedCluster, 0, len(all))
	//nolint:gocritic // map values are large structs; copy is unavoidable when materializing the result slice.
	for _, c := range all {
		out = append(out, c)
	}

	return out, nil
}

// ListClusters returns all managed clusters across all resource groups.
func (m *Mock) ListClusters(_ context.Context) ([]ManagedCluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.clusters.All()
	out := make([]ManagedCluster, 0, len(all))

	//nolint:gocritic // map values are large structs; copy is unavoidable when materializing the result slice.
	for _, c := range all {
		out = append(out, c)
	}

	return out, nil
}

// RotateClusterCertificates is a stub that simply marks the cluster updated.
// Real Azure rotates control-plane certs asynchronously; the mock surface is
// sufficient for the SDK round-trip.
func (m *Mock) RotateClusterCertificates(_ context.Context, rg, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	cluster, ok := m.clusters.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", name, rg)
	}

	cluster.UpdatedAt = m.opts.Clock.Now().UTC()
	m.clusters.Set(key, cluster)

	return nil
}

// CreateOrUpdateAgentPool creates or replaces an agent pool on a cluster.
//
//nolint:gocritic // in mirrors the public AgentPoolInput surface; pointer would invite caller mutation.
func (m *Mock) CreateOrUpdateAgentPool(
	_ context.Context, rg, cluster string, in AgentPoolInput,
) (*AgentPool, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "agent pool name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cKey := clusterKey(rg, cluster)
	if !m.clusters.Has(cKey) {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", cluster, rg)
	}

	now := m.opts.Clock.Now().UTC()
	pool := buildAgentPool(rg, cluster, in, now)

	if existing, ok := m.pools.Get(poolKey(rg, cluster, in.Name)); ok {
		pool.CreatedAt = existing.CreatedAt
	}

	m.pools.Set(poolKey(rg, cluster, in.Name), pool)
	m.attachPoolName(cKey, in.Name)

	out := pool

	return &out, nil
}

// attachPoolName appends pool to the cluster's AgentPoolNames if not present.
// Caller must hold m.mu (write).
func (m *Mock) attachPoolName(cKey, pool string) {
	c, ok := m.clusters.Get(cKey)
	if !ok {
		return
	}

	for _, existing := range c.AgentPoolNames {
		if existing == pool {
			return
		}
	}

	c.AgentPoolNames = append(c.AgentPoolNames, pool)
	m.clusters.Set(cKey, c)
}

// GetAgentPool returns an agent pool by name.
func (m *Mock) GetAgentPool(_ context.Context, rg, cluster, pool string) (*AgentPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ap, ok := m.pools.Get(poolKey(rg, cluster, pool))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "agent pool %q not found on cluster %q", pool, cluster)
	}

	out := ap

	return &out, nil
}

// DeleteAgentPool removes an agent pool.
func (m *Mock) DeleteAgentPool(_ context.Context, rg, cluster, pool string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := poolKey(rg, cluster, pool)
	if !m.pools.Delete(key) {
		return cerrors.Newf(cerrors.NotFound, "agent pool %q not found on cluster %q", pool, cluster)
	}

	cKey := clusterKey(rg, cluster)
	if c, ok := m.clusters.Get(cKey); ok {
		c.AgentPoolNames = removeString(c.AgentPoolNames, pool)
		m.clusters.Set(cKey, c)
	}

	return nil
}

// ListAgentPools returns all pools attached to a cluster.
//
//nolint:dupl // sub-resource lists are intentionally typed; sharing via generics adds noise.
func (m *Mock) ListAgentPools(_ context.Context, rg, cluster string) ([]AgentPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", cluster, rg)
	}

	all := m.pools.Filter(func(_ string, p AgentPool) bool {
		return p.ResourceGroup == rg && p.ClusterName == cluster
	})

	out := make([]AgentPool, 0, len(all))
	//nolint:gocritic // map values are large structs; copy is unavoidable when materializing the result slice.
	for _, p := range all {
		out = append(out, p)
	}

	return out, nil
}

// CreateOrUpdateMaintenanceConfig creates or replaces a maintenance config.
func (m *Mock) CreateOrUpdateMaintenanceConfig(
	_ context.Context, rg, cluster, name string, props map[string]any,
) (*MaintenanceConfig, error) {
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "maintenance config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", cluster, rg)
	}

	now := m.opts.Clock.Now().UTC()

	mc := MaintenanceConfig{
		Name:          name,
		ClusterName:   cluster,
		ResourceGroup: rg,
		Properties:    props,
		UpdatedAt:     now,
	}

	if existing, ok := m.maintenance.Get(maintenanceKey(rg, cluster, name)); ok {
		mc.CreatedAt = existing.CreatedAt
	} else {
		mc.CreatedAt = now
	}

	m.maintenance.Set(maintenanceKey(rg, cluster, name), mc)

	out := mc

	return &out, nil
}

// GetMaintenanceConfig returns a maintenance config by name.
func (m *Mock) GetMaintenanceConfig(_ context.Context, rg, cluster, name string) (*MaintenanceConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mc, ok := m.maintenance.Get(maintenanceKey(rg, cluster, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "maintenance config %q not found on cluster %q", name, cluster)
	}

	out := mc

	return &out, nil
}

// DeleteMaintenanceConfig removes a maintenance config.
func (m *Mock) DeleteMaintenanceConfig(_ context.Context, rg, cluster, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.maintenance.Delete(maintenanceKey(rg, cluster, name)) {
		return cerrors.Newf(cerrors.NotFound, "maintenance config %q not found on cluster %q", name, cluster)
	}

	return nil
}

// ListMaintenanceConfigs returns all maintenance configs on a cluster.
//
//nolint:dupl // sub-resource lists are intentionally typed; sharing via generics adds noise.
func (m *Mock) ListMaintenanceConfigs(_ context.Context, rg, cluster string) ([]MaintenanceConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterKey(rg, cluster)) {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", cluster, rg)
	}

	all := m.maintenance.Filter(func(_ string, mc MaintenanceConfig) bool {
		return mc.ResourceGroup == rg && mc.ClusterName == cluster
	})

	out := make([]MaintenanceConfig, 0, len(all))

	for _, mc := range all {
		out = append(out, mc)
	}

	return out, nil
}

// StubKubeconfig returns a stub kubeconfig blob pointing at the
// "data-plane not implemented" sentinel host. Wave 2 will replace this with a
// real cloudemu-served Kubernetes API endpoint.
//
// The output is deterministic: callers in tests can match on the sentinel
// host to confirm the data plane is intentionally unimplemented. The receiver
// is kept (unused) so the method satisfies the server-side Backend
// interface alongside the rest of the AKS surface.
func (*Mock) StubKubeconfig(rg, name string) []byte {
	return fmt.Appendf(nil, `apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: https://AKS-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local
contexts:
- name: %s
  context:
    cluster: %s
    user: clusterUser_%s_%s
current-context: %s
users:
- name: clusterUser_%s_%s
  user:
    token: cloudemu-stub-token
`, name, name, name, rg, name, name, rg, name)
}

func defaultIfEmpty(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func removeString(slice []string, target string) []string {
	for i, v := range slice {
		if v == target {
			return append(slice[:i], slice[i+1:]...)
		}
	}

	return slice
}
