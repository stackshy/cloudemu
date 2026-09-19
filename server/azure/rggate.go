package azure

import (
	"fmt"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/azure/resourcegroups"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// newResourceGroupGate builds the resource-group existence pre-dispatch hook.
// Real Azure rejects any operation scoped to a resource group that does not
// exist with 404 ResourceGroupNotFound, before it even looks at the resource
// type. This single chokepoint restores that behavior for every resource type
// at once — no per-handler edits — the same way newLockGate applies lock
// semantics centrally.
//
// It fires only for a resource operation INSIDE a resource group: the path
// parses to a non-empty ResourceGroup AND a non-empty Provider (i.e. there is a
// /providers/{ns}/... segment after the group). Resource-group-level operations
// themselves (create/get/delete the group, Provider == "") and
// subscription-scoped operations (ResourceGroup == "") are exempt, so an RG can
// always be created and the gate never blocks its own precondition.
func newResourceGroupGate(rg *resourcegroups.Handler) preDispatch {
	return func(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
		if !isControlPlane(r.URL.Path) {
			return r, true
		}

		rp, ok := azurearm.ParsePath(r.URL.Path)
		if !ok || rp.ResourceGroup == "" || rp.Provider == "" {
			return r, true
		}

		if rg.Exists(rp.Subscription, rp.ResourceGroup) {
			return r, true
		}

		azurearm.WriteError(w, http.StatusNotFound, "ResourceGroupNotFound",
			fmt.Sprintf("Resource group '%s' could not be found.", rp.ResourceGroup))

		return r, false
	}
}
