package functions

import (
	"encoding/json"
	"io"
	"net/http"

	azfunctions "github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// patchPlan serves PATCH .../serverfarms/{name} (armappservice
// PlansClient.Update). Only the fields present in the body change; the rest of
// the plan is kept. Like the sites PATCH it answers 200 synchronously with the
// updated plan, one of the two statuses (200/202) the SDK accepts. A PATCH
// against a missing plan, or the wrong resource group, is a 404.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func patchPlan(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store appServicePlanStore) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxControlBytes)

	var req patchServerFarmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	plan, err := store.PatchAppServicePlan(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName,
		toPlanPatch(&req))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toServerFarmResource(rp, plan))
}

// toPlanPatch maps the wire PATCH body onto the provider's partial update.
func toPlanPatch(req *patchServerFarmRequest) azfunctions.AppServicePlanPatch {
	patch := azfunctions.AppServicePlanPatch{Kind: req.Kind, Tags: req.Tags}

	if req.SKU != nil {
		patch.SKUName = req.SKU.Name
		patch.SKUTier = req.SKU.Tier
		patch.Capacity = req.SKU.Capacity
	}

	if req.Properties != nil {
		patch.Reserved = req.Properties.Reserved
		patch.PerSiteScaling = req.Properties.PerSiteScaling
		patch.ZoneRedundant = req.Properties.ZoneRedundant
		patch.MaximumElasticWorkerCount = req.Properties.MaximumElasticWorkerCount
	}

	return patch
}

func boolOr(p *bool) bool {
	return p != nil && *p
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}

	return *p
}
