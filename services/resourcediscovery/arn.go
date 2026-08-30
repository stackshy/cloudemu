package resourcediscovery

import (
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// Per-provider ARN/URN builders. These produce best-effort canonical
// identifiers — enough for resource discovery output, not necessarily
// byte-equal to what each service's own driver would emit when it owns
// the canonical ID. Phases 2-4 may refine these as the SDK-compat
// handlers expose the exact strings real clients expect.

const azureDefaultResourceGroup = "default"

// Azure ARM resource-group tag keys. The Azure wire handlers stamp the owning
// resource group onto each resource's tags under these keys, because the
// cross-cloud driver models carry no resource-group field. They are duplicated
// here rather than imported because the services layer must not depend on the
// server layer; they are stable wire-contract strings. A resource carries at
// most one of them.
const (
	azureVNetRGTag       = "cloudemu:azureVNetResourceGroup"
	azureNSGRGTag        = "cloudemu:azureNSGResourceGroup"
	azurePublicIPRGTag   = "cloudemu:azurePublicIPResourceGroup"
	azureNATGatewayRGTag = "cloudemu:azureNatGatewayResourceGroup"
	azureRouteTableRGTag = "cloudemu:azureRouteTableResourceGroup"
	azureDiskRGTag       = "cloudemu:azureRG"
)

// azureRGFromTags returns the Azure resource group recorded on a resource's ARM
// tags, or "" when none is present (a portable-API creation that recorded no
// group, or a non-Azure provider whose tags never carry these keys). A resource
// carries at most one of the keys, so the first match wins. Callers gate this on
// the Azure provider so AWS/GCP ARNs stay byte-unchanged.
func (e *Engine) azureRGFromTags(tags map[string]string) string {
	if e.provider != ProviderAzure {
		return ""
	}

	for _, k := range []string{
		azureVNetRGTag, azureNSGRGTag, azurePublicIPRGTag,
		azureNATGatewayRGTag, azureRouteTableRGTag, azureDiskRGTag,
	} {
		if v := tags[k]; v != "" {
			return v
		}
	}

	return ""
}

// Network resource kind constants used by per-provider ARN routing.
const (
	netKindVPC           = "vpc"
	netKindSubnet        = "subnet"
	netKindSecurityGroup = "security-group"
	netKindNetworkIface  = "network-interface"
	netKindElasticIP     = "elastic-ip"
	netKindNATGateway    = "natgateway"
	netKindInternetGW    = "internet-gateway"
	netKindPeering       = "vpc-peering-connection"
	netKindRouteTable    = "route-table"
)

// computeInstanceARN builds the canonical identifier for a compute instance.
// resourceGroup is the Azure resource group the instance belongs to; empty
// falls back to the default group (and it is ignored by the AWS/GCP branches,
// which have no such concept).
func (e *Engine) computeInstanceARN(id, resourceGroup string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("ec2", e.region, e.accountID, "instance/"+id)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup),
			"Microsoft.Compute", "virtualMachines", id)
	case ProviderGCP:
		return id
	default:
		return id
	}
}

// computeVolumeARN canonicalizes a block-volume id. When the driver already
// hands back a fully-qualified id (an Azure managed-disk ARM path, a GCP
// self-link, or an AWS ARN) it is used verbatim; otherwise a per-provider id
// is built from the short id. resourceGroup is the Azure managed-disk group
// (empty falls back to the default); AWS/GCP ignore it.
func (e *Engine) computeVolumeARN(id, resourceGroup string) string {
	if isQualifiedID(id) {
		return id
	}

	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("ec2", e.region, e.accountID, "volume/"+id)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup), "Microsoft.Compute", "disks", id)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "zones/"+e.region+"/disks", id)
	default:
		return id
	}
}

// computeSnapshotARN canonicalizes a block-storage snapshot id, using an
// already-qualified id verbatim and otherwise building a per-provider one.
// resourceGroup is the Azure snapshot group (empty falls back to the default);
// AWS/GCP ignore it.
func (e *Engine) computeSnapshotARN(id, resourceGroup string) string {
	if isQualifiedID(id) {
		return id
	}

	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("ec2", e.region, e.accountID, "snapshot/"+id)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup), "Microsoft.Compute", "snapshots", id)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "global/snapshots", id)
	default:
		return id
	}
}

// isQualifiedID reports whether id is already a canonical cloud identifier and
// should be used as-is rather than rebuilt.
func isQualifiedID(id string) bool {
	switch {
	case len(id) >= 4 && id[:4] == "arn:":
		return true
	case len(id) >= 1 && id[0] == '/':
		return true
	case len(id) >= 8 && id[:8] == "https://":
		return true
	default:
		return false
	}
}

