package keyvault

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// CreateOrUpdateVault creates a vault or replaces an existing one's
// properties/tags (ARM PUT semantics). Vault names are globally unique, so the
// store is keyed by name; the request's scope (subscription/resource group) is
// recorded on the stored record for scoped lists and the Get scope check.
func (m *Mock) CreateOrUpdateVault(_ context.Context, cfg driver.KVVaultConfig) (*driver.KVVaultInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "vault name is required")
	}

	info := &driver.KVVaultInfo{
		Name:       cfg.Name,
		Location:   cfg.Location,
		Scope:      cfg.Scope,
		Tags:       copyTags(cfg.Tags),
		Properties: copyVaultProperties(cfg.Properties),
	}

	if info.Properties.VaultURI == "" {
		info.Properties.VaultURI = "https://" + cfg.Name + ".vault.azure.net/"
	}

	m.armVaults.Set(cfg.Name, info)

	return cloneVaultInfo(info), nil
}

// GetVault returns the vault by name. Scope enforcement is left to the caller
// (the ARM handler checks the URL's resource group against the record).
func (m *Mock) GetVault(_ context.Context, name string) (*driver.KVVaultInfo, error) {
	info, ok := m.armVaults.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "vault %q not found", name)
	}

	return cloneVaultInfo(info), nil
}

// ListVaults returns the vaults visible under filter (a zero filter lists all).
func (m *Mock) ListVaults(_ context.Context, filter scope.Scope) ([]driver.KVVaultInfo, error) {
	all := m.armVaults.All()

	out := make([]driver.KVVaultInfo, 0, len(all))

	for _, info := range all {
		if !info.Scope.Matches(filter) {
			continue
		}

		out = append(out, *cloneVaultInfo(info))
	}

	return out, nil
}

// DeleteVault removes a vault by name.
func (m *Mock) DeleteVault(_ context.Context, name string) error {
	if !m.armVaults.Delete(name) {
		return errors.Newf(errors.NotFound, "vault %q not found", name)
	}

	return nil
}

// cloneVaultInfo deep-copies a stored vault record so callers never alias the
// store's maps/slices.
func cloneVaultInfo(in *driver.KVVaultInfo) *driver.KVVaultInfo {
	out := *in
	out.Tags = copyTags(in.Tags)
	out.Properties = copyVaultProperties(in.Properties)

	return &out
}

// copyVaultProperties deep-copies the mutable slices/pointers of a vault's
// properties so a stored record and a returned copy share no backing storage.
func copyVaultProperties(p driver.KVVaultProperties) driver.KVVaultProperties {
	out := p
	out.EnableRbacAuthorization = copyBool(p.EnableRbacAuthorization)
	out.EnabledForDeployment = copyBool(p.EnabledForDeployment)
	out.EnabledForDiskEncryption = copyBool(p.EnabledForDiskEncryption)
	out.EnabledForTemplateDeployment = copyBool(p.EnabledForTemplateDeployment)
	out.EnableSoftDelete = copyBool(p.EnableSoftDelete)
	out.EnablePurgeProtection = copyBool(p.EnablePurgeProtection)
	out.AccessPolicies = copyAccessPolicies(p.AccessPolicies)

	return out
}

func copyBool(in *bool) *bool {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyAccessPolicies(in []driver.KVAccessPolicy) []driver.KVAccessPolicy {
	if in == nil {
		return nil
	}

	out := make([]driver.KVAccessPolicy, len(in))
	for i := range in {
		out[i] = driver.KVAccessPolicy{
			TenantID: in[i].TenantID,
			ObjectID: in[i].ObjectID,
			Permissions: driver.KVAccessPermissions{
				Keys:         copyStrings(in[i].Permissions.Keys),
				Secrets:      copyStrings(in[i].Permissions.Secrets),
				Certificates: copyStrings(in[i].Permissions.Certificates),
				Storage:      copyStrings(in[i].Permissions.Storage),
			},
		}
	}

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}
