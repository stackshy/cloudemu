package aks

import (
	"github.com/stackshy/cloudemu/v2/providers/azure/aks"
)

// ARM resource type identifiers.
const (
	resourceTypeManagedClusters    = "managedClusters"
	resourceTypeAgentPool          = "Microsoft.ContainerService/managedClusters/agentPools"
	resourceTypeMaintenanceConfig  = "Microsoft.ContainerService/managedClusters/maintenanceConfigurations"
	resourceTypeManagedClusterFull = "Microsoft.ContainerService/managedClusters"
)

// armManagedCluster mirrors the JSON shape Azure ARM expects for
// Microsoft.ContainerService/managedClusters. Only the fields cloudemu cares
// about are wired through; unknown fields are accepted on decode (because we
// keep the struct open at the JSON layer) and dropped on response.
type armManagedCluster struct {
	ID         string                       `json:"id,omitempty"`
	Name       string                       `json:"name,omitempty"`
	Type       string                       `json:"type,omitempty"`
	Location   string                       `json:"location,omitempty"`
	Tags       map[string]*string           `json:"tags,omitempty"`
	SKU        *armManagedClusterSKU        `json:"sku,omitempty"`
	Identity   *armManagedClusterIdentity   `json:"identity,omitempty"`
	Properties *armManagedClusterProperties `json:"properties,omitempty"`
}

