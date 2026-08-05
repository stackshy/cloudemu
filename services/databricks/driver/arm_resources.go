package driver

import "context"

// This file extends the Microsoft.Databricks ARM control-plane surface beyond
// workspaces (issue #209): access connectors, private endpoint connections,
// private link resources, virtual-network peerings, outbound network
// dependencies, and the provider operations list.
//
// These are modeled store-and-echo: the ARM wire shapes round-trip faithfully
// over the real armdatabricks SDK, but the underlying networking side effects
// (private-endpoint approval on the platform side, actual VNet peering, live
// outbound reachability) are not simulated — see docs/services.md.

// Peering provisioning/state values.
const (
	PeeringStateInitiated = "Initiated"
	PeeringStateConnected = "Connected"
)

// ManagedIdentity models an ARM managed service identity on an access connector.
type ManagedIdentity struct {
	// Type is one of "None", "SystemAssigned", "UserAssigned",
	// "SystemAssigned,UserAssigned".
	Type string
	// UserAssigned holds the user-assigned identity resource IDs (keys of the
	// ARM userAssignedIdentities map).
	UserAssigned []string
	// PrincipalID/TenantID are synthesized for a system-assigned identity.
	PrincipalID string
	TenantID    string
}

// AccessConnector is a Microsoft.Databricks/accessConnectors resource.
type AccessConnector struct {
	ID                string
	Name              string
	ResourceGroup     string
	Location          string
	Tags              map[string]string
	Identity          *ManagedIdentity
	ProvisioningState string
	CreatedAt         string
}

// AccessConnectorConfig is the createOrUpdate input for an access connector.
type AccessConnectorConfig struct {
	Name          string
	ResourceGroup string
	Location      string
	Tags          map[string]string
	Identity      *ManagedIdentity
}

// PrivateEndpointConnection is a workspace private-endpoint connection.
type PrivateEndpointConnection struct {
	ID                string
	Name              string
	GroupIDs          []string
	PrivateEndpointID string
	Status            string // Pending | Approved | Rejected | Disconnected
	Description       string
	ActionsRequired   string
	ProvisioningState string
}

// GroupIDInformation is a workspace private-link resource (a group id and its
// required members / DNS zones).
type GroupIDInformation struct {
	ID                string
	Name              string
	GroupID           string
	RequiredMembers   []string
	RequiredZoneNames []string
}

// AddressSpace is a list of CIDR address prefixes.
type AddressSpace struct {
	AddressPrefixes []string
}

// VirtualNetworkPeering is a workspace virtualNetworkPeerings resource.
type VirtualNetworkPeering struct {
	ID                        string
	Name                      string
	AllowForwardedTraffic     bool
	AllowGatewayTransit       bool
	AllowVirtualNetworkAccess bool
	UseRemoteGateways         bool
	DatabricksVNetID          string
	DatabricksAddressSpace    *AddressSpace
	RemoteVNetID              string
	RemoteAddressSpace        *AddressSpace
	PeeringState              string
	ProvisioningState         string
}

// VirtualNetworkPeeringConfig is the createOrUpdate input for a peering.
type VirtualNetworkPeeringConfig struct {
	AllowForwardedTraffic     bool
	AllowGatewayTransit       bool
	AllowVirtualNetworkAccess bool
	UseRemoteGateways         bool
	DatabricksVNetID          string
	DatabricksAddressSpace    *AddressSpace
	RemoteVNetID              string
	RemoteAddressSpace        *AddressSpace
}

// OutboundEndpoint is one category of outbound network dependency for a
// workspace and the domains it reaches.
type OutboundEndpoint struct {
	Category  string
	Endpoints []EndpointDependency
}

// EndpointDependency is a domain and the ports the workspace connects to.
type EndpointDependency struct {
	DomainName      string
	EndpointDetails []EndpointDetail
}

// EndpointDetail is a single reachable port for a domain.
type EndpointDetail struct {
	Port int32
}

// Operation is one entry in the provider operations list.
type Operation struct {
	Name         string
	Provider     string
	Resource     string
	Operation    string
	Description  string
	IsDataAction bool
}

// ARMResources is the extended Microsoft.Databricks ARM surface (issue #209).
// It is composed into Databricks so a single driver value serves the whole
// provider namespace.
type ARMResources interface {
	// Access connectors (top-level Microsoft.Databricks/accessConnectors).
	CreateOrUpdateAccessConnector(ctx context.Context, cfg AccessConnectorConfig) (*AccessConnector, error)
	GetAccessConnector(ctx context.Context, resourceGroup, name string) (*AccessConnector, error)
	UpdateAccessConnector(
		ctx context.Context, resourceGroup, name string, tags map[string]string, identity *ManagedIdentity,
	) (*AccessConnector, error)
	DeleteAccessConnector(ctx context.Context, resourceGroup, name string) error
	ListAccessConnectorsByResourceGroup(ctx context.Context, resourceGroup string) ([]AccessConnector, error)
	ListAccessConnectors(ctx context.Context) ([]AccessConnector, error)

	// Private endpoint connections (workspaces/{w}/privateEndpointConnections).
	PutPrivateEndpointConnection(
		ctx context.Context, resourceGroup, workspace, name, status, description string,
	) (*PrivateEndpointConnection, error)
	GetPrivateEndpointConnection(ctx context.Context, resourceGroup, workspace, name string) (*PrivateEndpointConnection, error)
	DeletePrivateEndpointConnection(ctx context.Context, resourceGroup, workspace, name string) error
	ListPrivateEndpointConnections(ctx context.Context, resourceGroup, workspace string) ([]PrivateEndpointConnection, error)

	// Private link resources (workspaces/{w}/privateLinkResources).
	GetPrivateLinkResource(ctx context.Context, resourceGroup, workspace, groupID string) (*GroupIDInformation, error)
	ListPrivateLinkResources(ctx context.Context, resourceGroup, workspace string) ([]GroupIDInformation, error)

	// Virtual network peerings (workspaces/{w}/virtualNetworkPeerings).
	CreateOrUpdateVNetPeering(
		ctx context.Context, resourceGroup, workspace, name string, cfg VirtualNetworkPeeringConfig,
	) (*VirtualNetworkPeering, error)
	GetVNetPeering(ctx context.Context, resourceGroup, workspace, name string) (*VirtualNetworkPeering, error)
	DeleteVNetPeering(ctx context.Context, resourceGroup, workspace, name string) error
	ListVNetPeerings(ctx context.Context, resourceGroup, workspace string) ([]VirtualNetworkPeering, error)

	// Outbound network dependencies (workspaces/{w}/outboundNetworkDependenciesEndpoints).
	ListOutboundNetworkDependencies(ctx context.Context, resourceGroup, workspace string) ([]OutboundEndpoint, error)

	// Provider operations (/providers/Microsoft.Databricks/operations).
	ListOperations(ctx context.Context) ([]Operation, error)
}
