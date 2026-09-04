package keyvault

import (
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/scope"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	vaultProviderName = "Microsoft.KeyVault"
	vaultResourceType = "vaults"
	vaultARMType      = "Microsoft.KeyVault/vaults"
	// provisioningStateSucceeded is the terminal LRO state. The mock applies
	// mutations synchronously, so every response is already Succeeded, which
	// terminates the SDK's poller on the first response.
	provisioningStateSucceeded = "Succeeded"
	defaultVaultLocation       = "eastus"
	defaultSKUFamily           = "A"
	defaultSKUName             = "standard"
)

// vaultSKUJSON mirrors the armkeyvault SKU: Family is always "A", Name is the
// tier ("standard" or "premium").
type vaultSKUJSON struct {
	Family string `json:"family,omitempty"`
	Name   string `json:"name,omitempty"`
}

// vaultPermissionsJSON mirrors armkeyvault Permissions.
type vaultPermissionsJSON struct {
	Keys         []string `json:"keys,omitempty"`
	Secrets      []string `json:"secrets,omitempty"`
	Certificates []string `json:"certificates,omitempty"`
	Storage      []string `json:"storage,omitempty"`
}

// vaultAccessPolicyJSON mirrors armkeyvault AccessPolicyEntry.
type vaultAccessPolicyJSON struct {
	TenantID    string               `json:"tenantId,omitempty"`
	ObjectID    string               `json:"objectId,omitempty"`
	Permissions vaultPermissionsJSON `json:"permissions"`
}

// vaultPropertiesJSON mirrors the subset of armkeyvault VaultProperties the
// control-plane mock records. Pointer bools omit an unset flag rather than
// emitting a spurious false.
type vaultPropertiesJSON struct {
	TenantID                     string                  `json:"tenantId,omitempty"`
	SKU                          *vaultSKUJSON           `json:"sku,omitempty"`
	AccessPolicies               []vaultAccessPolicyJSON `json:"accessPolicies,omitempty"`
	EnableRbacAuthorization      *bool                   `json:"enableRbacAuthorization,omitempty"`
	EnabledForDeployment         *bool                   `json:"enabledForDeployment,omitempty"`
	EnabledForDiskEncryption     *bool                   `json:"enabledForDiskEncryption,omitempty"`
	EnabledForTemplateDeployment *bool                   `json:"enabledForTemplateDeployment,omitempty"`
	EnableSoftDelete             *bool                   `json:"enableSoftDelete,omitempty"`
	SoftDeleteRetentionInDays    int                     `json:"softDeleteRetentionInDays,omitempty"`
	EnablePurgeProtection        *bool                   `json:"enablePurgeProtection,omitempty"`
	PublicNetworkAccess          string                  `json:"publicNetworkAccess,omitempty"`
	VaultURI                     string                  `json:"vaultUri,omitempty"`
	ProvisioningState            string                  `json:"provisioningState,omitempty"`
}

// vaultJSON mirrors the armkeyvault Vault resource (CreateOrUpdateParameters on
// input, Vault on output).
type vaultJSON struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name,omitempty"`
	Type       string               `json:"type,omitempty"`
	Location   string               `json:"location,omitempty"`
	Tags       map[string]string    `json:"tags,omitempty"`
	Properties *vaultPropertiesJSON `json:"properties,omitempty"`
}

type vaultListResult struct {
	Value []vaultJSON `json:"value"`
}

// vaultConfigFromJSON builds the driver config from a decoded request body,
// stamping the request path's scope and resource name.
func vaultConfigFromJSON(rp *azurearm.ResourcePath, body *vaultJSON) secretsdriver.KVVaultConfig {
	cfg := secretsdriver.KVVaultConfig{
		Name:     rp.ResourceName,
		Location: body.Location,
		Scope:    scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
		Tags:     body.Tags,
	}

	if body.Properties != nil {
		cfg.Properties = vaultPropertiesFromJSON(body.Properties)
	}

	return cfg
}

// vaultPropertiesFromJSON maps the request properties onto the driver shape.
func vaultPropertiesFromJSON(p *vaultPropertiesJSON) secretsdriver.KVVaultProperties {
	out := secretsdriver.KVVaultProperties{
		TenantID:                     p.TenantID,
		EnableRbacAuthorization:      p.EnableRbacAuthorization,
		EnabledForDeployment:         p.EnabledForDeployment,
		EnabledForDiskEncryption:     p.EnabledForDiskEncryption,
		EnabledForTemplateDeployment: p.EnabledForTemplateDeployment,
		EnableSoftDelete:             p.EnableSoftDelete,
		SoftDeleteRetentionInDays:    p.SoftDeleteRetentionInDays,
		EnablePurgeProtection:        p.EnablePurgeProtection,
		PublicNetworkAccess:          p.PublicNetworkAccess,
		VaultURI:                     p.VaultURI,
	}

	if p.SKU != nil {
		out.SKU = secretsdriver.KVVaultSKU{Family: p.SKU.Family, Name: p.SKU.Name}
	}

	for i := range p.AccessPolicies {
		ap := &p.AccessPolicies[i]
		out.AccessPolicies = append(out.AccessPolicies, secretsdriver.KVAccessPolicy{
			TenantID: ap.TenantID,
			ObjectID: ap.ObjectID,
			Permissions: secretsdriver.KVAccessPermissions{
				Keys:         ap.Permissions.Keys,
				Secrets:      ap.Permissions.Secrets,
				Certificates: ap.Permissions.Certificates,
				Storage:      ap.Permissions.Storage,
			},
		})
	}

	return out
}