// armManagedClusterIdentity mirrors armcontainerservice.ManagedClusterIdentity.
type armManagedClusterIdentity struct {
	Type                   string                           `json:"type,omitempty"`
	PrincipalID            string                           `json:"principalId,omitempty"`
	TenantID               string                           `json:"tenantId,omitempty"`
	UserAssignedIdentities map[string]*armUserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// armUserAssignedValue mirrors
// armcontainerservice.ManagedServiceIdentityUserAssignedIdentitiesValue.
type armUserAssignedValue struct {
	PrincipalID string `json:"principalId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
}

// armManagedClusterSKU mirrors armcontainerservice.ManagedClusterSKU. The tier
// (Free / Standard / Premium) is the uptime-SLA cost input a discoverer reads.
type armManagedClusterSKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type armManagedClusterProperties struct {
	ProvisioningState        string                `json:"provisioningState,omitempty"`
	KubernetesVersion        string                `json:"kubernetesVersion,omitempty"`
	CurrentKubernetesVersion string                `json:"currentKubernetesVersion,omitempty"`
	DNSPrefix                string                `json:"dnsPrefix,omitempty"`
	Fqdn                     string                `json:"fqdn,omitempty"`
	NodeResourceGroup        string                `json:"nodeResourceGroup,omitempty"`
	AgentPoolProfiles        []armAgentPoolProfile `json:"agentPoolProfiles,omitempty"`
	PowerState               *armPowerState        `json:"powerState,omitempty"`
	EnableRBAC               *bool                 `json:"enableRBAC,omitempty"`
	NetworkProfile           *armNetworkProfile    `json:"networkProfile,omitempty"`
}

type armPowerState struct {
	Code string `json:"code,omitempty"`
}

// armNetworkProfile mirrors the subset of
// armcontainerservice.NetworkProfile the emulator synthesizes as defaults.
type armNetworkProfile struct {
	NetworkPlugin   string `json:"networkPlugin,omitempty"`
	NetworkPolicy   string `json:"networkPolicy,omitempty"`
	LoadBalancerSKU string `json:"loadBalancerSku,omitempty"`
	ServiceCidr     string `json:"serviceCidr,omitempty"`
	DNSServiceIP    string `json:"dnsServiceIP,omitempty"`
	PodCidr         string `json:"podCidr,omitempty"`
	OutboundType    string `json:"outboundType,omitempty"`
}

type armAgentPoolProfile struct {
	Name              string             `json:"name,omitempty"`
	Count             *int32             `json:"count,omitempty"`
	VMSize            string             `json:"vmSize,omitempty"`
	OSDiskSizeGB      int32              `json:"osDiskSizeGB,omitempty"`
	OSType            string             `json:"osType,omitempty"`
	Mode              string             `json:"mode,omitempty"`
	OrchestratorVer   string             `json:"orchestratorVersion,omitempty"`
	ScaleSetPriority  string             `json:"scaleSetPriority,omitempty"`
	NodeLabels        map[string]*string `json:"nodeLabels,omitempty"`
	NodeTaints        []string           `json:"nodeTaints,omitempty"`
	ProvisioningState string             `json:"provisioningState,omitempty"`
	MaxPods           int32              `json:"maxPods,omitempty"`
	OSDiskType        string             `json:"osDiskType,omitempty"`
	Type              string             `json:"type,omitempty"`
	PowerState        *armPowerState     `json:"powerState,omitempty"`
	NodeImageVersion  string             `json:"nodeImageVersion,omitempty"`
	armAgentPoolAdvanced
}

// armAgentPoolAdvanced holds the optional node-pool fields Terraform's
// default_node_pool commonly submits. Embedded in both the inline profile and
// the standalone-pool shapes so both wire paths model them identically. Every
// field is omitempty so a value absent on the request is absent on the
// response — which keeps any unmodeled sibling sub-key round-tripping through
// the property overlay.
type armAgentPoolAdvanced struct {
	AvailabilityZones  []string `json:"availabilityZones,omitempty"`
	EnableAutoScaling  *bool    `json:"enableAutoScaling,omitempty"`
	MinCount           *int32   `json:"minCount,omitempty"`
	MaxCount           *int32   `json:"maxCount,omitempty"`
	VnetSubnetID       string   `json:"vnetSubnetID,omitempty"`
	OSSKU              string   `json:"osSKU,omitempty"`
	EnableNodePublicIP *bool    `json:"enableNodePublicIP,omitempty"`
	// Further default_node_pool fields modeled so an inline submission
	// round-trips the same as a standalone pool.
	UpgradeSettings        *armAgentPoolUpgradeSettings `json:"upgradeSettings,omitempty"`
	Tags                   map[string]*string           `json:"tags,omitempty"`
	EnableFIPS             *bool                        `json:"enableFIPS,omitempty"`
	SpotMaxPrice           *float32                     `json:"spotMaxPrice,omitempty"`
	ScaleSetEvictionPolicy string                       `json:"scaleSetEvictionPolicy,omitempty"`
	NodePublicIPPrefixID   string                       `json:"nodePublicIPPrefixID,omitempty"`
	KubeletDiskType        string                       `json:"kubeletDiskType,omitempty"`
	KubeletConfig          map[string]any               `json:"kubeletConfig,omitempty"`
	LinuxOSConfig          map[string]any               `json:"linuxOSConfig,omitempty"`
}

// armAgentPoolUpgradeSettings mirrors the subset of
// armcontainerservice.AgentPoolUpgradeSettings the emulator round-trips.
type armAgentPoolUpgradeSettings struct {
	MaxSurge                  string `json:"maxSurge,omitempty"`
	DrainTimeoutInMinutes     *int32 `json:"drainTimeoutInMinutes,omitempty"`
	NodeSoakDurationInMinutes *int32 `json:"nodeSoakDurationInMinutes,omitempty"`
}

// applyTo copies the submitted advanced fields onto a driver AgentPoolInput.
// Shared by the inline and standalone agent-pool wire paths so both behave
// identically.
func (a *armAgentPoolAdvanced) applyTo(in *aks.AgentPoolInput) {
	in.AvailabilityZones = a.AvailabilityZones
	in.EnableAutoScaling = a.EnableAutoScaling
	in.MinCount = a.MinCount
	in.MaxCount = a.MaxCount
	in.VnetSubnetID = a.VnetSubnetID
	in.OSSKU = a.OSSKU
	in.EnableNodePublicIP = a.EnableNodePublicIP
	in.Tags = fromPtrTags(a.Tags)
	in.EnableFIPS = a.EnableFIPS
	in.SpotMaxPrice = a.SpotMaxPrice
	in.ScaleSetEvictionPolicy = a.ScaleSetEvictionPolicy
	in.NodePublicIPPrefixID = a.NodePublicIPPrefixID
	in.KubeletDiskType = a.KubeletDiskType
	in.KubeletConfig = a.KubeletConfig
	in.LinuxOSConfig = a.LinuxOSConfig
	in.UpgradeSettings = a.upgradeSettingsInput()
}

// upgradeSettingsInput maps the submitted upgradeSettings onto the driver type.
func (a *armAgentPoolAdvanced) upgradeSettingsInput() *aks.AgentPoolUpgradeSettings {
	if a.UpgradeSettings == nil {
		return nil
	}

	return &aks.AgentPoolUpgradeSettings{
		MaxSurge:                  a.UpgradeSettings.MaxSurge,
		DrainTimeoutInMinutes:     a.UpgradeSettings.DrainTimeoutInMinutes,
		NodeSoakDurationInMinutes: a.UpgradeSettings.NodeSoakDurationInMinutes,
	}
}

// armAgentPool is the standalone (sub-resource) shape used by the
// agentPools/{name} endpoint. It carries the same property bag as the inline
// armAgentPoolProfile but as the sole "properties" body.
type armAgentPool struct {
	ID         string                  `json:"id,omitempty"`
	Name       string                  `json:"name,omitempty"`
	Type       string                  `json:"type,omitempty"`
	Properties *armAgentPoolProperties `json:"properties,omitempty"`
}

type armAgentPoolProperties struct {
	Count             *int32             `json:"count,omitempty"`
	VMSize            string             `json:"vmSize,omitempty"`
	OSDiskSizeGB      int32              `json:"osDiskSizeGB,omitempty"`
	OSType            string             `json:"osType,omitempty"`
	Mode              string             `json:"mode,omitempty"`
	OrchestratorVer   string             `json:"orchestratorVersion,omitempty"`
	ScaleSetPriority  string             `json:"scaleSetPriority,omitempty"`
	NodeLabels        map[string]*string `json:"nodeLabels,omitempty"`
	NodeTaints        []string           `json:"nodeTaints,omitempty"`
	ProvisioningState string             `json:"provisioningState,omitempty"`
	MaxPods           int32              `json:"maxPods,omitempty"`
	OSDiskType        string             `json:"osDiskType,omitempty"`
	Type              string             `json:"type,omitempty"`
	PowerState        *armPowerState     `json:"powerState,omitempty"`
	NodeImageVersion  string             `json:"nodeImageVersion,omitempty"`
	armAgentPoolAdvanced
}

// armMaintenanceConfig is the wire shape for the maintenanceConfigurations
// sub-resource. The properties bag is opaque — the SDK serializes its rich
// MaintenanceConfigurationProperties into JSON and we round-trip it.
type armMaintenanceConfig struct {
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Type       string         `json:"type,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// armTagsObject is the body shape for PATCH (UpdateTags).
type armTagsObject struct {
	Tags map[string]*string `json:"tags,omitempty"`
}

// armCredentialResults is the response shape for ListCluster*Credential.
type armCredentialResults struct {
	Kubeconfigs []armCredentialResult `json:"kubeconfigs,omitempty"`
}

type armCredentialResult struct {
	Name  string `json:"name,omitempty"`
	Value []byte `json:"value,omitempty"`
}

// armList is the ARM list-response envelope.
type armList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// toARMCluster converts the in-memory cluster + its pools to the ARM JSON
// shape returned by the SDK. Pools are listed inline under
// properties.agentPoolProfiles for parity with the real API.
func toARMCluster(c *aks.ManagedCluster, pools []aks.AgentPool, subscription string) armManagedCluster {
	enableRBAC := c.EnableRBAC
	out := armManagedCluster{
		ID:       aks.ClusterResourceID(subscription, c.ResourceGroup, c.Name),
		Name:     c.Name,
		Type:     resourceTypeManagedClusterFull,
		Location: c.Location,
		Tags:     toPtrTags(c.Tags),
		SKU:      &armManagedClusterSKU{Name: "Base", Tier: c.Tier},
		Properties: &armManagedClusterProperties{
			ProvisioningState:        c.ProvisioningState,
			KubernetesVersion:        c.KubernetesVersion,
			CurrentKubernetesVersion: c.KubernetesVersion,
			DNSPrefix:                c.DNSPrefix,
			Fqdn:                     c.FQDN,
			NodeResourceGroup:        c.NodeResourceGroup,
			AgentPoolProfiles:        toAgentPoolProfiles(pools),
			PowerState:               &armPowerState{Code: c.PowerState},
			EnableRBAC:               &enableRBAC,
			NetworkProfile:           toNetworkProfile(&c.NetworkProfile),
		},
	}

	if c.IdentityType != "" && c.IdentityType != "None" {
		out.Identity = &armManagedClusterIdentity{
			Type:                   c.IdentityType,
			PrincipalID:            c.PrincipalID,
			TenantID:               c.TenantID,
			UserAssignedIdentities: toUserAssignedIdentities(c.UserAssignedIdentities),
		}
	}

	return out
}

// toUserAssignedIdentities renders the stored user-assigned identity map onto
// the ARM identity shape.
func toUserAssignedIdentities(in map[string]aks.UserAssignedIdentity) map[string]*armUserAssignedValue {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]*armUserAssignedValue, len(in))
	for id, v := range in {
		out[id] = &armUserAssignedValue{PrincipalID: v.PrincipalID, ClientID: v.ClientID}
	}

	return out
}

