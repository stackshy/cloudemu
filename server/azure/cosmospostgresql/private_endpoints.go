package cosmospostgresql

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

func (h *Handler) servePrivateEndpoints(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	serveCRUD(w, r, rp, crudHandlers{
		put:  h.createOrUpdatePrivateEndpoint,
		get:  h.getPrivateEndpoint,
		del:  h.deletePrivateEndpoint,
		list: h.listPrivateEndpoints,
	})
}

func (h *Handler) createOrUpdatePrivateEndpoint(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body privateEndpointConnectionResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	status, description := "", ""
	if p := body.Properties; p != nil && p.PrivateLinkServiceConnectionState != nil {
		status = p.PrivateLinkServiceConnectionState.Status
		description = p.PrivateLinkServiceConnectionState.Description
	}

	pec, err := h.db.CreateOrUpdatePrivateEndpointConnection(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, status, description,
	)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMPrivateEndpointConnection(pec, h.childID(rp, subPrivateEPs, pec.Name)))
}

func (h *Handler) getPrivateEndpoint(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	pec, err := h.db.GetPrivateEndpointConnection(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMPrivateEndpointConnection(pec, h.childID(rp, subPrivateEPs, pec.Name)))
}

func (h *Handler) deletePrivateEndpoint(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeletePrivateEndpointConnection(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listPrivateEndpoints(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	pecs, err := h.db.ListPrivateEndpointConnections(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, armListOf(pecs, func(pec *cpgdriver.PrivateEndpointConnection) privateEndpointConnectionResource {
		return toARMPrivateEndpointConnection(pec, h.childID(rp, subPrivateEPs, pec.Name))
	}))
}

func (h *Handler) servePrivateLinks(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	serveReadOnly(w, r, rp, h.listPrivateLinks, h.getPrivateLink)
}

func (h *Handler) getPrivateLink(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	plr, err := h.db.GetPrivateLinkResource(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMPrivateLinkResource(plr, h.childID(rp, subPrivateLinks, plr.Name)))
}

func (h *Handler) listPrivateLinks(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	plrs, err := h.db.ListPrivateLinkResources(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, armListOf(plrs, func(plr *cpgdriver.PrivateLinkResource) privateLinkResource {
		return toARMPrivateLinkResource(plr, h.childID(rp, subPrivateLinks, plr.Name))
	}))
}