// mergeVaultProperties applies a PATCH request's properties onto a stored
// vault's properties: only fields the request body actually carries replace
// the existing value (a nil pointer, empty SKU/access-policy list, or empty
// string leaves the stored field untouched), matching real ARM PATCH merge
// semantics as opposed to PUT's full replace.
func mergeVaultProperties(existing *secretsdriver.KVVaultProperties, p *vaultPropertiesJSON) secretsdriver.KVVaultProperties {
	out := *existing

	if p.TenantID != "" {
		out.TenantID = p.TenantID
	}

	if p.SKU != nil {
		out.SKU = secretsdriver.KVVaultSKU{Family: p.SKU.Family, Name: p.SKU.Name}
	}

	if p.AccessPolicies != nil {
		out.AccessPolicies = accessPoliciesFromJSON(p.AccessPolicies)
	}

	if p.SoftDeleteRetentionInDays != 0 {
		out.SoftDeleteRetentionInDays = p.SoftDeleteRetentionInDays
	}

	if p.PublicNetworkAccess != "" {
		out.PublicNetworkAccess = p.PublicNetworkAccess
	}

	mergeVaultFlags(&out, p)

	return out
}

// accessPoliciesFromJSON converts a PATCH/PUT request's access-policy list to
// the driver shape.
func accessPoliciesFromJSON(in []vaultAccessPolicyJSON) []secretsdriver.KVAccessPolicy {
	out := make([]secretsdriver.KVAccessPolicy, 0, len(in))

	for i := range in {
		ap := &in[i]
		out = append(out, secretsdriver.KVAccessPolicy{
			TenantID: ap.TenantID,
			ObjectID: ap.ObjectID,
			Permissions: secretsdriver.KVAccessPermissions{
				Keys:         ap.Permissions.Keys,
				Secrets:      ap.Permissions.Secrets,
				Certificates: ap.Permissions.Certificates,
				Storage:      ap.Permissions.Storage,
			},
		})
	}

	return out
}

// mergeVaultFlags applies the pointer-bool feature flags of a PATCH request
// onto out: a nil pointer in p leaves the corresponding stored flag untouched.
func mergeVaultFlags(out *secretsdriver.KVVaultProperties, p *vaultPropertiesJSON) {
	if p.EnableRbacAuthorization != nil {
		out.EnableRbacAuthorization = p.EnableRbacAuthorization
	}

	if p.EnabledForDeployment != nil {
		out.EnabledForDeployment = p.EnabledForDeployment
	}

	if p.EnabledForDiskEncryption != nil {
		out.EnabledForDiskEncryption = p.EnabledForDiskEncryption
	}

	if p.EnabledForTemplateDeployment != nil {
		out.EnabledForTemplateDeployment = p.EnabledForTemplateDeployment
	}

	if p.EnableSoftDelete != nil {
		out.EnableSoftDelete = p.EnableSoftDelete
	}

	if p.EnablePurgeProtection != nil {
		out.EnablePurgeProtection = p.EnablePurgeProtection
	}
}

// toVaultJSON converts a stored driver record into its ARM element. Resources
// without a recorded scope fall back to the request path's scope.
func toVaultJSON(rp *azurearm.ResourcePath, info *secretsdriver.KVVaultInfo) vaultJSON {
	sub := info.Scope.Subscription
	if sub == "" {
		sub = rp.Subscription
	}

	rg := info.Scope.ResourceGroup
	if rg == "" {
		rg = rp.ResourceGroup
	}

	location := info.Location
	if location == "" {
		location = defaultVaultLocation
	}

	return vaultJSON{
		ID:         azurearm.BuildResourceID(sub, rg, vaultProviderName, vaultResourceType, info.Name),
		Name:       info.Name,
		Type:       vaultARMType,
		Location:   location,
		Tags:       info.Tags,
		Properties: toVaultPropertiesJSON(&info.Properties),
	}
}

// toVaultPropertiesJSON renders the stored properties, defaulting the SKU when
// the record carries none (e.g. a minimal create body).
func toVaultPropertiesJSON(p *secretsdriver.KVVaultProperties) *vaultPropertiesJSON {
	sku := &vaultSKUJSON{Family: p.SKU.Family, Name: p.SKU.Name}
	if sku.Family == "" {
		sku.Family = defaultSKUFamily
	}

	if sku.Name == "" {
		sku.Name = defaultSKUName
	}

	out := &vaultPropertiesJSON{
		TenantID:                     p.TenantID,
		SKU:                          sku,
		EnableRbacAuthorization:      p.EnableRbacAuthorization,
		EnabledForDeployment:         p.EnabledForDeployment,
		EnabledForDiskEncryption:     p.EnabledForDiskEncryption,
		EnabledForTemplateDeployment: p.EnabledForTemplateDeployment,
		EnableSoftDelete:             p.EnableSoftDelete,
		SoftDeleteRetentionInDays:    p.SoftDeleteRetentionInDays,
		EnablePurgeProtection:        p.EnablePurgeProtection,
		PublicNetworkAccess:          p.PublicNetworkAccess,
		VaultURI:                     p.VaultURI,
		ProvisioningState:            provisioningStateSucceeded,
	}

	for i := range p.AccessPolicies {
		ap := &p.AccessPolicies[i]
		out.AccessPolicies = append(out.AccessPolicies, vaultAccessPolicyJSON{
			TenantID: ap.TenantID,
			ObjectID: ap.ObjectID,
			Permissions: vaultPermissionsJSON{
				Keys:         ap.Permissions.Keys,
				Secrets:      ap.Permissions.Secrets,
				Certificates: ap.Permissions.Certificates,
				Storage:      ap.Permissions.Storage,
			},
		})
	}

	return out
}