// toNetworkProfile renders the stored network profile onto the ARM shape. The
// values are whatever CreateOrUpdateCluster stored — the caller's submitted
// values, or the AKS defaults when the caller omitted networkProfile. Every
// field is omitempty, so a sub-key the caller never set is not emitted (and a
// sub-key the emulator does not model still round-trips via the overlay).
func toNetworkProfile(np *aks.NetworkProfile) *armNetworkProfile {
	return &armNetworkProfile{
		NetworkPlugin:   np.NetworkPlugin,
		NetworkPolicy:   np.NetworkPolicy,
		LoadBalancerSKU: np.LoadBalancerSKU,
		ServiceCidr:     np.ServiceCidr,
		DNSServiceIP:    np.DNSServiceIP,
		PodCidr:         np.PodCidr,
		OutboundType:    np.OutboundType,
	}
}

// toAgentPoolAdvanced renders the optional advanced pool fields onto the shared
// embedded ARM shape used by both the inline and standalone pool responses.
func toAgentPoolAdvanced(p *aks.AgentPool) armAgentPoolAdvanced {
	return armAgentPoolAdvanced{
		AvailabilityZones:      p.AvailabilityZones,
		EnableAutoScaling:      p.EnableAutoScaling,
		MinCount:               p.MinCount,
		MaxCount:               p.MaxCount,
		VnetSubnetID:           p.VnetSubnetID,
		OSSKU:                  p.OSSKU,
		EnableNodePublicIP:     p.EnableNodePublicIP,
		Tags:                   toPtrTags(p.Tags),
		EnableFIPS:             p.EnableFIPS,
		SpotMaxPrice:           p.SpotMaxPrice,
		ScaleSetEvictionPolicy: p.ScaleSetEvictionPolicy,
		NodePublicIPPrefixID:   p.NodePublicIPPrefixID,
		KubeletDiskType:        p.KubeletDiskType,
		KubeletConfig:          p.KubeletConfig,
		LinuxOSConfig:          p.LinuxOSConfig,
		UpgradeSettings:        toUpgradeSettings(p.UpgradeSettings),
	}
}

