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

// AzureRegistryConfig is the full create-or-replace payload for an ACR registry
// (ARM PUT).
type AzureRegistryConfig struct {
	Location         string
	Tags             map[string]string
	SKUName          string // Basic / Standard / Premium (defaults to Standard)
	AdminUserEnabled bool
	// IdentityType is the managed-identity type submitted in the ARM identity
	// block: "SystemAssigned", "UserAssigned", "SystemAssigned, UserAssigned"
	// or "None"/"" for no identity.
	IdentityType string
	// UserAssignedIdentities holds the user-assigned managed-identity resource
	// IDs submitted in the identity block's userAssignedIdentities map. Only
	// meaningful when IdentityType includes "UserAssigned".
	UserAssignedIdentities []string
}

// AzureRegistryUpdate is the partial-update payload for an ACR registry (ARM
// PATCH, RegistryUpdateParameters). Every field is optional: a nil pointer (or
// nil Tags map) leaves the corresponding attribute untouched on the existing
// registry.
type AzureRegistryUpdate struct {
	Tags             map[string]string // nil = unchanged; non-nil replaces the tag set
	SKUName          *string
	AdminUserEnabled *bool
	IdentityType     *string
	// UserAssignedIdentities is the user-assigned identity resource-ID set to
	// apply when IdentityType is updated to include "UserAssigned"; nil leaves
	// the stored identity untouched (only consulted when IdentityType is set).
	UserAssignedIdentities []string
}

