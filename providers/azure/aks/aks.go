// Package aks provides an in-memory mock of Microsoft.ContainerService
// (Azure Kubernetes Service) — control-plane only.
//
// Scope: ManagedClusters + AgentPools + MaintenanceConfigurations CRUD and
// credential listing, plus a live Kubernetes data plane. When a shared
// kubernetes.APIServer is wired in, ListClusterAdminCredentials returns a
// kubeconfig pointing at a real in-memory apiserver (with its CA) so client-go
// and kubectl operate end-to-end; without one, the kubeconfig points at a
// sentinel host so callers can detect the data plane is unwired.
package aks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/k8spki"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	armProvider          = "Microsoft.ContainerService"
	resourceManagedClstr = "managedClusters"
	defaultK8sVersion    = "1.29.0"
	defaultNodeCount     = 3
	defaultVMSize        = "Standard_DS2_v2"
	defaultOSDiskGB      = 128
	defaultMaxPods       = 110
	defaultOSDiskType    = "Managed"
	defaultPoolType      = "VirtualMachineScaleSets"
	defaultNodeImage     = "AKSUbuntu-2204gen2containerd-202401.09.0"
	poolPowerRunning     = "Running"
	poolPowerStopped     = "Stopped"
	// clusterPowerRunning / clusterPowerStopped are the ARM powerState.code
	// values a managed cluster reports; provisioningStateSucceeded is the
	// terminal provisioningState every mutating op settles to synchronously.
	clusterPowerRunning        = "Running"
	clusterPowerStopped        = "Stopped"
	provisioningStateSucceeded = "Succeeded"
	// agentPoolModeSystem is the AgentPool.Mode value AKS requires at least
	// one pool to carry at all times; DeleteAgentPool enforces that rule.
	agentPoolModeSystem = "System"
	// Network-profile defaults the real AKS service synthesizes when a create
	// omits properties.networkProfile entirely.
	defaultNetworkPlugin   = "kubenet"
	defaultServiceCidr     = "10.0.0.0/16"
	defaultDNSServiceIP    = "10.0.0.10"
	defaultPodCidr         = "10.244.0.0/16"
	defaultLoadBalancerSKU = "standard"
	defaultOutboundType    = "loadBalancer"
	emulatorTenantID       = "11111111-1111-1111-1111-111111111111"
	cpuMetricRunning       = 0.35
	memMetricRunning       = 0.50
	podMetricRunning       = 12.0
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
	// Tier is the cluster SKU tier (Free / Standard / Premium) — the uptime-SLA
	// cost input a discoverer reads from `sku.tier`.
	Tier           string
	Tags           map[string]string
	AgentPoolNames []string

	// NetworkProfile echoes properties.networkProfile. When a create omits it,
	// the mock stores the standard AKS defaults (see defaultNetworkProfile);
	// otherwise it stores exactly the submitted values so a read round-trips.
	NetworkProfile NetworkProfile

	// EnableRBAC mirrors properties.enableRBAC; defaults to true, the AKS
	// default when a create omits it.
	EnableRBAC bool
	// Identity echoes the managed-identity block. IdentityType is
	// "SystemAssigned" / "UserAssigned" / "None"/""; PrincipalID and TenantID
	// are populated for a system-assigned identity.
	IdentityType string
	PrincipalID  string
	TenantID     string
	// UserAssignedIdentities echoes identity.userAssignedIdentities: the ARM
	// resource ID of each assigned identity mapped to its synthesized
	// principal/client pair. Populated only for a UserAssigned identity.
	UserAssignedIdentities map[string]UserAssignedIdentity

	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserAssignedIdentity is the synthesized principal/client pair echoed for a
// single user-assigned managed identity.
type UserAssignedIdentity struct {
	PrincipalID string
	ClientID    string
}

// NetworkProfile captures the cluster network configuration echoed back on read.
// A pointer to it on ClusterInput distinguishes "caller omitted networkProfile"
// (nil → synthesize defaults) from "caller submitted it" (echo verbatim). Only
// the sub-keys the emulator models live here; any sub-key a caller sets but the
// emulator does not model still round-trips through the property overlay.
type NetworkProfile struct {
	NetworkPlugin   string
	NetworkPolicy   string
	ServiceCidr     string
	DNSServiceIP    string
	PodCidr         string
	LoadBalancerSKU string
	OutboundType    string
}

