package acr

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// serveReplication routes .../registries/{registry}/replications[/{name}].
//
//nolint:dupl // webhook and replication sub-resource routers are intentionally typed; sharing via generics adds noise.
func (h *ARMHandler) serveReplication(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			armMethodNotAllowed(w)
			return
		}

		h.listReplications(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateReplication(w, r, rp)
	case http.MethodPatch:
		h.updateReplication(w, r, rp)
	case http.MethodGet:
		h.getReplication(w, r, rp)
	case http.MethodDelete:
		h.deleteReplication(w, r, rp)
	default:
		armMethodNotAllowed(w)
	}
}

// createOrUpdateReplication handles the ARM PUT (full create-or-replace).
func (h *ARMHandler) createOrUpdateReplication(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armReplication
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := crdriver.AzureReplicationConfig{
		Location:              body.Location,
		Tags:                  fromPtrTags(body.Tags),
		RegionEndpointEnabled: true,
	}

	if body.Properties != nil {
		cfg.RegionEndpointEnabled = body.Properties.RegionEndpointEnabled
	}

	rep, created, err := h.mgr.CreateOrUpdateReplication(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, createdStatus(created), toARMReplication(rep, rp.Subscription))
}

// updateReplication handles the ARM PATCH (partial update): only properties
// present in the request body are overwritten; the rest are preserved.
func (h *ARMHandler) updateReplication(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armReplication
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	var upd crdriver.AzureReplicationUpdate

	if body.Tags != nil {
		upd.Tags = fromPtrTags(body.Tags)
	}

	if body.Properties != nil {
		enabled := body.Properties.RegionEndpointEnabled
		upd.RegionEndpointEnabled = &enabled
	}

	rep, err := h.mgr.UpdateReplication(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, upd)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMReplication(rep, rp.Subscription))
}

func (h *ARMHandler) getReplication(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rep, err := h.mgr.GetReplication(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMReplication(rep, rp.Subscription))
}

func (h *ARMHandler) deleteReplication(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.mgr.DeleteReplication(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // webhook and replication sub-resource lists are intentionally typed; sharing via generics adds noise.
func (h *ARMHandler) listReplications(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	reps, err := h.mgr.ListReplications(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armReplication, 0, len(reps))
	for i := range reps {
		out = append(out, toARMReplication(&reps[i], rp.Subscription))
	}

	azurearm.WriteJSON(w, http.StatusOK, armRegistryList[armReplication]{Value: out})
}
