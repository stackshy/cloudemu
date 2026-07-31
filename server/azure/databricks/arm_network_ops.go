package databricks

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// --- Private endpoint connections ---

//nolint:dupl // parallel sub-resource dispatch; mirrors servePeering over a different collection
func (h *Handler) servePEC(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		h.listPEC(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putPEC(w, r, rp)
	case http.MethodGet:
		h.getPEC(w, r, rp)
	case http.MethodDelete:
		h.deletePEC(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) putPEC(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armPEC
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	var status, description string
	if body.Properties != nil && body.Properties.PrivateLinkServiceConnectionState != nil {
		status = body.Properties.PrivateLinkServiceConnectionState.Status
		description = body.Properties.PrivateLinkServiceConnectionState.Description
	}

	c, err := h.dbx.PutPrivateEndpointConnection(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, status, description)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMPEC(c))
}

func (h *Handler) getPEC(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	c, err := h.dbx.GetPrivateEndpointConnection(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMPEC(c))
}

func (h *Handler) deletePEC(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// ARM DELETE is idempotent: a missing resource is the caller's desired end
	// state, so a NotFound from the driver still returns 204 (teardown retries
	// and delete-then-delete must not fail on the second pass).
	err := h.dbx.DeletePrivateEndpointConnection(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listPEC(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	cs, err := h.dbx.ListPrivateEndpointConnections(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]armPEC, 0, len(cs))
	for i := range cs {
		out = append(out, toARMPEC(&cs[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, armPECList{Value: out})
}

// --- Private link resources (read-only) ---

func (h *Handler) servePLR(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)

		return
	}

	if rp.SubResourceName == "" {
		h.listPLR(w, r, rp)

		return
	}

	g, err := h.dbx.GetPrivateLinkResource(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMGroupIDInformation(g))
}

func (h *Handler) listPLR(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	gs, err := h.dbx.ListPrivateLinkResources(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]armGroupIDInformation, 0, len(gs))
	for i := range gs {
		out = append(out, toARMGroupIDInformation(&gs[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, armPLRList{Value: out})
}

// --- Virtual network peerings ---

//nolint:dupl // parallel sub-resource dispatch; mirrors servePEC over a different collection
func (h *Handler) servePeering(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		h.listPeering(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putPeering(w, r, rp)
	case http.MethodGet:
		h.getPeering(w, r, rp)
	case http.MethodDelete:
		h.deletePeering(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) putPeering(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armPeering
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := dbxdriver.VirtualNetworkPeeringConfig{}
	if p := body.Properties; p != nil {
		cfg.AllowForwardedTraffic = p.AllowForwardedTraffic
		cfg.AllowGatewayTransit = p.AllowGatewayTransit
		cfg.AllowVirtualNetworkAccess = p.AllowVirtualNetworkAccess
		cfg.UseRemoteGateways = p.UseRemoteGateways
		cfg.DatabricksAddressSpace = fromARMAddressSpace(p.DatabricksAddressSpace)
		cfg.RemoteAddressSpace = fromARMAddressSpace(p.RemoteAddressSpace)

		if p.DatabricksVirtualNetwork != nil {
			cfg.DatabricksVNetID = p.DatabricksVirtualNetwork.ID
		}

		if p.RemoteVirtualNetwork != nil {
			cfg.RemoteVNetID = p.RemoteVirtualNetwork.ID
		}
	}

	peer, err := h.dbx.CreateOrUpdateVNetPeering(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMPeering(peer))
}

func (h *Handler) getPeering(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	peer, err := h.dbx.GetVNetPeering(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMPeering(peer))
}

func (h *Handler) deletePeering(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// ARM DELETE is idempotent: a missing resource is the caller's desired end
	// state, so a NotFound from the driver still returns 204 (teardown retries
	// and delete-then-delete must not fail on the second pass).
	err := h.dbx.DeleteVNetPeering(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listPeering(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	ps, err := h.dbx.ListVNetPeerings(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]armPeering, 0, len(ps))
	for i := range ps {
		out = append(out, toARMPeering(&ps[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, armPeeringList{Value: out})
}

// --- Outbound network dependencies (read-only list) ---

func (h *Handler) serveOutbound(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)

		return
	}

	eps, err := h.dbx.ListOutboundNetworkDependencies(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]armOutboundEndpoint, 0, len(eps))
	for i := range eps {
		out = append(out, toARMOutbound(&eps[i]))
	}

	// The armdatabricks OutboundNetworkDependenciesEndpoints List response is a
	// bare JSON array (the SDK unmarshals the body straight into a slice), not a
	// {"value":[...]} envelope like the other list endpoints.
	azurearm.WriteJSON(w, http.StatusOK, out)
}
