package driver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// KVVaultSKU is the ARM Key Vault SKU. Family is always "A"; Name is the tier
// ("standard" or "premium").
type KVVaultSKU struct {
	Family string
	Name   string
}

// KVAccessPermissions are the object-level permissions granted to one access
// policy entry (the classic, non-RBAC authorization model).
type KVAccessPermissions struct {
	Keys         []string
	Secrets      []string
	Certificates []string
	Storage      []string
}

// KVAccessPolicy is one entry of a vault's classic access-policy list, granting
// a principal (ObjectID under TenantID) the listed object permissions.
type KVAccessPolicy struct {
	TenantID    string
	ObjectID    string
	Permissions KVAccessPermissions
}

// KVVaultProperties are the ARM vault properties (Microsoft.KeyVault/vaults).
// Pointer bools distinguish "unset" (nil, omitted from the response) from an
// explicit false, matching how the SDK round-trips the flags.
type KVVaultProperties struct {
	TenantID                     string
	SKU                          KVVaultSKU
	AccessPolicies               []KVAccessPolicy
	EnableRbacAuthorization      *bool
	EnabledForDeployment         *bool
	EnabledForDiskEncryption     *bool
	EnabledForTemplateDeployment *bool
	EnableSoftDelete             *bool
	SoftDeleteRetentionInDays    int
	EnablePurgeProtection        *bool
	PublicNetworkAccess          string
	// VaultURI is the vault's data-plane endpoint. Left empty on input; the
	// provider derives a stable {name}.vault.azure.net value.
	VaultURI string
}

// KVVaultConfig is the CreateOrUpdateVault input. Scope carries the request's
// subscription and resource group.
type KVVaultConfig struct {
	Name       string
	Location   string
	Scope      scope.Scope
	Tags       map[string]string
	Properties KVVaultProperties
}

// KVVaultInfo is one stored ARM vault.
type KVVaultInfo struct {
	Name       string
	Location   string
	Scope      scope.Scope
	Tags       map[string]string
	Properties KVVaultProperties
}

// KeyVaultVaults is the Azure Key Vault control-plane (ARM) surface —
// Microsoft.KeyVault/vaults — kept off the shared Secrets interface as a
// type-asserted optional interface, so only the Azure provider models the vault
// resource-manager lifecycle. It is distinct from the data-plane
// KeyVaultSecrets/KeyVaultKeys/KeyVaultCertificates surfaces, which manage the
// objects stored inside a vault rather than the vault resource itself.
type KeyVaultVaults interface {
	// CreateOrUpdateVault creates a vault, or replaces the properties/tags of an
	// existing one (ARM PUT semantics). Vault names are globally unique.
	CreateOrUpdateVault(ctx context.Context, cfg KVVaultConfig) (*KVVaultInfo, error)
	// GetVault returns the vault by name, regardless of scope. Scope enforcement
	// (the URL's resource group must match) is the caller's responsibility.
	GetVault(ctx context.Context, name string) (*KVVaultInfo, error)
	// ListVaults returns the vaults visible under the given scope filter (a zero
	// filter lists every vault).
	ListVaults(ctx context.Context, filter scope.Scope) ([]KVVaultInfo, error)
	// DeleteVault removes a vault by name.
	DeleteVault(ctx context.Context, name string) error
}
