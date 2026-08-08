package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
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

	h.place(info.ID, req.CompartmentID)

	// OCI attaches the gateway to its VCN in the create call; the portable
	// driver splits the two.
	if err := h.net.AttachInternetGateway(r.Context(), info.ID, req.VCNID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

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

// Service gateways.

func (h *Handler) serviceGatewayOps() crud {
	return crud{
		create: h.createServiceGateway,
		list:   h.listServiceGateways,
		get:    h.getServiceGateway,
		update: h.updateServiceGateway,
		remove: h.deleteServiceGateway,
	}
}

func (h *Handler) createServiceGateway(w http.ResponseWriter, r *http.Request) {
	var req serviceGatewayRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if len(req.Services) == 0 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "at least one service is required")
		return
	}

	info, err := h.net.CreateVPCEndpoint(r.Context(), netdriver.VPCEndpointConfig{
		VPCID:        req.VCNID,
		ServiceName:  req.Services[0].ServiceID,
		EndpointType: vcnprovider.EndpointTypeGateway,
		RouteTableIDs: func() []string {
			if req.RouteTableID == "" {
				return nil
			}

			return []string{req.RouteTableID}
		}(),
		Tags: withInternal(req.FreeformTags,
			tagDisplayName, req.DisplayName, tagServiceName, req.Services[0].ServiceName),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	ocirest.WriteJSON(w, r, http.StatusOK, h.toServiceGatewayResponse(info))
}

func (h *Handler) listServiceGateways(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeVPCEndpoints(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *netdriver.VPCEndpoint) (string, string) { return v.ID, v.VPCID },
		h.toServiceGatewayResponse)
}

func (h *Handler) getServiceGateway(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findServiceGateway(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toServiceGatewayResponse(info))
}

func (h *Handler) updateServiceGateway(w http.ResponseWriter, r *http.Request, id string) {
	existing, err := h.findServiceGateway(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var req serviceGatewayRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	cfg := netdriver.VPCEndpointConfig{Tags: updatedTags(existing.Tags, req.FreeformTags,
		tagDisplayName, req.DisplayName, tagServiceName, serviceNameOf(&req))}

	if len(req.Services) > 0 {
		cfg.ServiceName = req.Services[0].ServiceID
	}

	if req.RouteTableID != "" {
		cfg.RouteTableIDs = []string{req.RouteTableID}
	}

	info, err := h.net.ModifyVPCEndpoint(r.Context(), id, cfg)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toServiceGatewayResponse(info))
}

func (h *Handler) deleteServiceGateway(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.net.DeleteVPCEndpoint(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) findServiceGateway(ctx context.Context, id string) (*netdriver.VPCEndpoint, error) {
	infos, err := h.net.DescribeVPCEndpoints(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "serviceGateway %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toServiceGatewayResponse(info *netdriver.VPCEndpoint) serviceGatewayResponse {
	var routeTableID string
	if len(info.RouteTableIDs) > 0 {
		routeTableID = info.RouteTableIDs[0]
	}

	return serviceGatewayResponse{
		ID:            info.ID,
		CompartmentID: h.compartmentOf(info.ID),
		VCNID:         info.VPCID,
		DisplayName:   tagOr(info.Tags, tagDisplayName, ""),
		Services: []gatewayService{{
			ServiceID:   info.ServiceName,
			ServiceName: tagOr(info.Tags, tagServiceName, ""),
		}},
		RouteTableID:   routeTableID,
		BlockTraffic:   boolTag(info.Tags, tagBlockTraffic),
		LifecycleState: info.State,
		TimeCreated:    info.CreatedAt,
		FreeformTags:   freeformOf(info.Tags),
		DefinedTags:    definedTags{},
	}
}

// serviceNameOf returns the human name of the first service in a request.
func serviceNameOf(req *serviceGatewayRequest) string {
	if len(req.Services) == 0 {
		return ""
	}

	return req.Services[0].ServiceName
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