// defaultNetworkProfile returns the network-profile values the real AKS service
// synthesizes when a create omits properties.networkProfile.
func defaultNetworkProfile() NetworkProfile {
	return NetworkProfile{
		NetworkPlugin:   defaultNetworkPlugin,
		ServiceCidr:     defaultServiceCidr,
		DNSServiceIP:    defaultDNSServiceIP,
		PodCidr:         defaultPodCidr,
		LoadBalancerSKU: defaultLoadBalancerSKU,
		OutboundType:    defaultOutboundType,
	}
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
	// ScaleSetPriority is Regular or Spot — the Spot marker a discoverer reads
	// for Spot node-pool pricing.
	ScaleSetPriority string
	NodeLabels       map[string]string
	NodeTaints       []string
	// MaxPods, OSDiskType, Type, PowerState and NodeImageVersion are the
	// computed pool fields the real API always returns; the emulator synthesizes
	// standard defaults when a create omits them.
	MaxPods          int32
	OSDiskType       string
	Type             string
	PowerState       string
	NodeImageVersion string
	// Advanced pool fields Terraform's default_node_pool commonly submits. They
	// are echoed exactly as submitted (nil/empty when omitted) so a read
	// round-trips; the emulator synthesizes no defaults for them.
	AvailabilityZones  []string
	EnableAutoScaling  *bool
	MinCount           *int32
	MaxCount           *int32
	VnetSubnetID       string
	OSSKU              string
	EnableNodePublicIP *bool
	// Additional default_node_pool fields Terraform commonly submits inline.
	// Echoed verbatim so an inline submission round-trips the same as a
	// standalone pool; the emulator synthesizes no defaults for them.
	UpgradeSettings        *AgentPoolUpgradeSettings
	Tags                   map[string]string
	EnableFIPS             *bool
	SpotMaxPrice           *float32
	ScaleSetEvictionPolicy string
	NodePublicIPPrefixID   string
	KubeletDiskType        string
	KubeletConfig          map[string]any
	LinuxOSConfig          map[string]any

	CreatedAt time.Time
	UpdatedAt time.Time
}

// AgentPoolUpgradeSettings mirrors the subset of
// armcontainerservice.AgentPoolUpgradeSettings the emulator round-trips.
type AgentPoolUpgradeSettings struct {
	MaxSurge                  string
	DrainTimeoutInMinutes     *int32
	NodeSoakDurationInMinutes *int32
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

	// k8sAPI is the shared in-memory Kubernetes data-plane server. When set,
	// CreateOrUpdateCluster registers a fresh ClusterState with api and
	// Kubeconfig returns a kubeconfig pointing at it. When nil, the Wave 1
	// behavior is preserved: kubeconfigs point at the *-DATAPLANE-NOT-IMPLEMENTED
	// sentinel.
	k8sAPI *kubernetes.APIServer
	// k8sUIDs maps "{rg}/{name}" → the UID we registered with k8sAPI.
	k8sUIDs map[string]string
}

// New creates a new AKS mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:    memstore.New[ManagedCluster](),
		pools:       memstore.New[AgentPool](),
		maintenance: memstore.New[MaintenanceConfig](),
		opts:        opts,
		k8sUIDs:     make(map[string]string),
	}
}

