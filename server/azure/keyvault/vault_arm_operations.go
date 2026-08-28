package keyvault

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// createOrUpdateVault handles PUT — Vaults.BeginCreateOrUpdate. The LRO
// completes inline: returning 200/201 with the resource body terminates the
// SDK's poller on the first response. A first create returns 201, a replace of
// an existing vault returns 200, matching real ARM.
func (h *VaultARMHandler) createOrUpdateVault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body vaultJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	existing, getErr := h.vaults.GetVault(r.Context(), rp.ResourceName)
	existed := getErr == nil

	// Vault names are globally unique and fixed to their creation resource
	// group: a PUT of an existing name under a different group must not silently
	// relocate it. Real Key Vault answers 409 Conflict (GET/DELETE below 404 on
	// the same scope mismatch).
	if existed && !existing.Scope.Matches(scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}) {
		azurearm.WriteError(w, http.StatusConflict, "Conflict",
			"vault "+rp.ResourceName+" already exists in another resource group")
		return
	}

	info, err := h.vaults.CreateOrUpdateVault(r.Context(), vaultConfigFromJSON(rp, &body))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, toVaultJSON(rp, info))
}

// getVault handles GET on a single resource — Vaults.Get. Vaults are keyed by
// name (globally unique), so the handler enforces the request's resource-group
// scope: a vault created in one group must not resolve under a different group
// in the URL (real ARM answers 404, since the id would contradict the path).
func (h *VaultARMHandler) getVault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.vaults.GetVault(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if !info.Scope.Matches(scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"vault "+rp.ResourceName+" not found in resource group "+rp.ResourceGroup)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toVaultJSON(rp, info))
}

// deleteVault handles DELETE — Vaults.Delete. Returning 200 with an empty body
// completes the SDK's poller on the first response.
func (h *VaultARMHandler) deleteVault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// Enforce the request's resource-group scope before deleting, so a vault is
	// never removed through a path that names a different group than it lives in.
	info, err := h.vaults.GetVault(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if !info.Scope.Matches(scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"vault "+rp.ResourceName+" not found in resource group "+rp.ResourceGroup)
		return
	}

	if derr := h.vaults.DeleteVault(r.Context(), rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// listVaults handles GET on the collection — Vaults.ListByResourceGroup /
// ListBySubscription. The filter carries the path's subscription and, for
// RG-level lists, its resource group; subscription-level lists leave the
// resource group empty so the filter spans the subscription's groups.
func (h *VaultARMHandler) listVaults(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	infos, err := h.vaults.ListVaults(r.Context(),
		scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]vaultJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toVaultJSON(rp, &infos[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, vaultListResult{Value: out})
}
