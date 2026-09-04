package vnet

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	typeNATGateway   = "natGateways"
	armNATGatewayTag = "cloudemu:azureNatGatewayName"
	// armNATGatewayRGTag scopes NAT gateway lookups to the resource group they
	// were created in, the same isolation fix applied to public IPs.
	armNATGatewayRGTag = "cloudemu:azureNatGatewayResourceGroup"
)

// ARM JSON shapes for Microsoft.Network/natGateways.

type natGatewayRequest struct {
	Location   string                 `json:"location"`
	Tags       map[string]string      `json:"tags,omitempty"`
	SKU        *natGatewaySKU         `json:"sku,omitempty"`
	Zones      []string               `json:"zones,omitempty"`
	Properties natGatewayRequestProps `json:"properties"`
}

type natGatewaySKU struct {
	Name string `json:"name,omitempty"`
}

type natGatewayRequestProps struct {
	IdleTimeoutInMinutes int        `json:"idleTimeoutInMinutes,omitempty"`
	PublicIPAddresses    []armIDRef `json:"publicIpAddresses,omitempty"`
}

type natGatewayResponse struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Type       string                  `json:"type"`
	Location   string                  `json:"location"`
	Tags       map[string]string       `json:"tags,omitempty"`
	SKU        *natGatewaySKU          `json:"sku,omitempty"`
	Zones      []string                `json:"zones,omitempty"`
	Etag       string                  `json:"etag,omitempty"`
	Properties natGatewayResponseProps `json:"properties"`
}

type natGatewayResponseProps struct {
	ProvisioningState    string     `json:"provisioningState"`
	IdleTimeoutInMinutes int        `json:"idleTimeoutInMinutes,omitempty"`
	PublicIPAddresses    []armIDRef `json:"publicIpAddresses,omitempty"`
	// Subnets is read-only in real ARM: a subnet attaches to a NAT gateway by
	// setting its own natGateway property, not the other way around. It is
	// populated here by scanning subnets for that back-reference.
	Subnets []armIDRef `json:"subnets,omitempty"`
}

type natGatewayListResponse struct {
	Value []natGatewayResponse `json:"value"`
}