// toUpgradeSettings renders the stored upgrade settings onto the ARM shape.
func toUpgradeSettings(s *aks.AgentPoolUpgradeSettings) *armAgentPoolUpgradeSettings {
	if s == nil {
		return nil
	}

	return &armAgentPoolUpgradeSettings{
		MaxSurge:                  s.MaxSurge,
		DrainTimeoutInMinutes:     s.DrainTimeoutInMinutes,
		NodeSoakDurationInMinutes: s.NodeSoakDurationInMinutes,
	}
}

func toAgentPoolProfiles(pools []aks.AgentPool) []armAgentPoolProfile {
	if len(pools) == 0 {
		return nil
	}

	out := make([]armAgentPoolProfile, 0, len(pools))
	for i := range pools {
		out = append(out, armAgentPoolProfile{
			Name:                 pools[i].Name,
			Count:                ptrInt32(pools[i].Count),
			VMSize:               pools[i].VMSize,
			OSDiskSizeGB:         pools[i].OSDiskSizeGB,
			OSType:               pools[i].OSType,
			Mode:                 pools[i].Mode,
			OrchestratorVer:      pools[i].OrchestratorVer,
			ScaleSetPriority:     pools[i].ScaleSetPriority,
			NodeLabels:           toPtrTags(pools[i].NodeLabels),
			NodeTaints:           pools[i].NodeTaints,
			ProvisioningState:    pools[i].ProvisioningState,
			MaxPods:              pools[i].MaxPods,
			OSDiskType:           pools[i].OSDiskType,
			Type:                 pools[i].Type,
			PowerState:           &armPowerState{Code: pools[i].PowerState},
			NodeImageVersion:     pools[i].NodeImageVersion,
			armAgentPoolAdvanced: toAgentPoolAdvanced(&pools[i]),
		})
	}

	return out
}

