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
