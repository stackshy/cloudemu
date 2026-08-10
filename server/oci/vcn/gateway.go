package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Internet gateways.

func (h *Handler) internetGatewayOps() crud {
	return crud{
		create: h.createInternetGateway,
		list:   h.listInternetGateways,
		get:    h.getInternetGateway,
		update: h.updateInternetGateway,
		remove: h.deleteInternetGateway,
	}
}

func (h *Handler) createInternetGateway(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeGateway(w, r)
	if !ok {
		return
	}

	enabled := req.IsEnabled == nil || *req.IsEnabled

	info, err := h.net.CreateInternetGateway(r.Context(), netdriver.InternetGatewayConfig{
		Tags: withInternal(req.FreeformTags,
			tagDisplayName, req.DisplayName, tagIsEnabled, boolText(enabled)),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	// OCI attaches the gateway to its VCN in the create call; the portable
	// driver splits the two, so an attach that fails has to take the gateway
	// the first half already stored back out.
	if err := h.net.AttachInternetGateway(r.Context(), info.ID, req.VCNID); err != nil {
		_ = h.net.DeleteInternetGateway(r.Context(), info.ID)

		ocirest.WriteDriverError(w, r, err)

		return
	}

	h.place(info.ID, req.CompartmentID)

	info.VpcID = req.VCNID

	ocirest.WriteJSON(w, r, http.StatusOK, h.toInternetGatewayResponse(info))
}

func (h *Handler) listInternetGateways(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeInternetGateways(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *netdriver.InternetGateway) (string, string) { return v.ID, v.VpcID },
		h.toInternetGatewayResponse)
}

func (h *Handler) getInternetGateway(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findInternetGateway(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toInternetGatewayResponse(info))
}

//nolint:dupl // the two gateways differ only in the flag they carry; the shared part is already factored out.
func (h *Handler) updateInternetGateway(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findInternetGateway(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	req, ok := decodeGateway(w, r)
	if !ok {
		return
	}

	tags, ok := h.applyGatewayUpdate(w, r, id, info.Tags, &req, tagIsEnabled, req.IsEnabled)
	if !ok {
		return
	}

	info.Tags = tags

	ocirest.WriteJSON(w, r, http.StatusOK, h.toInternetGatewayResponse(info))
}

func (h *Handler) deleteInternetGateway(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findInternetGateway(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if info.VpcID != "" {
		if err := h.net.DetachInternetGateway(r.Context(), id, info.VpcID); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}
	}

	if err := h.net.DeleteInternetGateway(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) findInternetGateway(ctx context.Context, id string) (*netdriver.InternetGateway, error) {
	infos, err := h.net.DescribeInternetGateways(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "internetGateway %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toInternetGatewayResponse(info *netdriver.InternetGateway) internetGatewayResponse {
	return internetGatewayResponse{
		ID:             info.ID,
		CompartmentID:  h.compartmentOf(info.ID),
		VCNID:          info.VpcID,
		DisplayName:    tagOr(info.Tags, tagDisplayName, ""),
		IsEnabled:      boolTag(info.Tags, tagIsEnabled),
		LifecycleState: lifecycleAvailable,
		TimeCreated:    h.extras.Created(info.ID),
		FreeformTags:   freeformOf(info.Tags),
		DefinedTags:    definedTags{},
	}
}

// NAT gateways.

func (h *Handler) natGatewayOps() crud {
	return crud{
		create: h.createNATGateway,
		list:   h.listNATGateways,
		get:    h.getNATGateway,
		update: h.updateNATGateway,
		remove: h.deleteNATGateway,
	}
}

func (h *Handler) createNATGateway(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeGateway(w, r)
	if !ok {
		return
	}

	blocked := req.BlockTraffic != nil && *req.BlockTraffic

	info, err := h.net.CreateNATGateway(r.Context(), netdriver.NATGatewayConfig{
		SubnetID: req.VCNID,
		Tags: withInternal(req.FreeformTags,
			tagDisplayName, req.DisplayName, tagBlockTraffic, boolText(blocked)),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	ocirest.WriteJSON(w, r, http.StatusOK, h.toNATGatewayResponse(info))
}

func (h *Handler) listNATGateways(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeNATGateways(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *netdriver.NATGateway) (string, string) { return v.ID, v.VPCID },
		h.toNATGatewayResponse)
}

func (h *Handler) getNATGateway(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findNATGateway(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toNATGatewayResponse(info))
}

//nolint:dupl // see updateInternetGateway.
func (h *Handler) updateNATGateway(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findNATGateway(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	req, ok := decodeGateway(w, r)
	if !ok {
		return
	}

	tags, ok := h.applyGatewayUpdate(w, r, id, info.Tags, &req, tagBlockTraffic, req.BlockTraffic)
	if !ok {
		return
	}

	info.Tags = tags

	ocirest.WriteJSON(w, r, http.StatusOK, h.toNATGatewayResponse(info))
}

func (h *Handler) deleteNATGateway(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.net.DeleteNATGateway(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) findNATGateway(ctx context.Context, id string) (*netdriver.NATGateway, error) {
	infos, err := h.net.DescribeNATGateways(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "natGateway %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toNATGatewayResponse(info *netdriver.NATGateway) natGatewayResponse {
	return natGatewayResponse{
		ID:             info.ID,
		CompartmentID:  h.compartmentOf(info.ID),
		VCNID:          info.VPCID,
		DisplayName:    tagOr(info.Tags, tagDisplayName, ""),
		NatIP:          info.PublicIP,
		BlockTraffic:   boolTag(info.Tags, tagBlockTraffic),
		LifecycleState: info.State,
		TimeCreated:    info.CreatedAt,
		FreeformTags:   freeformOf(info.Tags),
		DefinedTags:    definedTags{},
	}
}

// applyGatewayUpdate writes a gateway update's display name, freeform tags and
// its one boolean attribute, returning the tag map now in force.
func (h *Handler) applyGatewayUpdate(
	w http.ResponseWriter, r *http.Request, id string,
	existing map[string]string, req *gatewayRequest, flagKey string, flag *bool,
) (map[string]string, bool) {
	value := tagOr(existing, flagKey, "")
	if flag != nil {
		value = boolText(*flag)
	}

	tags := updatedTags(existing, req.FreeformTags, tagDisplayName, req.DisplayName, flagKey, value)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return nil, false
	}

	return tags, true
}

// decodeGateway reads the body every gateway create and update shares and
// checks the compartment OCI requires on creates.
func decodeGateway(w http.ResponseWriter, r *http.Request) (gatewayRequest, bool) {
	var req gatewayRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return req, false
	}

	if r.Method == http.MethodPost && req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return req, false
	}

	return req, true
}
