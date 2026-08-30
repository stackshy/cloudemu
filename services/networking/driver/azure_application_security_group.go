package driver

import "context"

// Microsoft.Network/applicationSecurityGroups is a tag-like grouping of network
// interfaces: it carries no properties of its own beyond identity, location and
// tags, and is referenced by id from NIC ipConfigurations and NSG security
// rules. The cross-cloud Networking model has no equivalent, so — like
// AzureNetworkMetadata — the Azure provider stores it through this OPTIONAL,
// type-asserted capability. AWS and GCP do not implement it.

// AzureApplicationSecurityGroup is one Microsoft.Network/applicationSecurityGroups
// resource, addressed by (resourceGroup, name) to match ARM.
type AzureApplicationSecurityGroup struct {
	Name          string
	ResourceGroup string
	Location      string
	Tags          map[string]string
}

// AzureApplicationSecurityGroups is the Azure-only application-security-group
// surface. Keyed by (resourceGroup, name) for idempotent createOrUpdate; an
// empty resourceGroup on List means subscription-wide.
type AzureApplicationSecurityGroups interface {
	// PutAzureApplicationSecurityGroup creates or replaces an ASG in place (a
	// repeat createOrUpdate PUT updates rather than duplicating), returning the
	// stored value.
	PutAzureApplicationSecurityGroup(ctx context.Context, asg AzureApplicationSecurityGroup) AzureApplicationSecurityGroup
	// GetAzureApplicationSecurityGroup returns the ASG identified by
	// (resourceGroup, name).
	GetAzureApplicationSecurityGroup(ctx context.Context, resourceGroup, name string) (AzureApplicationSecurityGroup, bool)
	// DeleteAzureApplicationSecurityGroup removes the ASG, reporting whether it
	// existed.
	DeleteAzureApplicationSecurityGroup(ctx context.Context, resourceGroup, name string) bool
	// ListAzureApplicationSecurityGroups returns the ASGs in a resource group, or
	// all when resourceGroup is empty (subscription-wide list), ordered by key.
	ListAzureApplicationSecurityGroups(ctx context.Context, resourceGroup string) []AzureApplicationSecurityGroup
}