// SetMonitoring wires an Azure-Monitor-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetK8sAPI wires a shared in-memory Kubernetes data-plane server. After this
// is set, CreateOrUpdateCluster registers a fresh ClusterState with api and
// Kubeconfig returns a kubeconfig YAML that points at it. Pass the same
// *kubernetes.APIServer as azureserver.Drivers.K8sAPI when constructing the
// SDK-compat server, so kubeconfigs land on the right backend.
func (m *Mock) SetK8sAPI(api *kubernetes.APIServer) {
	m.mu.Lock()
	m.k8sAPI = api
	m.mu.Unlock()
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
	// Tier is the cluster SKU tier (Free / Standard / Premium); defaults to Free.
	Tier string
	Tags map[string]string
	// IdentityPresent is true when the PUT body carried an identity block. When
	// false on an update, the stored identity is preserved untouched.
	IdentityPresent bool
	// IdentityType echoes the managed-identity block: "SystemAssigned",
	// "UserAssigned", "None" or "".
	IdentityType string
	// UserAssignedIdentityIDs are the ARM resource IDs submitted under
	// identity.userAssignedIdentities; the mock synthesizes a principal/client
	// pair for each. Only meaningful when IdentityType includes "UserAssigned".
	UserAssignedIdentityIDs []string
	// EnableRBAC mirrors properties.enableRBAC. Nil means "not submitted", which
	// the mock resolves to the AKS default (true).
	EnableRBAC *bool
	// NetworkProfile mirrors properties.networkProfile. Nil means "not
	// submitted", which the mock resolves to the AKS defaults; a non-nil value
	// is echoed verbatim so a read round-trips the submitted configuration.
	NetworkProfile *NetworkProfile
	// AgentPools may be nil for an empty cluster; otherwise these are the
	// pools shipped inline at create time (system pool typically).
	AgentPools []AgentPoolInput
}

// AgentPoolInput captures the mutable fields of an AgentPool CreateOrUpdate.
type AgentPoolInput struct {
	Name string
	// Count is a pointer so an explicit count of 0 (scale-to-zero on a user
	// pool) is distinguishable from an omitted count. Nil means "not submitted"
	// and the mock applies the standard default node count.
	Count            *int32
	VMSize           string
	OSDiskSizeGB     int32
	OSType           string
	Mode             string
	OrchestratorVer  string
	ScaleSetPriority string
	NodeLabels       map[string]string
	NodeTaints       []string
	// MaxPods is the submitted max-pods-per-node; 0 means "not submitted" and
	// the mock applies the standard default.
	MaxPods int32
	// OSDiskType (Managed / Ephemeral) and Type (VirtualMachineScaleSets /
	// AvailabilitySet) round-trip when submitted; the mock applies the standard
	// default only when the field is empty.
	OSDiskType string
	Type       string
	// Advanced pool fields (autoscaler bounds, availability zones, subnet, OS
	// SKU, public-IP flag) echoed verbatim; nil/empty when omitted.
	AvailabilityZones  []string
	EnableAutoScaling  *bool
	MinCount           *int32
	MaxCount           *int32
	VnetSubnetID       string
	OSSKU              string
	EnableNodePublicIP *bool
	// Additional default_node_pool fields echoed verbatim; nil/empty when
	// omitted so a read round-trips exactly what was submitted.
	UpgradeSettings        *AgentPoolUpgradeSettings
	Tags                   map[string]string
	EnableFIPS             *bool
	SpotMaxPrice           *float32
	ScaleSetEvictionPolicy string
	NodePublicIPPrefixID   string
	KubeletDiskType        string
	KubeletConfig          map[string]any
	LinuxOSConfig          map[string]any
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

	resolveClusterFields(&cluster, input, existing)
	cluster.UpdatedAt = now

	// Reconcile inline pools by NAME — never wipe. A PUT that omits
	// agentPoolProfiles leaves every existing pool (including standalone-API
	// pools) untouched; a PUT that includes them upserts those pools.
	cluster.AgentPoolNames = m.reconcileInlinePools(input, cluster.KubernetesVersion, now)

	// Wave 2: if a Kubernetes data-plane server is wired and this is a fresh
	// cluster, register a ClusterState and remember the UID so Kubeconfig can
	// return a working URL. CreateOrUpdate may be called more than once for
	// the same cluster name; only register on the first sighting.
	if m.k8sAPI != nil {
		if _, ok := m.k8sUIDs[key]; !ok {
			uid, _ := m.k8sAPI.RegisterCluster()
			m.k8sUIDs[key] = uid
		}
	}

	m.clusters.Set(key, cluster)

	m.emitClusterMetrics(input.Subscription, input.ResourceGroup, input.Name,
		float64(totalNodeCount(input.AgentPools)), podMetricRunning)

	out := cluster

	return &out, nil
}

