package vnet

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Microsoft.Network route next-hop types (RouteNextHopType). VirtualApplianceEcmp
// is intentionally omitted: it requires the nextHop.nextHopIpAddresses ECMP shape
// that azurerm_route does not model, and is tracked as a build-out gap.
const (
	nextHopVirtualNetworkGateway = "VirtualNetworkGateway"
	nextHopVnetLocal             = "VnetLocal"
	nextHopInternet              = "Internet"
	nextHopVirtualAppliance      = "VirtualAppliance"
	nextHopNone                  = "None"
)

// routeRoute serves the RoutesClient / azurerm_route sub-resource surface:
// PUT/GET/DELETE .../routeTables/{rt}/routes/{route} and GET .../routeTables/{rt}/routes
// (list). Registered from routeRouteTable before any whole-route-table handler
// sees the request, so a standalone route op mutates only the addressed route and
// preserves siblings (mirrors routeSecurityRule).
//
//nolint:gocritic,dupl // rp is request-scoped; mirrors routeSecurityRule over a distinct sub-resource by design
func (h *Handler) routeRoute(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.listRoutes(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putRoute(w, r, rp)
	case http.MethodGet:
		h.getRoute(w, r, rp)
	case http.MethodDelete:
		h.deleteRoute(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := h.findRouteTableInGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	rtID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeRouteTable, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, routeListResponse{Value: fromAzureRoutes(rtID, h.storedRoutes(r.Context(), info.ID))})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getRoute(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := h.findRouteTableInGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	rtID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeRouteTable, rp.ResourceName)

	for _, rt := range h.storedRoutes(r.Context(), info.ID) {
		if rt.Name == rp.SubResourceName {
			azurearm.WriteJSON(w, http.StatusOK, fromAzureRoutes(rtID, []netdriver.AzureRoute{rt})[0])
			return
		}
	}

	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "route "+rp.SubResourceName+" not found")
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) putRoute(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"route tables are not supported by this networking driver")

		return
	}

	info, err := h.findRouteTableInGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var body route

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	// The URL name is authoritative, matching ARM's PUT-by-name semantics.
	body.Name = rp.SubResourceName

	rt := toAzureRoutes([]route{body})[0]

	if verr := validateAzureRoute(rt); verr != nil {
		azurearm.WriteCErr(w, verr)
		return
	}

	if _, err := meta.UpsertAzureRoute(r.Context(), info.ID, rt); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	rtID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeRouteTable, rp.ResourceName)

	writeAcceptedAsync(w, r, rp.Subscription, "route-create-"+rp.ResourceName+"-"+rp.SubResourceName,
		fromAzureRoutes(rtID, []netdriver.AzureRoute{rt})[0])
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"route tables are not supported by this networking driver")

		return
	}

	info, err := h.findRouteTableInGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := meta.DeleteAzureRoute(r.Context(), info.ID, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "route-delete-"+rp.ResourceName+"-"+rp.SubResourceName, nil)
}

// storedRoutes returns the routes stored for the route table with the given
// driver id (empty when the driver carries no Azure metadata or the table has
// none yet).
func (h *Handler) storedRoutes(ctx context.Context, rtDriverID string) []netdriver.AzureRoute {
	meta, ok := h.azureMeta()
	if !ok {
		return nil
	}

	md, found := meta.GetAzureRouteTableMetadata(ctx, rtDriverID)
	if !found {
		return nil
	}

	return md.Routes
}

// validateAzureRoute checks a route the way real ARM does before storing it:
// nextHopType must be one of the RouteNextHopType values, and a nextHopIpAddress
// is only accepted when nextHopType is VirtualAppliance.
func validateAzureRoute(rt netdriver.AzureRoute) error {
	switch rt.NextHopType {
	case nextHopVirtualNetworkGateway, nextHopVnetLocal, nextHopInternet, nextHopVirtualAppliance, nextHopNone:
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "invalid nextHopType %q", rt.NextHopType)
	}

	if rt.NextHopIPAddress != "" && rt.NextHopType != nextHopVirtualAppliance {
		return cerrors.Newf(cerrors.InvalidArgument,
			"nextHopIpAddress is only allowed when nextHopType is %s", nextHopVirtualAppliance)
	}

	return nil
}
