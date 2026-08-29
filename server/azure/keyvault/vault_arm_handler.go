// This file implements the Key Vault control-plane (ARM) resource
// Microsoft.KeyVault/vaults as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault
// Vaults clients configured with a custom endpoint hit this handler the same
// way they hit management.azure.com, driving the KeyVaultVaults control plane
// (CreateOrUpdate/Get/List/Delete).
//
// It is disjoint from the Key Vault data-plane surfaces (/secrets, /keys,
// /certificates) served by the other handlers in this package: those manage the
// objects inside a vault, whereas this manages the vault resource itself.
//
// Coverage:
//
//	PUT    .../providers/Microsoft.KeyVault/vaults/{name}   — Vaults.BeginCreateOrUpdate (LRO, completes inline)
//	GET    .../providers/Microsoft.KeyVault/vaults/{name}   — Vaults.Get
//	DELETE .../providers/Microsoft.KeyVault/vaults/{name}   — Vaults.Delete
//	GET    .../resourceGroups/{rg}/providers/Microsoft.KeyVault/vaults — Vaults.ListByResourceGroup
//	GET    .../subscriptions/{sub}/providers/Microsoft.KeyVault/vaults  — Vaults.ListBySubscription
package keyvault

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// VaultARMHandler serves Microsoft.KeyVault/vaults ARM requests against a
// KeyVaultVaults control-plane backend.
type VaultARMHandler struct {
	vaults secretsdriver.KeyVaultVaults
}

// NewVaultARM returns a Key Vault ARM (control-plane) handler backed by v.
func NewVaultARM(v secretsdriver.KeyVaultVaults) *VaultARMHandler {
	return &VaultARMHandler{vaults: v}
}

// Matches claims ARM URLs targeting Microsoft.KeyVault/vaults. The provider name
// is unique among Azure handlers, so registration order is unconstrained; it
// registers before the permissive BlobStorage fallback.
func (*VaultARMHandler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == vaultProviderName && rp.ResourceType == vaultResourceType
}

// ServeHTTP routes on the parsed path shape and method.
func (h *VaultARMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Collection list: no vault name (subscription- or RG-scoped list).
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.listVaults(w, r, &rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateVault(w, r, &rp)
	case http.MethodGet:
		h.getVault(w, r, &rp)
	case http.MethodDelete:
		h.deleteVault(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}
