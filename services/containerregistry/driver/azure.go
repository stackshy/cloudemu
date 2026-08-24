package driver

import (
	"context"
	"time"
)

// The Azure Container Registry ARM management plane
// (Microsoft.ContainerRegistry/registries) is an Azure-specific optional
// capability kept out of the cross-cloud ContainerRegistry interface: a
// registry is an ARM resource with a SKU, an admin credential pair, quota
// usages, webhooks and geo-replications that the AWS ECR / GCP Artifact
// Registry models do not share. A provider exposes it by implementing
// AzureRegistryManager; the wire handler reaches it by type assertion,
// mirroring the AzureNetworkInterfaces pattern in the networking driver.

// AzureRegistryConfig is the create-or-update payload for an ACR registry.
type AzureRegistryConfig struct {
	Location         string
	Tags             map[string]string
	SKUName          string // Basic / Standard / Premium (defaults to Standard)
	AdminUserEnabled bool
	// IdentityType is the managed-identity type submitted in the ARM identity
	// block: "SystemAssigned", "UserAssigned", "SystemAssigned, UserAssigned"
	// or "None"/"" for no identity.
	IdentityType string
}

// AzureRegistry is a stored/returned ACR registry resource.
type AzureRegistry struct {
	Name              string
	ResourceGroup     string
	Location          string
	SKUName           string
	SKUTier           string
	LoginServer       string
	AdminUserEnabled  bool
	ProvisioningState string
	CreationDate      time.Time
	Tags              map[string]string
	// IdentityType and the principal/tenant IDs echo the managed identity block.
	// PrincipalID/TenantID are populated only when a system-assigned identity is
	// requested.
	IdentityType string
	PrincipalID  string
	TenantID     string
}

// AzureRegistryCredentials is the admin username / password pair returned by
// listCredentials and regenerateCredential.
type AzureRegistryCredentials struct {
	Username  string
	Password  string
	Password2 string
}

// AzureRegistryUsage is one quota-usage entry returned by listUsages.
type AzureRegistryUsage struct {
	Name         string
	Limit        int64
	CurrentValue int64
	Unit         string
}

// AzureWebhookConfig is the create-or-update payload for a registry webhook.
type AzureWebhookConfig struct {
	Location      string
	Tags          map[string]string
	ServiceURI    string
	Actions       []string
	Scope         string
	Status        string // "enabled" / "disabled"
	CustomHeaders map[string]string
}

// AzureWebhook is a stored/returned registry webhook resource.
type AzureWebhook struct {
	Name              string
	RegistryName      string
	ResourceGroup     string
	Location          string
	Tags              map[string]string
	ServiceURI        string
	Actions           []string
	Scope             string
	Status            string
	CustomHeaders     map[string]string
	ProvisioningState string
}

// AzureReplicationConfig is the create-or-update payload for a geo-replication.
type AzureReplicationConfig struct {
	Location              string
	Tags                  map[string]string
	RegionEndpointEnabled bool
}

// AzureReplication is a stored/returned geo-replication resource.
type AzureReplication struct {
	Name                  string
	RegistryName          string
	ResourceGroup         string
	Location              string
	Tags                  map[string]string
	RegionEndpointEnabled bool
	ProvisioningState     string
	Status                string
}

// AzureRegistryManager is the Azure-specific ACR management-plane surface,
// keyed by (resourceGroup, name) to match ARM addressing and give idempotent
// createOrUpdate. An empty resource group on a list means subscription-wide.
type AzureRegistryManager interface {
	CreateOrUpdateRegistry(ctx context.Context, rg, name string, cfg AzureRegistryConfig) (*AzureRegistry, error)
	GetRegistry(ctx context.Context, rg, name string) (*AzureRegistry, error)
	DeleteRegistry(ctx context.Context, rg, name string) error
	ListRegistries(ctx context.Context, rg string) ([]AzureRegistry, error)

	ListRegistryCredentials(ctx context.Context, rg, name string) (*AzureRegistryCredentials, error)
	RegenerateRegistryCredential(ctx context.Context, rg, name, passwordName string) (*AzureRegistryCredentials, error)
	ListRegistryUsages(ctx context.Context, rg, name string) ([]AzureRegistryUsage, error)

	CreateOrUpdateWebhook(ctx context.Context, rg, registry, name string, cfg AzureWebhookConfig) (*AzureWebhook, error)
	GetWebhook(ctx context.Context, rg, registry, name string) (*AzureWebhook, error)
	DeleteWebhook(ctx context.Context, rg, registry, name string) error
	ListWebhooks(ctx context.Context, rg, registry string) ([]AzureWebhook, error)

	CreateOrUpdateReplication(
		ctx context.Context, rg, registry, name string, cfg AzureReplicationConfig,
	) (*AzureReplication, error)
	GetReplication(ctx context.Context, rg, registry, name string) (*AzureReplication, error)
	DeleteReplication(ctx context.Context, rg, registry, name string) error
	ListReplications(ctx context.Context, rg, registry string) ([]AzureReplication, error)
}

// AzureRepositoryWriter is the Azure-specific ACR data-plane surface for
// mutating a single tag or repository. DeleteTag removes one tag while leaving
// the underlying manifest and any other tags intact (unlike DeleteImage, which
// removes the whole manifest). The wire handler reaches it by type assertion.
type AzureRepositoryWriter interface {
	DeleteTag(ctx context.Context, repository, tag string) error
}
