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

// Network resource kind constants used by per-provider ARN routing.
const (
	netKindVPC           = "vpc"
	netKindSubnet        = "subnet"
	netKindSecurityGroup = "security-group"
	netKindNetworkIface  = "network-interface"
	netKindElasticIP     = "elastic-ip"
)

func (e *Engine) computeInstanceARN(id string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("ec2", e.region, e.accountID, "instance/"+id)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureDefaultResourceGroup, "Microsoft.Compute", "virtualMachines", id)
	case ProviderGCP:
		return id
	default:
		return id
	}
}

// computeVolumeARN canonicalizes a block-volume id. When the driver already
// hands back a fully-qualified id (an Azure managed-disk ARM path, a GCP
// self-link, or an AWS ARN) it is used verbatim; otherwise a per-provider id
// is built from the short id.
func (e *Engine) computeVolumeARN(id string) string {
	if isQualifiedID(id) {
		return id
	}

	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("ec2", e.region, e.accountID, "volume/"+id)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureDefaultResourceGroup, "Microsoft.Compute", "disks", id)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "zones/"+e.region+"/disks", id)
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

func (e *Engine) networkARN(kind, id string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("ec2", e.region, e.accountID, kind+"/"+id)
	case ProviderAzure:
		azureType := azureNetworkType(kind)
		return idgen.AzureID(e.accountID, azureDefaultResourceGroup, "Microsoft.Network", azureType, id)
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
	default:
		return kind
	}
}

func (e *Engine) storageBucketARN(name string) string {
	switch e.provider {
	case ProviderAWS:
		return fmt.Sprintf("arn:aws:s3:::%s", name)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureDefaultResourceGroup, "Microsoft.Storage",
			"storageAccounts/default/blobServices/default/containers", name)
	case ProviderGCP:
		return idgen.GCPID(e.accountID, "buckets", name)
	default:
		return name
	}
}

func (e *Engine) databaseTableARN(name string) string {
	switch e.provider {
	case ProviderAWS:
		return idgen.AWSARN("dynamodb", e.region, e.accountID, "table/"+name)
	case ProviderAzure:
		return idgen.AzureID(e.accountID, azureDefaultResourceGroup, "Microsoft.DocumentDB",
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
