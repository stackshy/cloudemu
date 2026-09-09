// Package driver defines the storage contract for Azure Data Factory
// (Microsoft.DataFactory/factories, API version 2018-06-01).
//
// A factory is an Azure-only ARM resource with no cross-cloud equivalent, so —
// like Azure Firewall and the Databricks access connector — the Azure provider
// stores the ARM body natively and exposes it through this dedicated,
// single-provider interface. AWS and GCP have no counterpart.
//
// The pieces the server-wide unmodeled-property echo cannot preserve are modeled
// explicitly here:
//
//   - identity is TOP-LEVEL on a factory (the echo overlay only reaches the
//     nested properties object), and its principalId/tenantId are computed GUIDs
//     when the identity includes SystemAssigned — synthesized once and returned
//     verbatim on every read so Terraform's computed identity never drifts.
//   - publicNetworkAccess is an explicit enum: the echo overlay swallows an
//     explicit zero, so public_network_enabled=false (→ Disabled) would be lost.
//   - provisioningState (Succeeded), createTime (ISO8601, stable across reads)
//     and version (2018-06-01) are computed read-only outputs the echo cannot
//     synthesize.
//
// globalParameters is modeled as a typed map. Every other property
// (repoConfiguration, purviewConfiguration, encryption, and any future key) is
// preserved verbatim in OtherProps so deferred sub-surfaces stay round-trip-safe.
package driver

import "context"

// PublicNetworkAccess enum values.
const (
	PublicNetworkAccessEnabled  = "Enabled"
	PublicNetworkAccessDisabled = "Disabled"
)

// StateSucceeded is the terminal provisioning state a factory reports; the ARM
// SDK poller and Terraform wait for it.
const StateSucceeded = "Succeeded"

// Version is the factory format version every factory reports (the 2018-06-01
// control-plane contract).
const Version = "2018-06-01"

// ManagedIdentity models the TOP-LEVEL FactoryIdentity managed service identity.
type ManagedIdentity struct {
	// Type is one of "None", "SystemAssigned", "UserAssigned",
	// "SystemAssigned,UserAssigned".
	Type string
	// UserAssigned holds the user-assigned identity resource IDs (keys of the
	// ARM userAssignedIdentities map).
	UserAssigned []string
	// PrincipalID/TenantID are synthesized (computed) for a system-assigned
	// identity and stable per resource.
	PrincipalID string
	TenantID    string
}

// GlobalParameterSpec is one entry of properties.globalParameters. Value is any
// JSON scalar or composite (the type enum is Object/String/Int/Float/Bool/Array).
type GlobalParameterSpec struct {
	Type  string
	Value any
}

// Factory is the natively-stored Microsoft.DataFactory/factories resource.
// Identity is top-level; ProvisioningState, CreateTime and Version are computed
// read-only outputs; PublicNetworkAccess is an explicit enum; GlobalParameters is
// modeled; OtherProps preserves every other property under "properties" verbatim.
type Factory struct {
	ID                  string
	Name                string
	Subscription        string
	ResourceGroup       string
	Location            string
	Tags                map[string]string
	Identity            *ManagedIdentity
	ProvisioningState   string
	CreateTime          string
	Version             string
	PublicNetworkAccess string
	GlobalParameters    map[string]GlobalParameterSpec
	OtherProps          map[string]any
	ETag                string
}

// FactoryConfig is the createOrUpdate (PUT) input for a factory.
type FactoryConfig struct {
	Name                string
	Subscription        string
	ResourceGroup       string
	Location            string
	Tags                map[string]string
	Identity            *ManagedIdentity
	PublicNetworkAccess string
	GlobalParameters    map[string]GlobalParameterSpec
	OtherProps          map[string]any
}

// Factories is the Azure-only Data Factory store, keyed by (resourceGroup, name)
// to match ARM addressing. CreateOrUpdate is a full replace; Update (PATCH) is a
// tags+identity replace. An empty resourceGroup is not used on List — the
// interface exposes both a by-resource-group and a subscription-wide listing.
type Factories interface {
	// CreateOrUpdateFactory stores the factory as a full replace and reports
	// whether it did not previously exist (created==true → HTTP 201, else 200).
	// CreateTime is set once at first create and preserved across later updates;
	// ProvisioningState/Version and the identity principal/tenant GUIDs are
	// computed. The returned value is a defensive copy.
	CreateOrUpdateFactory(ctx context.Context, cfg FactoryConfig) (stored *Factory, created bool, err error)
	// GetFactory returns the factory identified by (resourceGroup, name), or
	// NotFound. Returned identity principalId/tenantId and createTime are stable
	// across repeated reads.
	GetFactory(ctx context.Context, resourceGroup, name string) (*Factory, error)
	// UpdateFactory applies a PATCH (Factories_Update): it REPLACES tags and
	// identity and leaves every other field untouched. A nil tags/identity leaves
	// that field unchanged.
	UpdateFactory(
		ctx context.Context, resourceGroup, name string, tags map[string]string, identity *ManagedIdentity,
	) (*Factory, error)
	// DeleteFactory removes the factory, returning NotFound if it does not exist.
	DeleteFactory(ctx context.Context, resourceGroup, name string) error
	// ListFactoriesByResourceGroup returns the factories in resourceGroup, ordered
	// by ID.
	ListFactoriesByResourceGroup(ctx context.Context, resourceGroup string) ([]Factory, error)
	// ListFactories returns every factory in the subscription, ordered by ID.
	ListFactories(ctx context.Context) ([]Factory, error)
}