// networkARN builds the canonical identifier for a network resource.
// resourceGroup is the Azure resource group the resource belongs to (empty
// falls back to the default group); AWS/GCP ignore it, so their ARNs are
// byte-unchanged.
func (e *Engine) networkARN(kind, id, resourceGroup string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("ec2", e.region, e.accountID, kind+"/"+id)
	case ProviderAzure:
		azureType := azureNetworkType(kind)
		return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup), "Microsoft.Network", azureType, id)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, gcpNetworkCollection(kind), id)
	default:
		return id
	}
}

func azureNetworkType(kind string) string {
	switch kind {
	case netKindVPC:
		return "virtualNetworks"
	case netKindSubnet:
		return "subnets"
	case netKindSecurityGroup:
		return "networkSecurityGroups"
	case netKindNetworkIface:
		return "networkInterfaces"
	case netKindElasticIP:
		return "publicIPAddresses"
	case netKindNATGateway:
		return "natGateways"
	case netKindInternetGW:
		return "internetGateways"
	case netKindPeering:
		return "virtualNetworkPeerings"
	case netKindRouteTable:
		return "routeTables"
	default:
		return kind
	}
}

func gcpNetworkCollection(kind string) string {
	switch kind {
	case netKindVPC:
		return "networks"
	case netKindSubnet:
		return "subnetworks"
	case netKindSecurityGroup:
		return "firewalls"
	case netKindNetworkIface:
		return "networkInterfaces"
	case netKindElasticIP:
		return "addresses"
	case netKindNATGateway:
		return "routers"
	case netKindInternetGW:
		return "gateways"
	case netKindPeering:
		return "networkPeerings"
	case netKindRouteTable:
		return "routes"
	default:
		return kind
	}
}

// storageBucketARN builds the canonical identifier for a storage bucket/account.
// resourceGroup is the Azure storage-account group (empty falls back to the
// default); AWS/GCP ignore it.
func (e *Engine) storageBucketARN(name, resourceGroup string) string {
	switch e.provider {
	case ProviderAWS:
		return fmt.Sprintf("arn:aws:s3:::%s", name)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup), "Microsoft.Storage",
			"storageAccounts/default/blobServices/default/containers", name)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "buckets", name)
	default:
		return name
	}
}

// databaseTableARN builds the canonical identifier for a database table/account.
// resourceGroup is the Azure Cosmos DB account group (empty falls back to the
// default); AWS/GCP ignore it.
func (e *Engine) databaseTableARN(name, resourceGroup string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("dynamodb", e.region, e.accountID, "table/"+name)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup), "Microsoft.DocumentDB",
			"databaseAccounts/default/sqlDatabases/default/containers", name)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "databases/(default)/collections", name)
	default:
		return name
	}
}

// region and resourceGroup come from the cluster (falling back to engine
// defaults) so the identifier matches the resource's real location: GCP
// self-links embed the region, Azure IDs embed the resource group.
func (e *Engine) kubernetesClusterARN(region, resourceGroup, name string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("eks", region, e.accountID, "cluster/"+name)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup),
			"Microsoft.ContainerService", "managedClusters", name)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "locations/"+region+"/clusters", name)
	default:
		return name
	}
}

func (e *Engine) kubernetesNodeGroupARN(region, resourceGroup, cluster, name string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("eks", region, e.accountID, "nodegroup/"+cluster+"/"+name)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup),
			"Microsoft.ContainerService", "managedClusters/"+cluster+"/agentPools", name)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "locations/"+region+"/clusters/"+cluster+"/nodePools", name)
	default:
		return name
	}
}

func azureResourceGroupOrDefault(rg string) string {
	if rg == "" {
		return azureDefaultResourceGroup
	}

	return rg
}

func (e *Engine) serverlessFunctionARN(name string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("lambda", e.region, e.accountID, "function:"+name)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureDefaultResourceGroup, "Microsoft.Web", "sites", name)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "locations/"+e.region+"/functions", name)
	default:
		return name
	}
}

// monitoringAlarmARN builds the canonical identifier for a metric alarm/alert,
// so the emitted resource carries a stable ARN rather than falling back to the
// bare alarm name.
func (e *Engine) monitoringAlarmARN(name string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("cloudwatch", e.region, e.accountID, "alarm:"+name)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureDefaultResourceGroup, "Microsoft.Insights", "metricAlerts", name)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "alertPolicies", name)
	default:
		return name
	}
}