// resolveClusterFields merges the submitted input onto cluster. On a create,
// unset fields resolve to the AKS defaults; on an update, a field the request
// omits is preserved from the stored value — so a tags-only or version-only PUT
// never resets networkProfile, identity, dnsPrefix, or the node pools.
//
//nolint:gocritic // input is a value-type mirror of the public CreateOrUpdate body.
func resolveClusterFields(cluster *ManagedCluster, input ClusterInput, existing bool) {
	cluster.Location = mergeStr(cluster.Location, input.Location, "", existing)
	cluster.ProvisioningState = provisioningStateSucceeded
	// A create starts Running; an update leaves whatever power state Start/Stop
	// last set (real AKS keeps a stopped cluster stopped across most PUTs).
	if !existing {
		cluster.PowerState = clusterPowerRunning
	}

	cluster.Tier = mergeStr(cluster.Tier, input.Tier, "Free", existing)
	cluster.KubernetesVersion = mergeStr(cluster.KubernetesVersion, input.KubernetesVersion, defaultK8sVersion, existing)
	cluster.DNSPrefix = mergeStr(cluster.DNSPrefix, input.DNSPrefix, input.Name+"-dns", existing)
	cluster.NodeResourceGroup = mergeStr(cluster.NodeResourceGroup, input.NodeResourceGroup,
		"MC_"+input.ResourceGroup+"_"+input.Name+"_"+cluster.Location, existing)

	if input.Tags != nil || !existing {
		cluster.Tags = copyTags(input.Tags)
	}

	if input.EnableRBAC != nil || !existing {
		cluster.EnableRBAC = input.EnableRBAC == nil || *input.EnableRBAC
	}

	if input.NetworkProfile != nil || !existing {
		applyNetworkProfile(cluster, input.NetworkProfile)
	}

	resolveClusterIdentity(cluster, input, existing)

	cluster.FQDN = cluster.DNSPrefix + ".hcp." + defaultIfEmpty(cluster.Location, "eastus") + ".azmk8s.io"
}

// mergeStr resolves a string field: the submitted value wins when non-empty;
// otherwise the stored value is preserved on an update, or def applied on a
// create.
func mergeStr(cur, in, def string, existing bool) string {
	if in != "" {
		return in
	}

	if existing {
		return cur
	}

	return def
}

// resolveClusterIdentity applies the submitted identity block, or preserves the
// stored identity when the update omitted identity entirely.
//
//nolint:gocritic // input is a value-type mirror of the public CreateOrUpdate body.
func resolveClusterIdentity(cluster *ManagedCluster, input ClusterInput, existing bool) {
	if existing && !input.IdentityPresent {
		return
	}

	applyClusterIdentity(cluster, input.IdentityType, input.UserAssignedIdentityIDs)
}

// applyClusterIdentity echoes the submitted managed-identity block, generating
// a deterministic principal/tenant pair for a system-assigned identity and a
// synthesized principal/client pair for each user-assigned identity.
func applyClusterIdentity(cluster *ManagedCluster, identityType string, userAssignedIDs []string) {
	cluster.IdentityType = identityType
	cluster.PrincipalID = ""
	cluster.TenantID = ""
	cluster.UserAssignedIdentities = nil

	if identityType == "" || identityType == "None" {
		return
	}

	if strings.Contains(identityType, "SystemAssigned") {
		cluster.PrincipalID = idgen.SyntheticGUID(
			"principal/cluster/" + cluster.ResourceGroup + "/" + cluster.Name)
		cluster.TenantID = emulatorTenantID
	}

	if strings.Contains(identityType, "UserAssigned") {
		cluster.UserAssignedIdentities = synthesizeUserAssigned(userAssignedIDs)
	}
}

// synthesizeUserAssigned builds a deterministic principal/client pair for each
// submitted user-assigned identity ARM resource ID.
func synthesizeUserAssigned(ids []string) map[string]UserAssignedIdentity {
	if len(ids) == 0 {
		return nil
	}

	out := make(map[string]UserAssignedIdentity, len(ids))
	for _, id := range ids {
		out[id] = UserAssignedIdentity{
			PrincipalID: idgen.SyntheticGUID("principal/uami/" + id),
			ClientID:    idgen.SyntheticGUID("client/uami/" + id),
		}
	}

	return out
}

