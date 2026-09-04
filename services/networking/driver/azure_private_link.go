package driver

import "context"

// Microsoft.Network's Private Link surface — privateEndpoints and
// privateLinkServices — has no equivalent in the cross-cloud Networking model,
// so — like AzureNetworkGateways and AzurePublicIPPrefixes — the Azure provider
// stores it through this OPTIONAL, type-asserted capability. AWS and GCP do not
// implement it. Both resource types are addressed by (resourceGroup, name) to
// match ARM; an empty resourceGroup on a List means subscription-wide.
//
// These are the standalone Microsoft.Network/privateEndpoints and
// privateLinkServices resources, distinct from the per-service
// privateEndpointConnections sub-resource surfaces (e.g. the cosmos-postgres or
// databricks workspace connections), which each service models on its own.

// AzurePrivateLinkServiceConnection is one privateLinkServiceConnections entry
// on a private endpoint: the consumer's connection to a provider resource.
type AzurePrivateLinkServiceConnection struct {
	Name                 string
	PrivateLinkServiceID string
	GroupIDs             []string
	RequestMessage       string
	// Status is the connection-state enum (Pending/Approved/Rejected). The
	// emulator auto-approves a new connection, so it defaults to Approved unless
	// the request supplies one.
	Status      string
	Description string
}

// AzurePrivateEndpoint is one Microsoft.Network/privateEndpoints resource.
type AzurePrivateEndpoint struct {
	Name                          string
	ResourceGroup                 string
	Location                      string
	Tags                          map[string]string
	SubnetID                      string
	CustomNetworkInterfaceName    string
	PrivateLinkServiceConnections []AzurePrivateLinkServiceConnection
	ManualConnections             []AzurePrivateLinkServiceConnection
}

// AzurePrivateLinkServiceIPConfiguration is one ipConfigurations entry of a
// private link service, allocating an address from the service's subnet.
type AzurePrivateLinkServiceIPConfiguration struct {
	Name                      string
	SubnetID                  string
	PrivateIPAllocationMethod string
	Primary                   bool
}

// AzurePrivateLinkService is one Microsoft.Network/privateLinkServices resource
// — the provider side of a Private Link, fronted by a load balancer.
type AzurePrivateLinkService struct {
	Name                    string
	ResourceGroup           string
	Location                string
	Tags                    map[string]string
	LoadBalancerFrontendIDs []string
	IPConfigurations        []AzurePrivateLinkServiceIPConfiguration
	VisibilitySubscriptions []string
	AutoApprovalSubs        []string
	EnableProxyProtocol     bool
	Fqdns                   []string
}

// AzurePrivateLink is the Azure-only Private Link surface. Each resource type is
// keyed by (resourceGroup, name) for idempotent createOrUpdate.
type AzurePrivateLink interface {
	// PutAzurePrivateEndpoint creates or replaces a private endpoint in place (a
	// repeat createOrUpdate PUT updates rather than duplicating), returning the
	// stored value.
	PutAzurePrivateEndpoint(ctx context.Context, pe AzurePrivateEndpoint) AzurePrivateEndpoint
	// GetAzurePrivateEndpoint returns the endpoint identified by (resourceGroup, name).
	GetAzurePrivateEndpoint(ctx context.Context, resourceGroup, name string) (AzurePrivateEndpoint, bool)
	// DeleteAzurePrivateEndpoint removes the endpoint, reporting whether it existed.
	DeleteAzurePrivateEndpoint(ctx context.Context, resourceGroup, name string) bool
	// ListAzurePrivateEndpoints returns the endpoints in a resource group, or all
	// when resourceGroup is empty, ordered by key.
	ListAzurePrivateEndpoints(ctx context.Context, resourceGroup string) []AzurePrivateEndpoint

	// PutAzurePrivateLinkService creates or replaces a private link service in place.
	PutAzurePrivateLinkService(ctx context.Context, pls AzurePrivateLinkService) AzurePrivateLinkService
	// GetAzurePrivateLinkService returns the service identified by (resourceGroup, name).
	GetAzurePrivateLinkService(ctx context.Context, resourceGroup, name string) (AzurePrivateLinkService, bool)
	// DeleteAzurePrivateLinkService removes the service, reporting whether it existed.
	DeleteAzurePrivateLinkService(ctx context.Context, resourceGroup, name string) bool
	// ListAzurePrivateLinkServices returns the services in a resource group, or all
	// when resourceGroup is empty, ordered by key.
	ListAzurePrivateLinkServices(ctx context.Context, resourceGroup string) []AzurePrivateLinkService
}
