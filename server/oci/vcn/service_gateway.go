package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

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
