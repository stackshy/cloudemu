package keyvault

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/scope"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	subAccessPolicies   = "accessPolicies"
	accessPolicyType    = vaultProviderName + "/vaults/accessPolicies"
	typeDeletedVaults   = "deletedVaults"
	resourceLocations   = "locations"
	accessPolicyMaxPath = 3 // vaults/{v}/accessPolicies/{kind}
)

// accessPolicyJSON is the VaultAccessPolicyParameters body and response.
type accessPolicyJSON struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	Location   string `json:"location,omitempty"`
	Properties struct {
		AccessPolicies []vaultAccessPolicyJSON `json:"accessPolicies"`
	} `json:"properties"`
}

// isAccessPolicyPath reports whether rp is vaults/{v}/accessPolicies/{kind}.
func isAccessPolicyPath(rp *azurearm.ResourcePath) bool {
	return strings.EqualFold(rp.SubResource, subAccessPolicies) && rp.SubResourceName != "" &&
		!rp.IsExtension() && rp.Depth <= accessPolicyMaxPath
}

// serveAccessPolicy handles PUT vaults/{v}/accessPolicies/{add|replace|remove}
// (Vaults.UpdateAccessPolicy). Only the vault's access-policy list changes.
func (h *VaultARMHandler) serveAccessPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPut {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	kind := secretsdriver.KVAccessPolicyUpdateKind(strings.ToLower(rp.SubResourceName))
	switch kind {
	case secretsdriver.KVAccessPolicyAdd, secretsdriver.KVAccessPolicyReplace, secretsdriver.KVAccessPolicyRemove:
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest",
			"The access policy update kind '"+rp.SubResourceName+"' is invalid. Valid values are add, replace and remove.")
		return
	}

	var body accessPolicyJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	vault, err := h.vaults.GetVault(r.Context(), rp.ResourceName)
	if err != nil || !vault.Scope.Matches(scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"vault "+rp.ResourceName+" not found in resource group "+rp.ResourceGroup)
		return
	}

	list, err := h.vaults.UpdateVaultAccessPolicies(r.Context(), rp.ResourceName, kind,
		accessPoliciesFromJSON(body.Properties.AccessPolicies))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := accessPolicyJSON{
		ID:       toVaultJSON(rp, vault).ID + "/accessPolicies/" + string(kind),
		Name:     string(kind),
		Type:     accessPolicyType,
		Location: vault.Location,
	}
	out.Properties.AccessPolicies = accessPoliciesToJSON(list)

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// isDeletedVaultsPath reports whether rp is the deleted-vault surface:
// locations/{l}/deletedVaults[/{n}[/purge]] or the subscription deletedVaults list.
func isDeletedVaultsPath(rp *azurearm.ResourcePath) bool {
	return rp.ResourceType == typeDeletedVaults ||
		(rp.ResourceType == resourceLocations && rp.SubResource == typeDeletedVaults)
}

// serveDeletedVaults answers the deleted-vault surface. cloudemu does not
// soft-delete vaults, so there is never a deleted vault: lists are empty and
// a get or purge of one is 404. azurerm_key_vault create checks this first.
func serveDeletedVaults(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	isList := rp.ResourceType == typeDeletedVaults || rp.SubResourceName == ""
	if isList && r.Method == http.MethodGet {
		azurearm.WriteJSON(w, http.StatusOK, vaultListResult{Value: []vaultJSON{}})
		return
	}

	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
		"The deleted vault '"+rp.SubResourceName+"' was not found.")
}
