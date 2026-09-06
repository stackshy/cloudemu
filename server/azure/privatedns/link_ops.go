package privatedns

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	pddriver "github.com/stackshy/cloudemu/v2/services/privatedns/driver"
)

// createOrUpdateLink handles PUT .../privateDnsZones/{zone}/virtualNetworkLinks/
// {link}. The whole link arrives in one body and fully REPLACES the stored
// state. VirtualNetworkLinks.CreateOrUpdate is an LRO; returning the
// fully-provisioned body completes the poller — 201 on create, 200 on update.
func (h *Handler) createOrUpdateLink(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body linkJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	link := buildLink(&body)

	stored, created, err := h.pdns.CreateOrUpdateVirtualNetworkLink(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, link)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toLinkJSON(rp, stored))
}

func (h *Handler) getLink(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.pdns.GetVirtualNetworkLink(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toLinkJSON(rp, stored))
}

// updateLinkTags handles PATCH on a link — VirtualNetworkLinks.Update. Tags are
// REPLACED wholesale; registrationEnabled and the virtualNetwork reference are
// left untouched. A request with tags omitted is a no-op on tags.
func (h *Handler) updateLinkTags(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body linkJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.pdns.GetVirtualNetworkLink(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if body.Tags != nil {
		stored.Tags = body.Tags
	}

	updated, _, err := h.pdns.CreateOrUpdateVirtualNetworkLink(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, *stored)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toLinkJSON(rp, updated))
}

// deleteLink removes the link. VirtualNetworkLinks.Delete is an LRO; a 200
// completes the poller.
func (h *Handler) deleteLink(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.pdns.DeleteVirtualNetworkLink(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listLinks(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.pdns.ListVirtualNetworkLinks(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := linkListResult{Value: make([]linkJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.SubResourceName = stored[i].Name
		out.Value = append(out.Value, toLinkJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// buildLink maps the ARM request body to the native model. An omitted
// registrationEnabled stays nil (defaults to false on read); an explicit false
// is preserved distinctly.
func buildLink(body *linkJSON) pddriver.VirtualNetworkLink {
	link := pddriver.VirtualNetworkLink{Location: defaultLocation, Tags: body.Tags}

	if body.Properties != nil {
		link.RegistrationEnabled = body.Properties.RegistrationEnabled
		if body.Properties.VirtualNetwork != nil {
			link.VirtualNetworkID = body.Properties.VirtualNetwork.ID
		}
	}

	return link
}

// toLinkJSON reconstructs the ARM link body, stamping the id/etag and injecting
// the terminal virtualNetworkLinkState and provisioningState. registrationEnabled
// is always emitted (false when unset), so an explicit false never drifts.
func toLinkJSON(rp *azurearm.ResourcePath, link *pddriver.VirtualNetworkLink) linkJSON {
	zoneID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typePrivateZones, rp.ResourceName)
	id := zoneID + "/" + subVirtualNetworkLinks + "/" + rp.SubResourceName

	reg := false
	if link.RegistrationEnabled != nil {
		reg = *link.RegistrationEnabled
	}

	props := &linkPropsJSON{
		RegistrationEnabled:     &reg,
		VirtualNetworkLinkState: virtualNetworkLinkStateCompleted,
		ProvisioningState:       provisioningStateSucceeded,
	}
	if link.VirtualNetworkID != "" {
		props.VirtualNetwork = &subResource{ID: link.VirtualNetworkID}
	}

	return linkJSON{
		ID:         id,
		Name:       rp.SubResourceName,
		Type:       linkResourceType,
		Location:   defaultLocation,
		Etag:       azurearm.ETag(id),
		Tags:       link.Tags,
		Properties: props,
	}
}