// toARMAgentPool converts an in-memory agent pool to the ARM JSON shape used
// by the standalone agentPools/{name} endpoint.
func toARMAgentPool(p *aks.AgentPool, subscription string) armAgentPool {
	return armAgentPool{
		ID:   aks.AgentPoolResourceID(subscription, p.ResourceGroup, p.ClusterName, p.Name),
		Name: p.Name,
		Type: resourceTypeAgentPool,
		Properties: &armAgentPoolProperties{
			Count:                ptrInt32(p.Count),
			VMSize:               p.VMSize,
			OSDiskSizeGB:         p.OSDiskSizeGB,
			OSType:               p.OSType,
			Mode:                 p.Mode,
			OrchestratorVer:      p.OrchestratorVer,
			ScaleSetPriority:     p.ScaleSetPriority,
			NodeLabels:           toPtrTags(p.NodeLabels),
			NodeTaints:           p.NodeTaints,
			ProvisioningState:    p.ProvisioningState,
			MaxPods:              p.MaxPods,
			OSDiskType:           p.OSDiskType,
			Type:                 p.Type,
			PowerState:           &armPowerState{Code: p.PowerState},
			NodeImageVersion:     p.NodeImageVersion,
			armAgentPoolAdvanced: toAgentPoolAdvanced(p),
		},
	}
}

// toARMMaintenance converts an in-memory maintenance config to ARM JSON.
func toARMMaintenance(mc *aks.MaintenanceConfig, subscription string) armMaintenanceConfig {
	return armMaintenanceConfig{
		ID:         aks.MaintenanceConfigResourceID(subscription, mc.ResourceGroup, mc.ClusterName, mc.Name),
		Name:       mc.Name,
		Type:       resourceTypeMaintenanceConfig,
		Properties: mc.Properties,
	}
}

// ptrInt32 returns a pointer to v. Agent-pool count is a pointer on the wire so
// an explicit 0 (scale-to-zero) round-trips; the stored value is always
// concrete, so the response pointer is always non-nil.
func ptrInt32(v int32) *int32 {
	return &v
}

// toPtrTags converts a flat map[string]string to ARM's map[string]*string.
func toPtrTags(in map[string]string) map[string]*string {
	if in == nil {
		return nil
	}

	out := make(map[string]*string, len(in))

	for k, v := range in {
		val := v
		out[k] = &val
	}

	return out
}

// fromPtrTags converts ARM's map[string]*string to flat map[string]string.
func fromPtrTags(in map[string]*string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if v != nil {
			out[k] = *v
		}
	}

	return out
}
