package storageaccount

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	accountMigrationsKind = "accountMigrations"
	// accountMigrationName is the only migration name the REST spec allows.
	accountMigrationName = "default"
	accountMigrationType = providerName + "/" + resourceType + "/" + accountMigrationsKind
)

// accountMigrationJSON is the StorageAccountMigration resource.
type accountMigrationJSON struct {
	ID         string                    `json:"id"`
	Name       string                    `json:"name"`
	Type       string                    `json:"type"`
	Properties accountMigrationPropsJSON `json:"properties"`
}

type accountMigrationList struct {
	Value []accountMigrationJSON `json:"value"`
}

type accountMigrationPropsJSON struct {
	TargetSkuName string `json:"targetSkuName"`
}

// serveAccountMigrations serves the read side of the customer initiated
// migration resource:
//
//	GET …/accountMigrations          : {"value":[default]}
//	GET …/accountMigrations/default  : StorageAccounts.GetCustomerInitiatedMigration
//
// cloudemu never runs a redundancy migration, so the singleton reports no
// migrationStatus and the account's current SKU as its target. azurerm 5.x
// reads it on every storage account refresh and accepts only 200. Starting a
// migration is the startAccountMigration action, which stays 501.
func (h *Handler) serveAccountMigrations(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, rp, managementPolicyDepth) {
		return
	}

	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	if _, ok := h.lookup(w, r, rp); !ok {
		return
	}

	body := h.accountMigrationBody(r, rp)

	switch {
	case rp.SubResourceName == "":
		azurearm.WriteJSON(w, http.StatusOK, accountMigrationList{Value: []accountMigrationJSON{body}})
	case strings.EqualFold(rp.SubResourceName, accountMigrationName):
		azurearm.WriteJSON(w, http.StatusOK, body)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"The Resource '"+accountMigrationType+"/"+rp.SubResourceName+"' under resource group '"+
				rp.ResourceGroup+"' was not found.")
	}
}

func (h *Handler) accountMigrationBody(r *http.Request, rp *azurearm.ResourcePath) accountMigrationJSON {
	// toARMAccount always reports a SKU, the default one when none is stored.
	sku := h.toARMAccount(r.Context(), rp).SKU.Name

	return accountMigrationJSON{
		ID:         childID(rp, accountMigrationsKind, accountMigrationName),
		Name:       accountMigrationName,
		Type:       accountMigrationType,
		Properties: accountMigrationPropsJSON{TargetSkuName: sku},
	}
}
