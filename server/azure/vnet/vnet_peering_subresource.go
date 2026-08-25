package vnet

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// routeVNetPeering serves the VirtualNetworkPeeringsClient sub-resource
// surface: PUT/GET/DELETE .../virtualNetworks/{vn}/virtualNetworkPeerings/{n}
// and GET .../virtualNetworks/{vn}/virtualNetworkPeerings (list). Registered
// from routeVNet before any whole-VNet handler ever sees the request, so a
// standalone peering op mutates only the addressed peering and preserves the
// VNet (and every sibling peering on it).
//
//nolint:gocritic,dupl // rp is request-scoped; mirrors routeSecurityRule over a distinct sub-resource by design
func (h *Handler) routeVNetPeering(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.listVNetPeerings(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putVNetPeering(w, r, rp)
	case http.MethodGet:
		h.getVNetPeering(w, r, rp)
	case http.MethodDelete:
		h.deleteVNetPeering(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listVNetPeerings(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteJSON(w, http.StatusOK, vnetPeeringListResponse{})
		return
	}

	vnet, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := vnetPeeringListResponse{}

	for _, p := range meta.ListAzureVNetPeerings(r.Context(), vnet.ID) {
		out.Value = append(out.Value, toVNetPeeringResponse(p, rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getVNetPeering(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"virtual network peerings are not supported by this networking driver")

		return
	}

	vnet, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	p, found := meta.GetAzureVNetPeering(r.Context(), vnet.ID, rp.SubResourceName)
	if !found {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"virtual network peering "+rp.SubResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toVNetPeeringResponse(p, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) putVNetPeering(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"virtual network peerings are not supported by this networking driver")

		return
	}

	vnet, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var body vnetPeeringRequest

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	remoteVNet, remoteARMID, ok := h.resolvePeeringRemote(w, r, body.Properties.RemoteVirtualNetwork)
	if !ok {
		return
	}

	if remoteVNet.ID == vnet.ID {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "a virtual network cannot peer with itself")
		return
	}

	localID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeVNet, rp.ResourceName)

	_, remoteAddrPrefixes := h.vnetLocationPrefixes(r.Context(), remoteVNet)

	peering := netdriver.AzureVNetPeering{
		Name:                      rp.SubResourceName,
		RemoteVirtualNetworkID:    remoteARMID,
		RemoteAddressSpace:        remoteAddrPrefixes,
		AllowVirtualNetworkAccess: boolOr(body.Properties.AllowVirtualNetworkAccess, true),
		AllowForwardedTraffic:     boolOr(body.Properties.AllowForwardedTraffic, false),
		AllowGatewayTransit:       boolOr(body.Properties.AllowGatewayTransit, false),
		UseRemoteGateways:         boolOr(body.Properties.UseRemoteGateways, false),
		PeeringState:              netdriver.AzurePeeringStateInitiated,
	}

	// A peering stays Initiated until its reciprocal peering (one on the
	// remote VNet pointing back at this one) also exists, at which point real
	// ARM reports both sides Connected — see the peeringState citation on
	// AzureVNetPeering in services/networking/driver/azure_network_metadata.go.
	if reciprocal := findReciprocalPeering(r.Context(), meta, remoteVNet.ID, localID); reciprocal != "" {
		peering.PeeringState = netdriver.AzurePeeringStateConnected

		_ = meta.SetAzureVNetPeeringState(r.Context(), remoteVNet.ID, reciprocal, netdriver.AzurePeeringStateConnected)
	}

	updated, err := meta.UpsertAzureVNetPeering(r.Context(), vnet.ID, peering)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "peering-create-"+rp.SubResourceName, toVNetPeeringResponse(updated, rp))
}

// resolvePeeringRemote validates and resolves a PUT body's required
// remoteVirtualNetwork reference, writing the appropriate error response
// itself. ok is false when a response was already written and the caller
// must stop. armID is the reference's own ARM id, echoed back verbatim on the
// stored peering (it may name a different subscription/resource group than
// the local one).
func (h *Handler) resolvePeeringRemote(
	w http.ResponseWriter, r *http.Request, ref *armIDRef,
) (remote *netdriver.VPCInfo, armID string, ok bool) {
	if ref == nil || ref.ID == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "remoteVirtualNetwork is required")
		return nil, "", false
	}

	remoteRP, parsed := azurearm.ParsePath(ref.ID)
	if !parsed || remoteRP.ResourceType != typeVNet {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "malformed remoteVirtualNetwork id")
		return nil, "", false
	}

	vnet, err := findVNetByName(r.Context(), h.net, remoteRP.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return nil, "", false
	}

	return vnet, ref.ID, true
}

// findReciprocalPeering scans every peering stored on the remote VNet for one
// whose own remoteVirtualNetwork reference points back at localVNetARMID —
// the other half of a two-way peering.
func findReciprocalPeering(ctx context.Context, meta netdriver.AzureNetworkMetadata, remoteVNetID, localVNetARMID string) string {
	for _, p := range meta.ListAzureVNetPeerings(ctx, remoteVNetID) {
		if strings.EqualFold(p.RemoteVirtualNetworkID, localVNetARMID) {
			return p.Name
		}
	}

	return ""
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteVNetPeering(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"virtual network peerings are not supported by this networking driver")

		return
	}

	vnet, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := meta.DeleteAzureVNetPeering(r.Context(), vnet.ID, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "peering-delete-"+rp.SubResourceName, nil)
}

//nolint:gocritic // rp is a request-scoped value
func toVNetPeeringResponse(p netdriver.AzureVNetPeering, rp azurearm.ResourcePath) vnetPeeringResponse {
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeVNet, rp.ResourceName) +
		"/virtualNetworkPeerings/" + p.Name

	return vnetPeeringResponse{
		ID:   id,
		Name: p.Name,
		Etag: etagOf(id),
		Properties: vnetPeeringResponseProps{
			ProvisioningState:         provisioningSucceeded,
			PeeringState:              p.PeeringState,
			RemoteVirtualNetwork:      &armIDRef{ID: p.RemoteVirtualNetworkID},
			RemoteAddressSpace:        &addressSpace{AddressPrefixes: p.RemoteAddressSpace},
			AllowVirtualNetworkAccess: p.AllowVirtualNetworkAccess,
			AllowForwardedTraffic:     p.AllowForwardedTraffic,
			AllowGatewayTransit:       p.AllowGatewayTransit,
			UseRemoteGateways:         p.UseRemoteGateways,
		},
	}
}

// boolOr returns *v, or def when v is nil (the request body omitted the
// field entirely).
func boolOr(v *bool, def bool) bool {
	if v == nil {
		return def
	}

	return *v
}