// applyNetworkProfile stores the submitted network profile verbatim, or the
// standard AKS defaults when the caller omitted properties.networkProfile.
func applyNetworkProfile(cluster *ManagedCluster, np *NetworkProfile) {
	if np == nil {
		cluster.NetworkProfile = defaultNetworkProfile()
		return
	}

	cluster.NetworkProfile = *np
}

// reconcileInlinePools upserts the pools carried in the cluster PUT body by
// NAME and never wipes pools absent from the body — those may be standalone
// agentPools-API pools (azurerm_kubernetes_cluster_node_pool), which real AKS
// leaves intact across a cluster PUT. A PUT that omits agentPoolProfiles leaves
// every existing pool untouched. Caller must hold m.mu (write). clusterVersion
// is inherited by inline pools that omit their own orchestratorVersion.
//
//nolint:gocritic // input is a value-type mirror of the public CreateOrUpdate body.
func (m *Mock) reconcileInlinePools(input ClusterInput, clusterVersion string, now time.Time) []string {
	//nolint:gocritic // pool is a value mirror of the SDK input; copy is intentional.
	for _, pool := range input.AgentPools {
		key := poolKey(input.ResourceGroup, input.Name, pool.Name)
		ap := buildAgentPool(input.ResourceGroup, input.Name, clusterVersion, pool, now)

		if prev, ok := m.pools.Get(key); ok {
			ap.CreatedAt = prev.CreatedAt
		}

		m.pools.Set(key, ap)
	}

	return m.poolNamesForCluster(input.ResourceGroup, input.Name)
}

// poolNamesForCluster returns the sorted names of every pool currently attached
// to the cluster. Caller must hold m.mu.
func (m *Mock) poolNamesForCluster(rg, cluster string) []string {
	prefix := rg + "/" + cluster + "/"
	names := make([]string, 0)

	for _, k := range m.pools.Keys() {
		if strings.HasPrefix(k, prefix) {
			names = append(names, k[len(prefix):])
		}
	}

	sort.Strings(names)

	return names
}

//nolint:gocritic // in is a value mirror of the SDK AgentPool body; pointer would invite caller mutation.
func buildAgentPool(rg, cluster, clusterVersion string, in AgentPoolInput, now time.Time) AgentPool {
	count := int32(defaultNodeCount)
	if in.Count != nil {
		count = *in.Count
	}

	disk := in.OSDiskSizeGB
	if disk <= 0 {
		disk = defaultOSDiskGB
	}

	maxPods := in.MaxPods
	if maxPods <= 0 {
		maxPods = defaultMaxPods
	}

	return AgentPool{
		Name:                   in.Name,
		ClusterName:            cluster,
		ResourceGroup:          rg,
		Count:                  count,
		VMSize:                 defaultIfEmpty(in.VMSize, defaultVMSize),
		OSDiskSizeGB:           disk,
		OSType:                 defaultIfEmpty(in.OSType, "Linux"),
		Mode:                   defaultIfEmpty(in.Mode, "User"),
		OrchestratorVer:        defaultIfEmpty(in.OrchestratorVer, defaultIfEmpty(clusterVersion, defaultK8sVersion)),
		ProvisioningState:      provisioningStateSucceeded,
		ScaleSetPriority:       defaultIfEmpty(in.ScaleSetPriority, "Regular"),
		NodeLabels:             copyLabels(in.NodeLabels),
		NodeTaints:             copyTaints(in.NodeTaints),
		MaxPods:                maxPods,
		OSDiskType:             defaultIfEmpty(in.OSDiskType, defaultOSDiskType),
		Type:                   defaultIfEmpty(in.Type, defaultPoolType),
		PowerState:             poolPowerRunning,
		NodeImageVersion:       defaultNodeImage,
		AvailabilityZones:      copyTaints(in.AvailabilityZones),
		EnableAutoScaling:      copyBoolPtr(in.EnableAutoScaling),
		MinCount:               copyInt32Ptr(in.MinCount),
		MaxCount:               copyInt32Ptr(in.MaxCount),
		VnetSubnetID:           in.VnetSubnetID,
		OSSKU:                  in.OSSKU,
		EnableNodePublicIP:     copyBoolPtr(in.EnableNodePublicIP),
		UpgradeSettings:        copyUpgradeSettings(in.UpgradeSettings),
		Tags:                   copyTags(in.Tags),
		EnableFIPS:             copyBoolPtr(in.EnableFIPS),
		SpotMaxPrice:           copyFloat32Ptr(in.SpotMaxPrice),
		ScaleSetEvictionPolicy: in.ScaleSetEvictionPolicy,
		NodePublicIPPrefixID:   in.NodePublicIPPrefixID,
		KubeletDiskType:        in.KubeletDiskType,
		KubeletConfig:          copyAnyMap(in.KubeletConfig),
		LinuxOSConfig:          copyAnyMap(in.LinuxOSConfig),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

func copyBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}

func copyInt32Ptr(p *int32) *int32 {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}

func copyFloat32Ptr(p *float32) *float32 {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}

func copyAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}

	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func copyUpgradeSettings(s *AgentPoolUpgradeSettings) *AgentPoolUpgradeSettings {
	if s == nil {
		return nil
	}

	out := AgentPoolUpgradeSettings{
		MaxSurge:                  s.MaxSurge,
		DrainTimeoutInMinutes:     copyInt32Ptr(s.DrainTimeoutInMinutes),
		NodeSoakDurationInMinutes: copyInt32Ptr(s.NodeSoakDurationInMinutes),
	}

	return &out
}