// AzureUserAssignedIdentity is the synthesized principal/client pair Azure
// returns for each user-assigned managed identity attached to a resource.
type AzureUserAssignedIdentity struct {
	PrincipalID string
	ClientID    string
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
	// UserAssignedIdentities maps each attached user-assigned identity resource
	// ID to its synthesized principal/client pair; populated only when
	// IdentityType includes "UserAssigned".
	UserAssignedIdentities map[string]AzureUserAssignedIdentity
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

// AzureWebhookConfig is the full create-or-replace payload for a registry
// webhook (ARM PUT).
type AzureWebhookConfig struct {
	Location      string
	Tags          map[string]string
	ServiceURI    string
	Actions       []string
	Scope         string
	Status        string // "enabled" / "disabled"
	CustomHeaders map[string]string
}

// AzureWebhookUpdate is the partial-update payload for a registry webhook (ARM
// PATCH, WebhookUpdateParameters). Every field is optional: a nil pointer (or
// nil slice/map) leaves the corresponding attribute untouched.
type AzureWebhookUpdate struct {
	Tags          map[string]string // nil = unchanged
	ServiceURI    *string
	Actions       []string // nil = unchanged
	Scope         *string
	Status        *string
	CustomHeaders map[string]string // nil = unchanged
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

// AzureReplicationConfig is the full create-or-replace payload for a
// geo-replication (ARM PUT).
type AzureReplicationConfig struct {
	Location              string
	Tags                  map[string]string
	RegionEndpointEnabled bool
}

// AzureReplicationUpdate is the partial-update payload for a geo-replication
// (ARM PATCH, ReplicationUpdateParameters). Every field is optional: a nil
// pointer (or nil Tags map) leaves the corresponding attribute untouched.
type AzureReplicationUpdate struct {
	Tags                  map[string]string // nil = unchanged
	RegionEndpointEnabled *bool
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
	// CreateOrUpdateRegistry is the ARM PUT (full create-or-replace). It reports
	// whether the registry was newly created so the wire layer can return 201
	// Created on first create and 200 OK on replace.
	CreateOrUpdateRegistry(
		ctx context.Context, rg, name string, cfg AzureRegistryConfig,
	) (reg *AzureRegistry, created bool, err error)
	// UpdateRegistry is the ARM PATCH (partial update). It merges upd onto the
	// existing registry, returning NotFound when the registry does not exist.
	UpdateRegistry(ctx context.Context, rg, name string, upd AzureRegistryUpdate) (*AzureRegistry, error)
	GetRegistry(ctx context.Context, rg, name string) (*AzureRegistry, error)
	DeleteRegistry(ctx context.Context, rg, name string) error
	ListRegistries(ctx context.Context, rg string) ([]AzureRegistry, error)

	ListRegistryCredentials(ctx context.Context, rg, name string) (*AzureRegistryCredentials, error)
	RegenerateRegistryCredential(ctx context.Context, rg, name, passwordName string) (*AzureRegistryCredentials, error)
	ListRegistryUsages(ctx context.Context, rg, name string) ([]AzureRegistryUsage, error)

	CreateOrUpdateWebhook(
		ctx context.Context, rg, registry, name string, cfg AzureWebhookConfig,
	) (wh *AzureWebhook, created bool, err error)
	UpdateWebhook(ctx context.Context, rg, registry, name string, upd AzureWebhookUpdate) (*AzureWebhook, error)
	GetWebhook(ctx context.Context, rg, registry, name string) (*AzureWebhook, error)
	DeleteWebhook(ctx context.Context, rg, registry, name string) error
	ListWebhooks(ctx context.Context, rg, registry string) ([]AzureWebhook, error)

	CreateOrUpdateReplication(
		ctx context.Context, rg, registry, name string, cfg AzureReplicationConfig,
	) (rep *AzureReplication, created bool, err error)
	UpdateReplication(
		ctx context.Context, rg, registry, name string, upd AzureReplicationUpdate,
	) (*AzureReplication, error)
	GetReplication(ctx context.Context, rg, registry, name string) (*AzureReplication, error)
	DeleteReplication(ctx context.Context, rg, registry, name string) error
	ListReplications(ctx context.Context, rg, registry string) ([]AzureReplication, error)
}

// AzureChangeableAttributes mirrors ACR's Repository/Tag/ManifestWriteableProperties
// (the data-plane "changeableAttributes" lock bits). Each field is a pointer so
// a partial PATCH (UpdateRepositoryProperties / UpdateTagProperties /
// UpdateManifestProperties) can leave an attribute unspecified: nil means
// "leave unchanged" on an update. A Get call always returns every field
// resolved (never nil).
type AzureChangeableAttributes struct {
	DeleteEnabled *bool
	WriteEnabled  *bool
	ListEnabled   *bool
	ReadEnabled   *bool
}

// AzureRepositoryWriter is the Azure-specific ACR data-plane surface for
// mutating a single tag, manifest, or repository. DeleteTag removes one tag
// while leaving the underlying manifest and any other tags intact (unlike
// DeleteImage, which removes the whole manifest).
//
// The Get/Update*Attributes methods back ACR's changeableAttributes lock: a
// repository, tag, or manifest with deleteEnabled=false rejects deletion,
// writeEnabled=false rejects a push/re-tag that would overwrite it, and
// listEnabled=false hides it from catalog/tag/manifest listings while leaving
// it directly readable. The wire handler reaches all of this by type
// assertion, mirroring the AzureNetworkInterfaces pattern in the networking
// driver.
type AzureRepositoryWriter interface {
	DeleteTag(ctx context.Context, repository, tag string) error

	GetRepositoryAttributes(ctx context.Context, repository string) (AzureChangeableAttributes, error)
	UpdateRepositoryAttributes(
		ctx context.Context, repository string, attrs AzureChangeableAttributes,
	) (AzureChangeableAttributes, error)

	GetTagAttributes(ctx context.Context, repository, tag string) (AzureChangeableAttributes, error)
	UpdateTagAttributes(
		ctx context.Context, repository, tag string, attrs AzureChangeableAttributes,
	) (AzureChangeableAttributes, error)

	GetManifestAttributes(ctx context.Context, repository, digest string) (AzureChangeableAttributes, error)
	UpdateManifestAttributes(
		ctx context.Context, repository, digest string, attrs AzureChangeableAttributes,
	) (AzureChangeableAttributes, error)
}
