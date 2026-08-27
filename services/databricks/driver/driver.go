// Package driver defines the interface for Databricks-style analytics
// workspace services: lifecycle management of managed workspaces.
package driver

import "context"

// Provisioning state values for a workspace.
const (
	StateSucceeded = "Succeeded"
	StateCreating  = "Creating"
	StateDeleting  = "Deleting"
	StateFailed    = "Failed"
)

// WorkspaceCustomStringParameter wraps a string workspace custom parameter in
// the ARM {value:...} envelope (armdatabricks.WorkspaceCustomStringParameter).
type WorkspaceCustomStringParameter struct {
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

// WorkspaceCustomBoolParameter wraps a boolean workspace custom parameter in the
// ARM {value:...} envelope (armdatabricks.WorkspaceCustomBooleanParameter).
type WorkspaceCustomBoolParameter struct {
	Value bool   `json:"value"`
	Type  string `json:"type,omitempty"`
}

// WorkspaceEncryptionParameter wraps the CMK encryption details in the ARM
// {value:...} envelope (armdatabricks.WorkspaceEncryptionParameter).
type WorkspaceEncryptionParameter struct {
	Value *WorkspaceEncryption `json:"value,omitempty"`
	Type  string               `json:"type,omitempty"`
}

// WorkspaceEncryption holds Customer-Managed Key (CMK) encryption details
// (armdatabricks.Encryption). Field JSON names match the real ARM wire exactly.
type WorkspaceEncryption struct {
	KeyName     string `json:"KeyName,omitempty"`
	KeySource   string `json:"keySource,omitempty"`
	KeyVaultURI string `json:"keyvaulturi,omitempty"`
	KeyVersion  string `json:"keyversion,omitempty"`
}

// WorkspaceCustomParameters mirrors armdatabricks.WorkspaceCustomParameters: the
// VNet-injection, CMK, and managed-network parameters a workspace is created
// with. Each field is a {value:...} wrapper so it round-trips byte-identically.
type WorkspaceCustomParameters struct {
	CustomVirtualNetworkID          *WorkspaceCustomStringParameter `json:"customVirtualNetworkId,omitempty"`
	CustomPrivateSubnetName         *WorkspaceCustomStringParameter `json:"customPrivateSubnetName,omitempty"`
	CustomPublicSubnetName          *WorkspaceCustomStringParameter `json:"customPublicSubnetName,omitempty"`
	EnableNoPublicIP                *WorkspaceCustomBoolParameter   `json:"enableNoPublicIp,omitempty"`
	PrepareEncryption               *WorkspaceCustomBoolParameter   `json:"prepareEncryption,omitempty"`
	Encryption                      *WorkspaceEncryptionParameter   `json:"encryption,omitempty"`
	RequireInfrastructureEncryption *WorkspaceCustomBoolParameter   `json:"requireInfrastructureEncryption,omitempty"`
	StorageAccountName              *WorkspaceCustomStringParameter `json:"storageAccountName,omitempty"`
	StorageAccountSKUName           *WorkspaceCustomStringParameter `json:"storageAccountSkuName,omitempty"`
	VnetAddressPrefix               *WorkspaceCustomStringParameter `json:"vnetAddressPrefix,omitempty"`
	NatGatewayName                  *WorkspaceCustomStringParameter `json:"natGatewayName,omitempty"`
	PublicIPName                    *WorkspaceCustomStringParameter `json:"publicIpName,omitempty"`
	LoadBalancerID                  *WorkspaceCustomStringParameter `json:"loadBalancerId,omitempty"`
	LoadBalancerBackendPoolName     *WorkspaceCustomStringParameter `json:"loadBalancerBackendPoolName,omitempty"`
}

// WorkspaceProviderAuthorization mirrors armdatabricks.WorkspaceProviderAuthorization.
type WorkspaceProviderAuthorization struct {
	PrincipalID      string `json:"principalId,omitempty"`
	RoleDefinitionID string `json:"roleDefinitionId,omitempty"`
}

// ManagedIdentityConfiguration mirrors armdatabricks.ManagedIdentityConfiguration
// (the managed-identity details for a workspace's managed disk / storage account).
type ManagedIdentityConfiguration struct {
	PrincipalID string `json:"principalId,omitempty"`
	TenantID    string `json:"tenantId,omitempty"`
	Type        string `json:"type,omitempty"`
}

// WorkspaceExtendedProperties carries the VNet-injection, CMK, and network
// workspace properties beyond the basic managedResourceGroupId/URL/ID set. They
// are set on create (PUT) and must round-trip on GET, and be preserved across a
// tags-only PATCH. Shared between WorkspaceConfig (create input) and Workspace.
type WorkspaceExtendedProperties struct {
	Parameters             *WorkspaceCustomParameters       `json:"parameters,omitempty"`
	PublicNetworkAccess    string                           `json:"publicNetworkAccess,omitempty"`
	RequiredNsgRules       string                           `json:"requiredNsgRules,omitempty"`
	Authorizations         []WorkspaceProviderAuthorization `json:"authorizations,omitempty"`
	UIDefinitionURI        string                           `json:"uiDefinitionUri,omitempty"`
	ManagedDiskIdentity    *ManagedIdentityConfiguration    `json:"managedDiskIdentity,omitempty"`
	StorageAccountIdentity *ManagedIdentityConfiguration    `json:"storageAccountIdentity,omitempty"`
	DiskEncryptionSetID    string                           `json:"diskEncryptionSetId,omitempty"`
}

// WorkspaceConfig describes a workspace to create.
type WorkspaceConfig struct {
	Name                   string
	Subscription           string
	ResourceGroup          string
	Location               string
	SKUName                string
	SKUTier                string
	ManagedResourceGroupID string
	Tags                   map[string]string

	WorkspaceExtendedProperties
}

// Workspace describes a managed analytics workspace.
type Workspace struct {
	ID                     string
	Name                   string
	Subscription           string
	ResourceGroup          string
	Location               string
	SKUName                string
	SKUTier                string
	ManagedResourceGroupID string
	WorkspaceURL           string
	WorkspaceID            string
	ProvisioningState      string
	Tags                   map[string]string
	CreatedAt              string

	WorkspaceExtendedProperties
}

// Databricks is the interface that workspace service implementations must
// satisfy. It also embeds the extended Microsoft.Databricks ARM surface
// (access connectors, private endpoint connections, private link resources,
// VNet peerings, outbound network dependencies, operations — issue #209).
type Databricks interface {
	CreateWorkspace(ctx context.Context, cfg WorkspaceConfig) (*Workspace, error)
	GetWorkspace(ctx context.Context, resourceGroup, name string) (*Workspace, error)
	DeleteWorkspace(ctx context.Context, resourceGroup, name string) error
	UpdateWorkspaceTags(ctx context.Context, resourceGroup, name string, tags map[string]string) (*Workspace, error)
	ListWorkspacesByResourceGroup(ctx context.Context, resourceGroup string) ([]Workspace, error)
	ListWorkspaces(ctx context.Context) ([]Workspace, error)

	ARMResources
}