func totalNodeCount(pools []AgentPoolInput) int32 {
	var total int32

	for i := range pools {
		c := int32(defaultNodeCount)
		if pools[i].Count != nil {
			c = *pools[i].Count
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

	// Wave 2: tear down the cluster's Kubernetes data-plane state alongside
	// the control-plane record. Subsequent kubeconfig lookups will no longer
	// see a registered UID and fall back to the sentinel.
	if uid, ok := m.k8sUIDs[key]; ok && m.k8sAPI != nil {
		m.k8sAPI.DeregisterCluster(uid)
		delete(m.k8sUIDs, key)
	}

	return nil
}

// ListClustersByResourceGroup returns all clusters in a resource group.
func (m *Mock) ListClustersByResourceGroup(_ context.Context, rg string) ([]ManagedCluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.clusters.Filter(func(_ string, c ManagedCluster) bool {
		return strings.EqualFold(c.ResourceGroup, rg)
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

// StartCluster starts a stopped managed cluster
// (Microsoft.ContainerService/.../start), restoring the control plane and
// every agent pool's VMs to Running. Idempotent: starting an already-running
// cluster succeeds and leaves it unchanged.
func (m *Mock) StartCluster(_ context.Context, rg, name string) (*ManagedCluster, error) {
	return m.setClusterPower(rg, name, clusterPowerRunning, poolPowerRunning)
}

// StopCluster stops a managed cluster (Microsoft.ContainerService/.../stop),
// deallocating the control plane and every agent pool's VMs while preserving
// cluster and workload state — real AKS does not bill a stopped cluster.
// Idempotent: stopping an already-stopped cluster succeeds and leaves it
// unchanged.
func (m *Mock) StopCluster(_ context.Context, rg, name string) (*ManagedCluster, error) {
	return m.setClusterPower(rg, name, clusterPowerStopped, poolPowerStopped)
}

// setClusterPower is the shared body for StartCluster/StopCluster: it sets
// the cluster's powerState.code, settles provisioningState back to Succeeded,
// and mirrors the transition onto every attached agent pool.
func (m *Mock) setClusterPower(rg, name, clusterState, poolState string) (*ManagedCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(rg, name)

	cluster, ok := m.clusters.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", name, rg)
	}

	cluster.PowerState = clusterState
	cluster.ProvisioningState = provisioningStateSucceeded
	cluster.UpdatedAt = m.opts.Clock.Now().UTC()
	m.clusters.Set(key, cluster)
	m.setPoolsPowerState(rg, name, poolState)

	out := cluster

	return &out, nil
}

// setPoolsPowerState updates PowerState on every pool attached to a cluster,
// mirroring the control plane's power transition down onto its agent pools.
// Caller must hold m.mu (write).
func (m *Mock) setPoolsPowerState(rg, cluster, state string) {
	prefix := rg + "/" + cluster + "/"

	for _, k := range m.pools.Keys() {
		if !strings.HasPrefix(k, prefix) {
			continue
		}

		if p, ok := m.pools.Get(k); ok {
			p.PowerState = state
			m.pools.Set(k, p)
		}
	}
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

	clusterRec, ok := m.clusters.Get(cKey)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed cluster %q not found in resource group %q", cluster, rg)
	}

	now := m.opts.Clock.Now().UTC()
	pool := buildAgentPool(rg, cluster, clusterRec.KubernetesVersion, in, now)

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

// DeleteAgentPool removes an agent pool. Deleting the last System-mode pool
// on a cluster is rejected: real AKS requires every cluster to retain at
// least one System pool so the control plane always has somewhere to
// schedule its own components.
func (m *Mock) DeleteAgentPool(_ context.Context, rg, cluster, pool string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := poolKey(rg, cluster, pool)

	ap, ok := m.pools.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "agent pool %q not found on cluster %q", pool, cluster)
	}

	if strings.EqualFold(ap.Mode, agentPoolModeSystem) && m.systemPoolCount(rg, cluster) <= 1 {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"agent pool %q is the last System-mode pool on cluster %q; AKS requires at least one", pool, cluster)
	}

	m.pools.Delete(key)

	cKey := clusterKey(rg, cluster)
	if c, ok := m.clusters.Get(cKey); ok {
		c.AgentPoolNames = removeString(c.AgentPoolNames, pool)
		m.clusters.Set(cKey, c)
	}

	return nil
}

// systemPoolCount returns how many System-mode pools are currently attached
// to a cluster. Caller must hold m.mu.
func (m *Mock) systemPoolCount(rg, cluster string) int {
	all := m.pools.Filter(func(_ string, p AgentPool) bool {
		return strings.EqualFold(p.ResourceGroup, rg) && strings.EqualFold(p.ClusterName, cluster) &&
			strings.EqualFold(p.Mode, agentPoolModeSystem)
	})

	return len(all)
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
		return strings.EqualFold(p.ResourceGroup, rg) && strings.EqualFold(p.ClusterName, cluster)
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
		return strings.EqualFold(mc.ResourceGroup, rg) && strings.EqualFold(mc.ClusterName, cluster)
	})

	out := make([]MaintenanceConfig, 0, len(all))

	for _, mc := range all {
		out = append(out, mc)
	}

	return out, nil
}

