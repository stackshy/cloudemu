package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// Local peering gateways.

// serveLPG routes the collection and the connect action on a single gateway.
func (h *Handler) serveLPG(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.Sub == subActions {
		h.lpgAction(w, r, rt)
		return
	}

	serveCRUD(w, r, rt, h.lpgOps())
}

func (h *Handler) lpgOps() crud {
	return crud{
		create: h.createLPG,
		list:   h.listLPGs,
		get:    h.getLPG,
		update: h.updateLPG,
		remove: h.deleteLPG,
	}
}

// lpgAction serves the connect action, which is how OCI peers two gateways.
func (h *Handler) lpgAction(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	if rt.Action != actionConnect {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown action "+rt.Action)
		return
	}

	var req lpgRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.PeerID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "peerId is required")
		return
	}

	if err := h.extras.ConnectLocalPeeringGateways(r.Context(), rt.ID, req.PeerID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) createLPG(w http.ResponseWriter, r *http.Request) {
	var req lpgRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	info, err := h.extras.CreateLocalPeeringGateway(r.Context(), req.VCNID,
		withInternal(req.FreeformTags, tagDisplayName, req.DisplayName, tagRouteTable, req.RouteTableID))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	ocirest.WriteJSON(w, r, http.StatusOK, h.toLPGResponse(info))
}

func (h *Handler) listLPGs(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.extras.DescribeLocalPeeringGateways(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(g *vcnprovider.LocalPeeringGateway) (string, string) { return g.ID, g.VCNID },
		h.toLPGResponse)
}

func (h *Handler) getLPG(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findLPG(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toLPGResponse(info))
}

//nolint:dupl // the tag-only updates differ by request type, tags and renderer.
func (h *Handler) updateLPG(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findLPG(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var req lpgRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	tags := updatedTags(info.Tags, req.FreeformTags,
		tagDisplayName, req.DisplayName, tagRouteTable, req.RouteTableID)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	info.Tags = tags

	ocirest.WriteJSON(w, r, http.StatusOK, h.toLPGResponse(info))
}

func (h *Handler) deleteLPG(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.extras.DeleteLocalPeeringGateway(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) findLPG(ctx context.Context, id string) (*vcnprovider.LocalPeeringGateway, error) {
	infos, err := h.extras.DescribeLocalPeeringGateways(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "localPeeringGateway %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toLPGResponse(info *vcnprovider.LocalPeeringGateway) lpgResponse {
	out := lpgResponse{
		ID:                        info.ID,
		CompartmentID:             h.compartmentOf(info.ID),
		VCNID:                     info.VCNID,
		DisplayName:               tagOr(info.Tags, tagDisplayName, ""),
		PeerID:                    info.PeerID,
		PeeringStatus:             info.PeeringStatus,
		PeerAdvertisedCIDRDetails: info.PeerAdvertisedCIDRs,
		RouteTableID:              tagOr(info.Tags, tagRouteTable, ""),
		LifecycleState:            lifecycleAvailable,
		TimeCreated:               h.extras.Created(info.ID),
		FreeformTags:              freeformOf(info.Tags),
		DefinedTags:               definedTags{},
	}

	// Real OCI advertises the aggregate of the peer's blocks; CloudEmu does
	// not aggregate, so the primary block stands in.
	if len(info.PeerAdvertisedCIDRs) > 0 {
		out.PeerAdvertisedCIDR = info.PeerAdvertisedCIDRs[0]
	}

	return out
}