// routeNATGateway dispatches Microsoft.Network/natGateways requests.
//
//nolint:gocritic,dupl // rp is request-scoped; the per-resource routers are the same method switch over a distinct type by design
func (h *Handler) routeNATGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listNATGateways(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createNATGateway(w, r, rp)
	case http.MethodPatch:
		h.patchNATGateway(w, r, rp)
	case http.MethodGet:
		h.getNATGateway(w, r, rp)
	case http.MethodDelete:
		h.deleteNATGateway(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createNATGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req natGatewayRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	tags := mergeTags(req.Tags, armNATGatewayTag, rp.ResourceName)
	tags = mergeTags(tags, armNATGatewayRGTag, rp.ResourceGroup)

	cfg := netdriver.NATGatewayConfig{
		Tags:             tags,
		ConnectivityType: "public",
	}

	allocationID, err := h.resolveNATGatewayAllocation(r.Context(), rp, req.Properties.PublicIPAddresses)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	cfg.AllocationID = allocationID

	info, err := h.upsertNATGateway(r.Context(), rp.ResourceGroup, rp.ResourceName, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	body := h.natGatewayResponse(r.Context(), info, rp, loc)

	// SKU / zones / idleTimeoutInMinutes aren't part of the cross-cloud NAT
	// gateway model, so they don't survive a later GET; echo what the caller
	// just submitted on the create response, matching real ARM's create-time
	// body while keeping the driver AWS/Azure/GCP-portable.
	if req.SKU != nil {
		body.SKU = req.SKU
	}

	if len(req.Zones) > 0 {
		body.Zones = req.Zones
	}

	if req.Properties.IdleTimeoutInMinutes > 0 {
		body.Properties.IdleTimeoutInMinutes = req.Properties.IdleTimeoutInMinutes
	}

	writeAcceptedAsync(w, r, rp.Subscription, "natgw-create-"+rp.ResourceName, body)
}

// resolveNATGatewayAllocation resolves the first entry of a natGateways PUT
// body's properties.publicIpAddresses to the driver Elastic IP allocation id
// CreateNATGateway expects. Real Azure supports multiple public IPs per NAT
// gateway; this mock binds only the first, matching the cross-cloud driver
// model's single AllocationID. It rejects a public IP already claimed by a NIC
// or another NAT gateway (a static public IP binds to one owner), excluding this
// NAT gateway on an idempotent re-PUT.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) resolveNATGatewayAllocation(
	ctx context.Context, rp azurearm.ResourcePath, publicIPAddresses []armIDRef,
) (string, error) {
	if len(publicIPAddresses) == 0 {
		return "", nil
	}

	pipID := publicIPAddresses[0].ID

	pipRP, ok := azurearm.ParsePath(pipID)
	if !ok || pipRP.ResourceType != typePublicIP {
		return "", cerrors.Newf(cerrors.InvalidArgument, "malformed publicIpAddresses[0].id %q", pipID)
	}

	pip, err := findPublicIPByName(ctx, h.net, pipRP.ResourceGroup, pipRP.ResourceName)
	if err != nil {
		return "", err
	}

	if kind, owner, claimed := h.publicIPClaimant(
		ctx, pipID, pip.AllocationID, claimKindNAT, rp.ResourceGroup, rp.ResourceName,
	); claimed {
		return "", cerrors.Newf(cerrors.FailedPrecondition,
			"public IP %q is already associated with %s %q", pipID, kind, owner)
	}

	return pip.AllocationID, nil
}

// upsertNATGateway reuses an existing NAT gateway of the same name (idempotent
// re-PUT) or creates one.
func (h *Handler) upsertNATGateway(ctx context.Context, rg, name string, cfg netdriver.NATGatewayConfig) (*netdriver.NATGateway, error) {
	if existing, err := findNATGatewayByName(ctx, h.net, rg, name); err == nil {
		if meta, ok := h.azureMeta(); ok {
			if uerr := meta.UpdateAzureNATGateway(ctx, existing.ID, cfg.AllocationID, cfg.Tags); uerr != nil {
				return nil, uerr
			}

			if refreshed, rerr := findNATGatewayByName(ctx, h.net, rg, name); rerr == nil {
				existing = refreshed
			}
		}

		return existing, nil
	}

	return h.net.CreateNATGateway(ctx, cfg)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getNATGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNATGatewayByName(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.natGatewayResponse(r.Context(), info, rp, defaultLoc))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteNATGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNATGatewayByName(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// Real ARM refuses to delete a NAT gateway still associated with any subnet;
	// the subnet's natGateway reference must be dropped first.
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNATGateway, rp.ResourceName)

	if refs := h.natGatewaySubnetRefs(r.Context(), rp, id); len(refs) > 0 {
		azurearm.WriteError(w, http.StatusBadRequest, "InUseNatGatewayCannotBeDeleted",
			inUseMessage("Nat gateway", rp.ResourceName, refs))

		return
	}

	if err := h.net.DeleteNATGateway(r.Context(), info.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "natgw-delete-"+rp.ResourceName, nil)
}

//nolint:gocritic,dupl // rp is request-scoped; mirrors listPublicIPs over a distinct resource type by design
func (h *Handler) listNATGateways(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	infos, err := h.net.DescribeNATGateways(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := natGatewayListResponse{}

	for i := range infos {
		if rp.ResourceGroup != "" && !strings.EqualFold(tagOr(infos[i].Tags, armNATGatewayRGTag, ""), rp.ResourceGroup) {
			continue
		}

		scope := rp
		scope.ResourceGroup = tagOr(infos[i].Tags, armNATGatewayRGTag, rp.ResourceGroup)
		scope.ResourceName = tagOr(infos[i].Tags, armNATGatewayTag, infos[i].ID)
		out.Value = append(out.Value, h.natGatewayResponse(r.Context(), &infos[i], scope, defaultLoc))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// findNATGatewayByName matches by both the ARM name tag and, when rg is
// non-empty, the resource-group tag (see armNATGatewayRGTag).
func findNATGatewayByName(ctx context.Context, n netdriver.Networking, rg, name string) (*netdriver.NATGateway, error) {
	infos, err := n.DescribeNATGateways(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, armNATGatewayTag, "") != name {
			continue
		}

		if rg != "" && tagOr(infos[i].Tags, armNATGatewayRGTag, "") != rg {
			continue
		}

		return &infos[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "natGateway %s not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) natGatewayResponse(
	ctx context.Context, info *netdriver.NATGateway, rp azurearm.ResourcePath, location string,
) natGatewayResponse {
	if location == "" {
		location = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNATGateway, rp.ResourceName)

	return natGatewayResponse{
		ID:       id,
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeNATGateway,
		Location: location,
		Tags:     stripInternal(info.Tags),
		Properties: natGatewayResponseProps{
			ProvisioningState: "Succeeded",
			PublicIPAddresses: h.natGatewayPublicIPRefs(ctx, rp, info.AllocationID),
			Subnets:           h.natGatewaySubnetRefs(ctx, rp, id),
		},
	}
}

// natGatewayPublicIPRefs resolves the NAT gateway's bound Elastic IP
// allocation (if any) back to its own ARM resource id.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) natGatewayPublicIPRefs(ctx context.Context, rp azurearm.ResourcePath, allocationID string) []armIDRef {
	if allocationID == "" {
		return nil
	}

	eips, err := h.net.DescribeAddresses(ctx, []string{allocationID})
	if err != nil || len(eips) == 0 {
		return nil
	}

	pipName := tagOr(eips[0].Tags, armPublicIPTag, "")
	if pipName == "" {
		return nil
	}

	pipRG := tagOr(eips[0].Tags, armPublicIPRGTag, rp.ResourceGroup)
	id := azurearm.BuildResourceID(rp.Subscription, pipRG, providerName, typePublicIP, pipName)

	return []armIDRef{{ID: id}}
}

// natGatewaySubnetRefs scans subnets for the ones associated with this NAT
// gateway (their armSubnetNATTag matches natGatewayARMID), building each
// subnet's nested ARM resource id.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) natGatewaySubnetRefs(ctx context.Context, rp azurearm.ResourcePath, natGatewayARMID string) []armIDRef {
	subs, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil
	}

	vpcs, err := h.net.DescribeVPCs(ctx, nil)
	if err != nil {
		return nil
	}

	vpcNames := make(map[string]string, len(vpcs))
	for i := range vpcs {
		vpcNames[vpcs[i].ID] = tagOr(vpcs[i].Tags, armNameTag, vpcs[i].ID)
	}

	var out []armIDRef

	for i := range subs {
		if !strings.EqualFold(tagOr(subs[i].Tags, armSubnetNATTag, ""), natGatewayARMID) {
			continue
		}

		vnetName := vpcNames[subs[i].VPCID]
		subnetName := tagOr(subs[i].Tags, armSubnetTag, subs[i].ID)
		id := "/subscriptions/" + rp.Subscription +
			"/resourceGroups/" + rp.ResourceGroup +
			"/providers/" + providerName + "/" + typeVNet +
			"/" + vnetName + "/subnets/" + subnetName

		out = append(out, armIDRef{ID: id})
	}

	return out
}
