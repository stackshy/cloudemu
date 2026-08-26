package vnet

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// RouteTable operations (RouteTablesClient / azurerm_route_table). Mirrors the
// networkSecurityGroups path: the cross-cloud RouteTable acts as an anchor
// (addressable by (rg, name) via armRouteTableTag / armRouteTableRGTag) while
// the ARM-specific fields (region, routes, user tags) round-trip through the
// Azure route-table metadata store.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeRouteTable(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listRouteTables(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createRouteTable(w, r, rp)
	case http.MethodGet:
		h.getRouteTable(w, r, rp)
	case http.MethodDelete:
		h.deleteRouteTable(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createRouteTable(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req routeTableRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.upsertRouteTable(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	if meta, ok := h.azureMeta(); ok {
		_ = meta.PutAzureRouteTableMetadata(r.Context(), info.ID, netdriver.AzureRouteTableMetadata{
			Location: loc,
			Routes:   toAzureRoutes(req.Properties.Routes),
			Tags:     req.Tags,
		})
	}

	writeAcceptedAsync(w, r, rp.Subscription, "routetable-create-"+rp.ResourceName, h.routeTableResponse(r.Context(), info, rp))
}

// upsertRouteTable reuses an existing route table of the same name (idempotent
// re-PUT) or creates one, anchoring the driver route table to any VNet
// (creating a synthetic one when none exists, as the driver requires a VPC id).
func (h *Handler) upsertRouteTable(ctx context.Context, rg, name string) (*netdriver.RouteTable, error) {
	if existing, err := h.findRouteTableInGroup(ctx, rg, name); err == nil {
		return existing, nil
	}

	vpcs, _ := h.net.DescribeVPCs(ctx, nil)

	var anchor string

	if len(vpcs) > 0 {
		anchor = vpcs[0].ID
	} else {
		v, vErr := h.net.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
		if vErr != nil {
			return nil, vErr
		}

		anchor = v.ID
	}

	return h.net.CreateRouteTable(ctx, netdriver.RouteTableConfig{
		VPCID: anchor,
		Tags:  mergeTags(mergeTags(nil, armRouteTableTag, name), armRouteTableRGTag, rg),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getRouteTable(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := h.findRouteTableInGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.routeTableResponse(r.Context(), info, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listRouteTables(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	infos, err := h.net.DescribeRouteTables(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := routeTableListResponse{}

	for i := range infos {
		name := tagOr(infos[i].Tags, armRouteTableTag, "")
		if name == "" {
			continue // not an ARM-managed route table (no name anchor)
		}

		itemRG := tagOr(infos[i].Tags, armRouteTableRGTag, "")
		if rp.ResourceGroup != "" && !strings.EqualFold(itemRG, rp.ResourceGroup) {
			continue
		}

		scope := rp
		scope.ResourceName = name

		if scope.ResourceGroup == "" {
			scope.ResourceGroup = itemRG
		}

		out.Value = append(out.Value, h.routeTableResponse(r.Context(), &infos[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteRouteTable(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := h.findRouteTableInGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if meta, ok := h.azureMeta(); ok {
		meta.DeleteAzureRouteTableMetadata(r.Context(), info.ID)
	}

	if err := h.net.DeleteRouteTable(r.Context(), info.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "routetable-delete-"+rp.ResourceName, nil)
}

// findRouteTableInGroup resolves a route table by both its ARM name and resource
// group; see findNSGInGroup for the rg semantics.
func (h *Handler) findRouteTableInGroup(ctx context.Context, rg, name string) (*netdriver.RouteTable, error) {
	infos, err := h.net.DescribeRouteTables(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, armRouteTableTag, "") != name {
			continue
		}

		if rg != "" && !strings.EqualFold(tagOr(infos[i].Tags, armRouteTableRGTag, ""), rg) {
			continue
		}

		return &infos[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "routeTable %s not found", name)
}

// purgeRouteTables deletes every route table (with its metadata) in the resource
// group, returning the first delete error encountered.
//
//nolint:dupl // mirrors purgeNSGs over a distinct resource type and store by design
func (h *Handler) purgeRouteTables(ctx context.Context, resourceGroup string) error {
	rts, err := h.net.DescribeRouteTables(ctx, nil)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range rts {
		if !strings.EqualFold(tagOr(rts[i].Tags, armRouteTableRGTag, ""), resourceGroup) {
			continue
		}

		if meta, ok := h.azureMeta(); ok {
			meta.DeleteAzureRouteTableMetadata(ctx, rts[i].ID)
		}

		if derr := h.net.DeleteRouteTable(ctx, rts[i].ID); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

// Response shaping.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeTableResponse(ctx context.Context, info *netdriver.RouteTable, rp azurearm.ResourcePath) routeTableResponse {
	location := defaultLoc

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeRouteTable, rp.ResourceName)

	var (
		routes []route
		tags   map[string]string
	)

	if meta, ok := h.azureMeta(); ok {
		if md, found := meta.GetAzureRouteTableMetadata(ctx, info.ID); found {
			if md.Location != "" {
				location = md.Location
			}

			routes = fromAzureRoutes(id, md.Routes)
			tags = md.Tags
		}
	}

	if routes == nil {
		routes = []route{}
	}

	return routeTableResponse{
		ID:       id,
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeRouteTable,
		Location: location,
		Etag:     etagOf(id),
		Tags:     tags,
		Properties: routeTableResponseProps{
			ProvisioningState: provisioningSucceeded,
			Routes:            routes,
			Subnets:           h.routeTableAssociatedSubnets(ctx, id),
		},
	}
}

// routeTableAssociatedSubnets scans every subnet for a routeTable reference
// (armSubnetRouteTableTag) matching rtARMID, the read-only back-reference real
// ARM reports on a routeTables GET (mirrors nsgAssociatedSubnets).
func (h *Handler) routeTableAssociatedSubnets(ctx context.Context, rtARMID string) []armIDRef {
	return h.subnetsAssociatedByTag(ctx, armSubnetRouteTableTag, rtARMID)
}

// toAzureRoutes maps the wire route shape to the stored driver representation,
// keeping empty fields verbatim so a Get round-trips exactly what was sent.
func toAzureRoutes(in []route) []netdriver.AzureRoute {
	out := make([]netdriver.AzureRoute, 0, len(in))

	for i := range in {
		p := in[i].Properties
		out = append(out, netdriver.AzureRoute{
			Name:             in[i].Name,
			AddressPrefix:    p.AddressPrefix,
			NextHopType:      p.NextHopType,
			NextHopIPAddress: p.NextHopIPAddress,
		})
	}

	return out
}

// fromAzureRoutes maps stored driver routes back to their wire shape, stamping
// each route's ARM id and a terminal provisioning state.
func fromAzureRoutes(routeTableID string, in []netdriver.AzureRoute) []route {
	out := make([]route, 0, len(in))

	for i := range in {
		rt := in[i]
		out = append(out, route{
			Name: rt.Name,
			ID:   routeTableID + "/routes/" + rt.Name,
			Properties: routeProps{
				AddressPrefix:     rt.AddressPrefix,
				NextHopType:       rt.NextHopType,
				NextHopIPAddress:  rt.NextHopIPAddress,
				ProvisioningState: provisioningSucceeded,
			},
		})
	}

	return out
}
