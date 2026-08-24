package acr

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// serveWebhook routes .../registries/{registry}/webhooks[/{name}].
//
//nolint:dupl // webhook and replication sub-resource routers are intentionally typed; sharing via generics adds noise.
func (h *ARMHandler) serveWebhook(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			armMethodNotAllowed(w)
			return
		}

		h.listWebhooks(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateWebhook(w, r, rp)
	case http.MethodPatch:
		h.updateWebhook(w, r, rp)
	case http.MethodGet:
		h.getWebhook(w, r, rp)
	case http.MethodDelete:
		h.deleteWebhook(w, r, rp)
	default:
		armMethodNotAllowed(w)
	}
}

// createOrUpdateWebhook handles the ARM PUT (full create-or-replace).
func (h *ARMHandler) createOrUpdateWebhook(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armWebhook
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := crdriver.AzureWebhookConfig{
		Location: body.Location,
		Tags:     fromPtrTags(body.Tags),
	}

	if body.Properties != nil {
		cfg.ServiceURI = body.Properties.ServiceURI
		cfg.Actions = body.Properties.Actions
		cfg.Scope = body.Properties.Scope
		cfg.Status = body.Properties.Status
		cfg.CustomHeaders = fromPtrTags(body.Properties.CustomHeaders)
	}

	wh, created, err := h.mgr.CreateOrUpdateWebhook(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, createdStatus(created), toARMWebhook(wh, rp.Subscription))
}

// updateWebhook handles the ARM PATCH (partial update): only properties present
// in the request body are overwritten; the rest are preserved.
func (h *ARMHandler) updateWebhook(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armWebhook
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	upd := crdriver.AzureWebhookUpdate{}

	if body.Tags != nil {
		upd.Tags = fromPtrTags(body.Tags)
	}

	applyWebhookProps(body.Properties, &upd)

	wh, err := h.mgr.UpdateWebhook(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, upd)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMWebhook(wh, rp.Subscription))
}

// applyWebhookProps copies the properties present in a PATCH body onto upd,
// leaving absent fields nil so the provider preserves the stored values.
func applyWebhookProps(p *armWebhookProps, upd *crdriver.AzureWebhookUpdate) {
	if p == nil {
		return
	}

	if p.ServiceURI != "" {
		uri := p.ServiceURI
		upd.ServiceURI = &uri
	}

	if p.Actions != nil {
		upd.Actions = p.Actions
	}

	if p.Scope != "" {
		scope := p.Scope
		upd.Scope = &scope
	}

	if p.Status != "" {
		status := p.Status
		upd.Status = &status
	}

	if p.CustomHeaders != nil {
		upd.CustomHeaders = fromPtrTags(p.CustomHeaders)
	}
}

func (h *ARMHandler) getWebhook(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	wh, err := h.mgr.GetWebhook(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMWebhook(wh, rp.Subscription))
}

func (h *ARMHandler) deleteWebhook(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.mgr.DeleteWebhook(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // webhook and replication sub-resource lists are intentionally typed; sharing via generics adds noise.
func (h *ARMHandler) listWebhooks(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	whs, err := h.mgr.ListWebhooks(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armWebhook, 0, len(whs))
	for i := range whs {
		out = append(out, toARMWebhook(&whs[i], rp.Subscription))
	}

	azurearm.WriteJSON(w, http.StatusOK, armRegistryList[armWebhook]{Value: out})
}
