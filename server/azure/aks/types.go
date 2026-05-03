package aks

import (
	"github.com/stackshy/cloudemu/providers/azure/aks"
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
	Properties *armManagedClusterProperties `json:"properties,omitempty"`
}

type armManagedClusterProperties struct {
	ProvisioningState string                `json:"provisioningState,omitempty"`
	KubernetesVersion string                `json:"kubernetesVersion,omitempty"`
	DNSPrefix         string                `json:"dnsPrefix,omitempty"`
	Fqdn              string                `json:"fqdn,omitempty"`
	NodeResourceGroup string                `json:"nodeResourceGroup,omitempty"`
	AgentPoolProfiles []armAgentPoolProfile `json:"agentPoolProfiles,omitempty"`
	PowerState        *armPowerState        `json:"powerState,omitempty"`
	EnableRBAC        *bool                 `json:"enableRBAC,omitempty"`
}

type armPowerState struct {
	Code string `json:"code,omitempty"`
}

type armAgentPoolProfile struct {
	Name              string             `json:"name,omitempty"`
	Count             int32              `json:"count,omitempty"`
	VMSize            string             `json:"vmSize,omitempty"`
	OSDiskSizeGB      int32              `json:"osDiskSizeGB,omitempty"`
	OSType            string             `json:"osType,omitempty"`
	Mode              string             `json:"mode,omitempty"`
	OrchestratorVer   string             `json:"orchestratorVersion,omitempty"`
	NodeLabels        map[string]*string `json:"nodeLabels,omitempty"`
	NodeTaints        []string           `json:"nodeTaints,omitempty"`
	ProvisioningState string             `json:"provisioningState,omitempty"`
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
	Count             int32              `json:"count,omitempty"`
	VMSize            string             `json:"vmSize,omitempty"`
	OSDiskSizeGB      int32              `json:"osDiskSizeGB,omitempty"`
	OSType            string             `json:"osType,omitempty"`
	Mode              string             `json:"mode,omitempty"`
	OrchestratorVer   string             `json:"orchestratorVersion,omitempty"`
	NodeLabels        map[string]*string `json:"nodeLabels,omitempty"`
	NodeTaints        []string           `json:"nodeTaints,omitempty"`
	ProvisioningState string             `json:"provisioningState,omitempty"`
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
	return armManagedCluster{
		ID:       aks.ClusterResourceID(subscription, c.ResourceGroup, c.Name),
		Name:     c.Name,
		Type:     resourceTypeManagedClusterFull,
		Location: c.Location,
		Tags:     toPtrTags(c.Tags),
		Properties: &armManagedClusterProperties{
			ProvisioningState: c.ProvisioningState,
			KubernetesVersion: c.KubernetesVersion,
			DNSPrefix:         c.DNSPrefix,
			Fqdn:              c.FQDN,
			NodeResourceGroup: c.NodeResourceGroup,
			AgentPoolProfiles: toAgentPoolProfiles(pools),
			PowerState:        &armPowerState{Code: c.PowerState},
		},
	}
}

func toAgentPoolProfiles(pools []aks.AgentPool) []armAgentPoolProfile {
	if len(pools) == 0 {
		return nil
	}

	out := make([]armAgentPoolProfile, 0, len(pools))
	for i := range pools {
		out = append(out, armAgentPoolProfile{
			Name:              pools[i].Name,
			Count:             pools[i].Count,
			VMSize:            pools[i].VMSize,
			OSDiskSizeGB:      pools[i].OSDiskSizeGB,
			OSType:            pools[i].OSType,
			Mode:              pools[i].Mode,
			OrchestratorVer:   pools[i].OrchestratorVer,
			NodeLabels:        toPtrTags(pools[i].NodeLabels),
			NodeTaints:        pools[i].NodeTaints,
			ProvisioningState: pools[i].ProvisioningState,
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
			Count:             p.Count,
			VMSize:            p.VMSize,
			OSDiskSizeGB:      p.OSDiskSizeGB,
			OSType:            p.OSType,
			Mode:              p.Mode,
			OrchestratorVer:   p.OrchestratorVer,
			NodeLabels:        toPtrTags(p.NodeLabels),
			NodeTaints:        p.NodeTaints,
			ProvisioningState: p.ProvisioningState,
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
