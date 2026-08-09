package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) routeTableOps() crud {
	return crud{
		create: h.createRouteTable,
		list:   h.listRouteTables,
		get:    h.getRouteTable,
		update: h.updateRouteTable,
		remove: h.deleteRouteTable,
	}
}

func (h *Handler) createRouteTable(w http.ResponseWriter, r *http.Request) {
	var req routeTableRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	info, err := h.net.CreateRouteTable(r.Context(), netdriver.RouteTableConfig{
		VPCID: req.VCNID,
		Tags:  withInternal(req.FreeformTags, tagDisplayName, req.DisplayName),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	if req.RouteRules != nil {
		info.Routes = toDriverRoutes(req.RouteRules)

		if err := h.extras.ReplaceRoutes(r.Context(), info.ID, info.Routes); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toRouteTableResponse(info))
}

func (h *Handler) listRouteTables(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeRouteTables(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *netdriver.RouteTable) (string, string) { return v.ID, v.VPCID },
		h.toRouteTableResponse)
}

func (h *Handler) getRouteTable(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findRouteTable(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toRouteTableResponse(info))
}

// updateRouteTable replaces the whole rule set, as OCI's update does.
func (h *Handler) updateRouteTable(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findRouteTable(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var req routeTableRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	tags := updatedTags(info.Tags, req.FreeformTags, tagDisplayName, req.DisplayName)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if req.RouteRules != nil {
		info.Routes = toDriverRoutes(req.RouteRules)

		if err := h.extras.ReplaceRoutes(r.Context(), id, info.Routes); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}
	}

	info.Tags = tags

	ocirest.WriteJSON(w, r, http.StatusOK, h.toRouteTableResponse(info))
}

func (h *Handler) deleteRouteTable(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.net.DeleteRouteTable(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// findRouteTable reads one route table, reporting OCI's not-found for an
// unknown OCID.
func (h *Handler) findRouteTable(ctx context.Context, id string) (*netdriver.RouteTable, error) {
	infos, err := h.net.DescribeRouteTables(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "routeTable %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toRouteTableResponse(info *netdriver.RouteTable) routeTableResponse {
	return routeTableResponse{
		ID:             info.ID,
		CompartmentID:  h.compartmentOf(info.ID),
		VCNID:          info.VPCID,
		DisplayName:    tagOr(info.Tags, tagDisplayName, ""),
		RouteRules:     toWireRoutes(info.Routes),
		LifecycleState: lifecycleAvailable,
		TimeCreated:    h.extras.Created(info.ID),
		FreeformTags:   freeformOf(info.Tags),
		DefinedTags:    definedTags{},
	}
}
