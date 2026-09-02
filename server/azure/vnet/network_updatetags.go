package vnet

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// ARM UpdateTags PATCH handlers for the core VNet-family resources
// (virtualNetworks, networkSecurityGroups, routeTables, publicIPAddresses,
// natGateways, networkInterfaces). Each armnetwork *Client.UpdateTags is a
// synchronous PATCH that answers 200 with the full resource, and REPLACES the
// tag collection wholesale — it does not merge (tags:{} wipes every tag). The
// resource's other properties are left intact. subnets are NOT independently
// taggable in Azure (their tags live on the parent virtual network), so they get
// no UpdateTags handler.
//
// The get-modify-put is serialized by h.patchMu so two concurrent PATCHes cannot
// drop each other's write; the actual store mutation is a single atomic driver
// call. Wire-internal cloudemu: anchor tags are preserved so the (rg, name)
// lookup keeps resolving after the user tags are replaced.

// preserveInternalTags folds a resource's wire-internal cloudemu: anchor tags
// (name / resource-group anchors and association references) into a wholesale
// UpdateTags replacement, so a PATCH that SETS the user-facing tags never drops
// the bookkeeping the (rg, name) lookup depends on. replacement is the caller's
// new user tag set (nil to wipe them); the result is nil when nothing remains.
//
// The resource's identity anchors are immutable across any PATCH: a caller's
// cloudemu:-prefixed keys are stripped from the replacement FIRST (so user input
// can never introduce a reserved key), and the STORED anchors are re-asserted
// LAST (so they always win even against a stray reserved key). The resource must
// always remain resolvable by (rg, name) after any UpdateTags, including a wipe.
func preserveInternalTags(existing, replacement map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(replacement))

	// User tags first, with reserved cloudemu: keys dropped so a caller can never
	// overwrite (or introduce) an identity anchor.
	for k, v := range replacement {
		if strings.HasPrefix(k, internalTagPrefix) {
			continue
		}

		out[k] = v
	}

	// Re-assert the stored anchors last so they always win — identity survives.
	for k, v := range existing {
		if strings.HasPrefix(k, internalTagPrefix) {
			out[k] = v
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// tagReplacer returns the wholesale-tag-replacement surface if the networking
// driver implements it (the Azure provider does).
func (h *Handler) tagReplacer() (netdriver.AzureNetworkTagReplacer, bool) {
	svc, ok := h.net.(netdriver.AzureNetworkTagReplacer)

	return svc, ok
}

//nolint:gocritic,dupl // rp is request-scoped; UpdateTags handlers share one get-modify-put shape over distinct types by design
func (h *Handler) patchVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	svc, ok := h.tagReplacer()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "UpdateTags is not supported by this networking driver")
		return
	}

	h.patchMu.Lock()

	info, err := findVNetInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		h.patchMu.Unlock()
		azurearm.WriteCErr(w, err)

		return
	}

	if req.Tags != nil {
		_ = svc.ReplaceVPCTags(r.Context(), info.ID, preserveInternalTags(info.Tags, req.Tags))

		if refreshed, rerr := findVNetInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName); rerr == nil {
			info = refreshed
		}
	}

	h.patchMu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, h.vnetResponse(r.Context(), info, rp))
}

//nolint:gocritic,dupl // rp is request-scoped; UpdateTags handlers share one get-modify-put shape over distinct types by design
func (h *Handler) patchNSG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	svc, ok := h.tagReplacer()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "UpdateTags is not supported by this networking driver")
		return
	}

	h.patchMu.Lock()

	info, err := findNSGInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		h.patchMu.Unlock()
		azurearm.WriteCErr(w, err)

		return
	}

	if req.Tags != nil {
		_ = svc.ReplaceSecurityGroupTags(r.Context(), info.ID, preserveInternalTags(info.Tags, req.Tags))

		if refreshed, rerr := findNSGInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName); rerr == nil {
			info = refreshed
		}
	}

	h.patchMu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, h.nsgResponse(r.Context(), info, rp))
}

//nolint:gocritic,dupl // rp is request-scoped; UpdateTags handlers share one get-modify-put shape over distinct types by design
func (h *Handler) patchPublicIP(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	svc, ok := h.tagReplacer()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "UpdateTags is not supported by this networking driver")
		return
	}

	h.patchMu.Lock()

	info, err := findPublicIPByName(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		h.patchMu.Unlock()
		azurearm.WriteCErr(w, err)

		return
	}

	if req.Tags != nil {
		_ = svc.ReplaceAddressTags(r.Context(), info.AllocationID, preserveInternalTags(info.Tags, req.Tags))

		if refreshed, rerr := findPublicIPByName(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName); rerr == nil {
			info = refreshed
		}
	}

	h.patchMu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, h.toPublicIPResponse(r.Context(), info, rp, defaultLoc))
}

//nolint:gocritic,dupl // rp is request-scoped; UpdateTags handlers share one get-modify-put shape over distinct types by design
func (h *Handler) patchNATGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	svc, ok := h.tagReplacer()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "UpdateTags is not supported by this networking driver")
		return
	}

	h.patchMu.Lock()

	info, err := findNATGatewayByName(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		h.patchMu.Unlock()
		azurearm.WriteCErr(w, err)

		return
	}

	if req.Tags != nil {
		_ = svc.ReplaceNATGatewayTags(r.Context(), info.ID, preserveInternalTags(info.Tags, req.Tags))

		if refreshed, rerr := findNATGatewayByName(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName); rerr == nil {
			info = refreshed
		}
	}

	h.patchMu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, h.natGatewayResponse(r.Context(), info, rp, defaultLoc))
}

// patchRouteTable replaces a route table's tags. Route-table tags live in the
// Azure route-table metadata store (not the cross-cloud anchor), and hold only
// user tags, so the replacement needs no internal-anchor preservation.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchRouteTable(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "UpdateTags is not supported by this networking driver")
		return
	}

	h.patchMu.Lock()

	info, err := h.findRouteTableInGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		h.patchMu.Unlock()
		azurearm.WriteCErr(w, err)

		return
	}

	if req.Tags != nil {
		md, _ := meta.GetAzureRouteTableMetadata(r.Context(), info.ID)
		md.Tags = replacementTags(req.Tags)
		_ = meta.PutAzureRouteTableMetadata(r.Context(), info.ID, md)
	}

	h.patchMu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, h.routeTableResponse(r.Context(), info, rp))
}

// patchNIC replaces a network interface's tags. NIC tags are user-only (the NIC
// store is keyed by (rg, name), not by anchor tags), so no internal-anchor
// preservation is needed.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchNIC(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, svc netdriver.AzureNetworkInterfaces) {
	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	replacer, ok := h.tagReplacer()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "UpdateTags is not supported by this networking driver")
		return
	}

	h.patchMu.Lock()

	if _, err := svc.GetNetworkInterface(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		h.patchMu.Unlock()
		azurearm.WriteCErr(w, err)

		return
	}

	if req.Tags != nil {
		_ = replacer.ReplaceNetworkInterfaceTags(r.Context(), rp.ResourceGroup, rp.ResourceName, replacementTags(req.Tags))
	}

	nic, err := svc.GetNetworkInterface(r.Context(), rp.ResourceGroup, rp.ResourceName)
	h.patchMu.Unlock()

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toNICResponse(nic, rp))
}