// Kubeconfig returns a kubeconfig blob for the named managed cluster.
//
// When a shared kubernetes.APIServer is wired (the normal path) and the
// cluster has a registered UID, the kubeconfig points at <base>/k8s/<uid> —
// the real in-memory K8s API server registered to this cluster on Create.
// When the APIServer isn't wired (Wave 1 fallback), the kubeconfig points
// at the *-DATAPLANE-NOT-IMPLEMENTED sentinel host instead, so callers in
// tests can match on it to confirm the data plane is intentionally
// unimplemented.
func (m *Mock) Kubeconfig(rg, name string) []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.k8sAPI != nil {
		key := clusterKey(rg, name)
		if uid, ok := m.k8sUIDs[key]; ok {
			if base := m.k8sAPI.BaseURL(); base != "" {
				return kubernetes.RenderKubeconfig(base, uid, name)
			}
		}
	}

	// Even on the Wave-1 fallback path (no wired data plane), advertise the
	// shared cluster CA so the kubeconfig is structurally identical to EKS/GKE,
	// which return a real certificate-authority-data unconditionally. Only the
	// server host differs (the NOT-IMPLEMENTED sentinel).
	return fmt.Appendf(nil, `apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: https://AKS-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local
    certificate-authority-data: %s
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
`, name, k8spki.CertificatePEM(), name, name, rg, name, name, rg, name)
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
