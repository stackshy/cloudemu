// Package vnet implements the Microsoft.Network ARM resources we expose:
// virtualNetworks, subnets (nested), and networkSecurityGroups. Real
// armnetwork clients hit this handler the same way they hit
// management.azure.com.
//
// Supported operations (compute parity with AWS EC2 networking):
//
//	PUT/GET/DELETE  /subscriptions/{s}/resourceGroups/{rg}/providers/
//	    Microsoft.Network/virtualNetworks/{name}
//	GET .../virtualNetworks                              — list in RG
//	PUT/GET/DELETE  .../virtualNetworks/{vn}/subnets/{n} — nested subnet
//	GET .../virtualNetworks/{vn}/subnets                 — list subnets
//	PUT/GET/DELETE  .../networkSecurityGroups/{name}     — NSG CRUD
//	GET .../networkSecurityGroups                        — list NSGs
//	PUT/GET/DELETE  .../networkInterfaces/{name}         — NIC CRUD
//	GET .../networkInterfaces                            — list NICs
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
	providerName     = "Microsoft.Network"
	typeVNet         = "virtualNetworks"
	typeNSG          = "networkSecurityGroups"
	typePublicIP     = "publicIPAddresses"
	typeLocations    = "locations"
	armNameTag       = "cloudemu:azureNetName"
	armSubnetTag     = "cloudemu:azureSubnet"
	armNSGTag        = "cloudemu:azureNSGName"
	armPublicIPTag   = "cloudemu:azurePublicIP"
	armPublicIPRGTag = "cloudemu:azurePublicIPResourceGroup"
	// armSubnetNATTag stores the full ARM resource id of the NAT gateway a
	// subnet is associated with (set via the subnet's own natGateway
	// property), so both the subnet response and the NAT gateway's
	// read-only subnets list can reflect the association.
	armSubnetNATTag = "cloudemu:azureSubnetNATGateway"
	defaultLoc      = "eastus"
	subResSubnets   = "subnets"
)

// Handler serves Microsoft.Network ARM requests against a networking driver.
type Handler struct {
	net netdriver.Networking
}

// New returns a network handler.
func New(n netdriver.Networking) *Handler {
	return &Handler{net: n}
}

// Matches returns true for ARM URLs targeting Microsoft.Network/virtualNetworks
// or networkSecurityGroups (and the locations operationStatuses sub-path used
// by async polling).
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rp.Provider != providerName {
		return false
	}

	switch rp.ResourceType {
	case typeVNet, typeNSG, typePublicIP, typeNIC, typeNATGateway, typeLocations:
		return true
	}

	return false
}

// ServeHTTP routes the request based on path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceType == typeLocations && rp.SubResource == "operationStatuses" {
		azurearm.WriteJSON(w, http.StatusOK, map[string]string{
			"name":   rp.SubResourceName,
			"status": "Succeeded",
		})

		return
	}

	switch rp.ResourceType {
	case typeVNet:
		h.routeVNet(w, r, rp)
	case typeNSG:
		h.routeNSG(w, r, rp)
	case typePublicIP:
		h.routePublicIP(w, r, rp)
	case typeNIC:
		h.routeNIC(w, r, rp)
	case typeNATGateway:
		h.routeNATGateway(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"unsupported resource type: "+rp.ResourceType)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	// Subnet sub-resource: SubResource="subnets", SubResourceName="{name}".
	if rp.SubResource == subResSubnets {
		h.routeSubnet(w, r, rp)
		return
	}

	if rp.ResourceName == "" {
		h.listVNets(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createVNet(w, r, rp)
	case http.MethodGet:
		h.getVNet(w, r, rp)
	case http.MethodDelete:
		h.deleteVNet(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeSubnet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		h.listSubnets(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createSubnet(w, r, rp)
	case http.MethodGet:
		h.getSubnet(w, r, rp)
	case http.MethodDelete:
		h.deleteSubnet(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeNSG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listNSGs(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createNSG(w, r, rp)
	case http.MethodGet:
		h.getNSG(w, r, rp)
	case http.MethodDelete:
		h.deleteNSG(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// VirtualNetwork operations.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req vnetRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	prefixes := vnetPrefixes(&req)

	info, err := h.upsertVNet(r.Context(), rp.ResourceName, prefixes, req.Tags)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	if meta, ok := h.azureMeta(); ok {
		_ = meta.PutAzureVNetMetadata(r.Context(), info.ID,
			netdriver.AzureVNetMetadata{Location: loc, AddressPrefixes: prefixes})
	}

	// Materialize any inline subnets so they become real, addressable children
	// (Subnets.Get resolves them) rather than a body-only echo.
	if err := h.materializeSubnets(r.Context(), info.ID, req.Properties.Subnets); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "vnet-create-"+rp.ResourceName, h.vnetResponse(r.Context(), info, rp))
}

// vnetPrefixes extracts the full address-prefix list from a vnet request.
func vnetPrefixes(req *vnetRequest) []string {
	if req.Properties.AddressSpace == nil {
		return nil
	}

	return req.Properties.AddressSpace.AddressPrefixes
}

// upsertVNet reuses an existing virtual network of the same name (so a repeated
// PUT updates in place rather than creating a duplicate) or creates a new one.
func (h *Handler) upsertVNet(ctx context.Context, name string, prefixes []string,
	tags map[string]string,
) (*netdriver.VPCInfo, error) {
	if existing, err := findVNetByName(ctx, h.net, name); err == nil {
		if len(tags) > 0 {
			_ = h.net.UpdateVPCTags(ctx, existing.ID, tags)

			if refreshed, rerr := findVNetByName(ctx, h.net, name); rerr == nil {
				existing = refreshed
			}
		}

		return existing, nil
	}

	cidr := ""
	if len(prefixes) > 0 {
		cidr = prefixes[0]
	}

	return h.net.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: cidr,
		Tags:      mergeTags(tags, armNameTag, name),
	})
}

// materializeSubnets creates the inline subnets carried in a vnet PUT body,
// skipping any that already exist (idempotent re-PUT).
func (h *Handler) materializeSubnets(ctx context.Context, vpcID string, subs []subnetRequest) error {
	if len(subs) == 0 {
		return nil
	}

	existing, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return err
	}

	have := make(map[string]struct{})

	for i := range existing {
		if existing[i].VPCID == vpcID {
			have[tagOr(existing[i].Tags, armSubnetTag, "")] = struct{}{}
		}
	}

	for i := range subs {
		if subs[i].Name == "" {
			continue
		}

		if _, ok := have[subs[i].Name]; ok {
			continue
		}

		if _, err := h.net.CreateSubnet(ctx, netdriver.SubnetConfig{
			VPCID:     vpcID,
			CIDRBlock: subs[i].Properties.AddressPrefix,
			Tags:      mergeTags(nil, armSubnetTag, subs[i].Name),
		}); err != nil {
			return err
		}
	}

	return nil
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.vnetResponse(r.Context(), info, rp))
}

//nolint:gocritic,dupl // rp is request-scoped; mirrors listNSGs over a distinct resource type by design
func (h *Handler) listVNets(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	infos, err := h.net.DescribeVPCs(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := vnetListResponse{}

	for i := range infos {
		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, armNameTag, infos[i].ID)
		out.Value = append(out.Value, h.vnetResponse(r.Context(), &infos[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// A virtual network with a subnet still bound to a network interface cannot
	// be deleted; ARM answers 400 with this code, which armnetwork switches on.
	if h.vnetSubnetInUse(r.Context(), rp.ResourceName) {
		azurearm.WriteError(w, http.StatusBadRequest, "InUseSubnetCannotBeDeleted",
			"virtual network "+rp.ResourceName+" has a subnet in use by a network interface")

		return
	}

	// Cascade-delete the child subnets so they stop being globally addressable
	// once their parent network is gone.
	h.deleteChildSubnets(r.Context(), info.ID)

	if meta, ok := h.azureMeta(); ok {
		meta.DeleteAzureVNetMetadata(r.Context(), info.ID)
	}

	if err := h.net.DeleteVPC(r.Context(), info.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "vnet-delete-"+rp.ResourceName, nil)
}

// deleteChildSubnets removes every subnet that belongs to the given vnet.
func (h *Handler) deleteChildSubnets(ctx context.Context, vpcID string) {
	subs, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return
	}

	for i := range subs {
		if subs[i].VPCID == vpcID {
			_ = h.net.DeleteSubnet(ctx, subs[i].ID)
		}
	}
}

// vnetSubnetInUse reports whether any network interface's ipConfiguration
// references a subnet of the named virtual network.
func (h *Handler) vnetSubnetInUse(ctx context.Context, vnetName string) bool {
	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		return false
	}

	nics, err := svc.ListNetworkInterfaces(ctx, "")
	if err != nil {
		return false
	}

	for i := range nics {
		for j := range nics[i].IPConfigs {
			if subnetVNetName(nics[i].IPConfigs[j].SubnetID) == vnetName {
				return true
			}
		}
	}

	return false
}

// subnetVNetName extracts the virtual-network name from an ARM subnet resource
// id (.../virtualNetworks/{vnet}/subnets/{name}).
func subnetVNetName(subnetID string) string {
	sp, ok := azurearm.ParsePath(subnetID)
	if !ok || sp.ResourceType != typeVNet {
		return ""
	}

	return sp.ResourceName
}

// Subnet operations.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createSubnet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	vnet, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req subnetRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	var natGatewayID string

	if req.Properties.NatGateway != nil {
		ngRP, ok := azurearm.ParsePath(req.Properties.NatGateway.ID)
		if !ok || ngRP.ResourceType != typeNATGateway {
			azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "malformed natGateway id")
			return
		}

		if _, ngErr := findNATGatewayByName(r.Context(), h.net, ngRP.ResourceGroup, ngRP.ResourceName); ngErr != nil {
			azurearm.WriteCErr(w, ngErr)
			return
		}

		natGatewayID = req.Properties.NatGateway.ID
	}

	info, err := h.upsertSubnet(r.Context(), vnet.ID, rp.SubResourceName, req.Properties.AddressPrefix, natGatewayID)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	body := toSubnetResponse(info, rp)

	writeAcceptedAsync(w, r, rp.Subscription, "subnet-create-"+rp.SubResourceName, body)
}

// upsertSubnet creates the named subnet, or — when it already exists — merges
// in a NAT gateway reference so a subnet can be attached to a NAT gateway via
// a second PUT (armnetwork's SubnetsClient.BeginCreateOrUpdate, the real ARM
// mechanism: the association lives on the subnet's natGateway property, not
// on the NAT gateway). Other subnet properties are not merged on update,
// matching this handler's existing create-only behavior for subnets.
func (h *Handler) upsertSubnet(ctx context.Context, vpcID, name, cidr, natGatewayID string) (*netdriver.SubnetInfo, error) {
	if existing, err := findSubnetInVNet(ctx, h.net, vpcID, name); err == nil {
		if natGatewayID == "" {
			return existing, nil
		}

		if err := h.net.UpdateSubnetTags(ctx, existing.ID, map[string]string{armSubnetNATTag: natGatewayID}); err != nil {
			return nil, err
		}

		existing.Tags = mergeTags(existing.Tags, armSubnetNATTag, natGatewayID)

		return existing, nil
	}

	tags := mergeTags(nil, armSubnetTag, name)
	if natGatewayID != "" {
		tags = mergeTags(tags, armSubnetNATTag, natGatewayID)
	}

	return h.net.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID:     vpcID,
		CIDRBlock: cidr,
		Tags:      tags,
	})
}

// findSubnetInVNet resolves a subnet by (vnet, name) — subnet names are only
// unique within a vnet — mirroring subnetCIDR's scoped lookup.
func findSubnetInVNet(ctx context.Context, n netdriver.Networking, vpcID, name string) (*netdriver.SubnetInfo, error) {
	subs, err := n.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range subs {
		if subs[i].VPCID == vpcID && tagOr(subs[i].Tags, armSubnetTag, "") == name {
			return &subs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "subnet %s not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getSubnet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findSubnetByName(r.Context(), h.net, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toSubnetResponse(info, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listSubnets(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	infos, err := h.net.DescribeSubnets(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := subnetListResponse{}

	for i := range infos {
		scope := rp
		scope.SubResourceName = tagOr(infos[i].Tags, armSubnetTag, infos[i].ID)
		out.Value = append(out.Value, toSubnetResponse(&infos[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteSubnet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findSubnetByName(r.Context(), h.net, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.net.DeleteSubnet(r.Context(), info.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "subnet-delete-"+rp.SubResourceName, nil)
}

// NSG operations.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createNSG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req nsgRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.upsertNSG(r.Context(), rp.ResourceName, req.Tags)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	if meta, ok := h.azureMeta(); ok {
		_ = meta.PutAzureNSGMetadata(r.Context(), info.ID, netdriver.AzureNSGMetadata{
			Location:      loc,
			SecurityRules: toAzureNSGRules(req.Properties.SecurityRules),
		})
	}

	writeAcceptedAsync(w, r, rp.Subscription, "nsg-create-"+rp.ResourceName, h.nsgResponse(r.Context(), info, rp))
}

// upsertNSG reuses an existing NSG of the same name (idempotent re-PUT) or
// creates one, anchoring the driver security group to any VPC (creating a
// synthetic one when none exists, as the driver requires a VPC id).
func (h *Handler) upsertNSG(ctx context.Context, name string,
	tags map[string]string,
) (*netdriver.SecurityGroupInfo, error) {
	if existing, err := findNSGByName(ctx, h.net, name); err == nil {
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

	return h.net.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		Name:  name,
		VPCID: anchor,
		Tags:  mergeTags(tags, armNSGTag, name),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getNSG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNSGByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.nsgResponse(r.Context(), info, rp))
}

//nolint:gocritic,dupl // rp is request-scoped; mirrors listVNets over a distinct resource type by design
func (h *Handler) listNSGs(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	infos, err := h.net.DescribeSecurityGroups(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := nsgListResponse{}

	for i := range infos {
		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, armNSGTag, infos[i].ID)
		out.Value = append(out.Value, h.nsgResponse(r.Context(), &infos[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteNSG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNSGByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if meta, ok := h.azureMeta(); ok {
		meta.DeleteAzureNSGMetadata(r.Context(), info.ID)
	}

	if err := h.net.DeleteSecurityGroup(r.Context(), info.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "nsg-delete-"+rp.ResourceName, nil)
}

// PublicIP operations.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routePublicIP(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listPublicIPs(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createPublicIP(w, r, rp)
	case http.MethodGet:
		h.getPublicIP(w, r, rp)
	case http.MethodDelete:
		h.deletePublicIP(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createPublicIP(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req publicIPRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	sku := ""
	if req.SKU != nil {
		sku = req.SKU.Name
	}

	tags := mergeTags(req.Tags, armPublicIPTag, rp.ResourceName)
	tags = mergeTags(tags, armPublicIPRGTag, rp.ResourceGroup)

	cfg := netdriver.ElasticIPConfig{
		SKU:                sku,
		AllocationMethod:   req.Properties.PublicIPAllocationMethod,
		Tags:               tags,
		Zones:              req.Zones,
		IdleTimeoutMinutes: req.Properties.IdleTimeoutInMinutes,
	}

	if req.Properties.DNSSettings != nil {
		cfg.DNSDomainNameLabel = req.Properties.DNSSettings.DomainNameLabel
	}

	info, err := h.net.AllocateAddress(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	body := h.toPublicIPResponse(r.Context(), info, rp, loc)

	writeAcceptedAsync(w, r, rp.Subscription, "publicip-create-"+rp.ResourceName, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getPublicIP(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findPublicIPByName(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toPublicIPResponse(r.Context(), info, rp, defaultLoc))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deletePublicIP(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findPublicIPByName(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// A NIC's ipConfiguration reference doesn't go through
	// AssociateAddress/eip.AssociationID (that's reserved for AWS-style
	// instance/NAT-gateway attachment), so it needs its own in-use check here
	// — the same scan the ipConfiguration back-reference already does.
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typePublicIP, rp.ResourceName)

	if h.publicIPConfigurationRef(r.Context(), rp.Subscription, id) != nil {
		azurearm.WriteError(w, http.StatusBadRequest, "PublicIPAddressCannotBeDeleted",
			"public IP "+rp.ResourceName+" is still referenced by a network interface ipConfiguration")

		return
	}

	if err := h.net.ReleaseAddress(r.Context(), info.AllocationID); err != nil {
		// A public IP still bound to a NIC/NAT gateway: ARM answers 400 with
		// this specific code, not the generic 409 WriteCErr would emit.
		if cerrors.IsFailedPrecondition(err) {
			azurearm.WriteError(w, http.StatusBadRequest, "PublicIPAddressCannotBeDeleted", err.Error())
			return
		}

		azurearm.WriteCErr(w, err)

		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "publicip-delete-"+rp.ResourceName, nil)
}

// listPublicIPs lists public IPs in rp's resource group, or every public IP in
// the subscription when the request path carries no resource group (ARM
// supports both .../resourceGroups/{rg}/.../publicIPAddresses and the
// subscription-wide .../providers/Microsoft.Network/publicIPAddresses).
//
//nolint:gocritic,dupl // rp is request-scoped; mirrors listNATGateways over a distinct resource type by design
func (h *Handler) listPublicIPs(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	infos, err := h.net.DescribeAddresses(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := publicIPListResponse{}

	for i := range infos {
		if rp.ResourceGroup != "" && tagOr(infos[i].Tags, armPublicIPRGTag, "") != rp.ResourceGroup {
			continue
		}

		scope := rp
		scope.ResourceGroup = tagOr(infos[i].Tags, armPublicIPRGTag, rp.ResourceGroup)
		scope.ResourceName = tagOr(infos[i].Tags, armPublicIPTag, infos[i].AllocationID)
		out.Value = append(out.Value, h.toPublicIPResponse(r.Context(), &infos[i], scope, defaultLoc))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// Lookup helpers — driver indexes by its own ID, so we match by tag.

func findVNetByName(ctx context.Context, n netdriver.Networking, name string) (*netdriver.VPCInfo, error) {
	infos, err := n.DescribeVPCs(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, armNameTag, "") == name {
			return &infos[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "virtualNetwork %s not found", name)
}

func findSubnetByName(ctx context.Context, n netdriver.Networking, name string) (*netdriver.SubnetInfo, error) {
	infos, err := n.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, armSubnetTag, "") == name {
			return &infos[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "subnet %s not found", name)
}

func findNSGByName(ctx context.Context, n netdriver.Networking, name string) (*netdriver.SecurityGroupInfo, error) {
	infos, err := n.DescribeSecurityGroups(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, armNSGTag, "") == name {
			return &infos[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "networkSecurityGroup %s not found", name)
}

// findPublicIPByName matches by both the ARM name tag and, when rg is
// non-empty, the resource-group tag — a public IP in a different resource
// group with the same name must not match (see armPublicIPRGTag).
func findPublicIPByName(ctx context.Context, n netdriver.Networking, rg, name string) (*netdriver.ElasticIP, error) {
	infos, err := n.DescribeAddresses(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, armPublicIPTag, "") != name {
			continue
		}

		if rg != "" && tagOr(infos[i].Tags, armPublicIPRGTag, "") != rg {
			continue
		}

		return &infos[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "publicIPAddress %s not found", name)
}

// Response shaping helpers.

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) vnetResponse(ctx context.Context, info *netdriver.VPCInfo, rp azurearm.ResourcePath) vnetResponse {
	location, prefixes := h.vnetLocationPrefixes(ctx, info)

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeVNet, rp.ResourceName)

	out := vnetResponse{
		ID:       id,
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeVNet,
		Location: location,
		Etag:     etagOf(id),
		Tags:     stripInternal(info.Tags),
		Properties: vnetResponseProps{
			ProvisioningState: "Succeeded",
			AddressSpace:      &addressSpace{AddressPrefixes: prefixes},
		},
	}

	subs, _ := h.net.DescribeSubnets(ctx, nil)

	for i := range subs {
		if subs[i].VPCID == info.ID {
			s := subs[i]
			scope := rp
			scope.SubResource = subResSubnets
			scope.SubResourceName = tagOr(s.Tags, armSubnetTag, s.ID)
			out.Properties.Subnets = append(out.Properties.Subnets, toSubnetResponse(&s, scope))
		}
	}

	return out
}

// vnetLocationPrefixes returns the region and full address-prefix list for a
// vnet, reading the Azure metadata store when available and falling back to the
// cross-cloud CIDR.
func (h *Handler) vnetLocationPrefixes(ctx context.Context, info *netdriver.VPCInfo) (location string, prefixes []string) {
	location = defaultLoc
	prefixes = []string{info.CIDRBlock}

	meta, ok := h.azureMeta()
	if !ok {
		return location, prefixes
	}

	md, found := meta.GetAzureVNetMetadata(ctx, info.ID)
	if !found {
		return location, prefixes
	}

	if md.Location != "" {
		location = md.Location
	}

	if len(md.AddressPrefixes) > 0 {
		prefixes = md.AddressPrefixes
	}

	return location, prefixes
}

//nolint:gocritic // rp is a request-scoped value
func toSubnetResponse(info *netdriver.SubnetInfo, rp azurearm.ResourcePath) subnetResponse {
	name := tagOr(info.Tags, armSubnetTag, rp.SubResourceName)
	id := "/subscriptions/" + rp.Subscription +
		"/resourceGroups/" + rp.ResourceGroup +
		"/providers/" + providerName + "/" + typeVNet +
		"/" + rp.ResourceName + "/subnets/" + name

	out := subnetResponse{
		ID:   id,
		Name: name,
		Etag: etagOf(id),
		Properties: subnetResponseProps{
			ProvisioningState: "Succeeded",
			AddressPrefix:     info.CIDRBlock,
		},
	}

	if ngID := tagOr(info.Tags, armSubnetNATTag, ""); ngID != "" {
		out.Properties.NatGateway = &armIDRef{ID: ngID}
	}

	return out
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) nsgResponse(ctx context.Context, info *netdriver.SecurityGroupInfo, rp azurearm.ResourcePath) nsgResponse {
	location := defaultLoc

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNSG, rp.ResourceName)

	var rules []securityRule

	if meta, ok := h.azureMeta(); ok {
		if md, found := meta.GetAzureNSGMetadata(ctx, info.ID); found {
			if md.Location != "" {
				location = md.Location
			}

			rules = fromAzureNSGRules(id, md.SecurityRules)
		}
	}

	return nsgResponse{
		ID:       id,
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeNSG,
		Location: location,
		Etag:     etagOf(id),
		Tags:     stripInternal(info.Tags),
		Properties: nsgResponseProps{
			ProvisioningState:    "Succeeded",
			SecurityRules:        rules,
			DefaultSecurityRules: defaultSecurityRules(id),
		},
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) toPublicIPResponse(
	ctx context.Context, info *netdriver.ElasticIP, rp azurearm.ResourcePath, location string,
) publicIPResponse {
	if location == "" {
		location = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typePublicIP, rp.ResourceName)

	out := publicIPResponse{
		ID:       id,
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typePublicIP,
		Location: location,
		Tags:     stripInternal(info.Tags),
		Zones:    info.Zones,
		Properties: publicIPRespProps{
			ProvisioningState:        "Succeeded",
			PublicIPAllocationMethod: info.AllocationMethod,
			IPAddress:                info.PublicIP,
			IdleTimeoutInMinutes:     info.IdleTimeoutMinutes,
		},
	}

	if info.SKU != "" {
		out.SKU = &publicIPSKU{Name: info.SKU}
	}

	if info.DNSDomainNameLabel != "" {
		out.Properties.DNSSettings = &publicIPDNSSettings{
			DomainNameLabel: info.DNSDomainNameLabel,
			FQDN:            info.DNSFQDN,
		}
	}

	out.Properties.IPConfiguration = h.publicIPConfigurationRef(ctx, rp.Subscription, id)

	return out
}

// publicIPConfigurationRef scans network interfaces for an ipConfiguration
// referencing the public IP with the given ARM id, matching the back-reference
// a real publicIPAddresses GET reports once a NIC attaches the address (mirrors
// vnetResponse's subnet-by-VPCID scan and vnetSubnetInUse's NIC scan).
func (h *Handler) publicIPConfigurationRef(ctx context.Context, sub, publicIPARMID string) *armIDRef {
	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		return nil
	}

	nics, err := svc.ListNetworkInterfaces(ctx, "")
	if err != nil {
		return nil
	}

	for i := range nics {
		for j := range nics[i].IPConfigs {
			if strings.EqualFold(nics[i].IPConfigs[j].PublicIPID, publicIPARMID) {
				return &armIDRef{ID: ipConfigResourceID(sub, nics[i].ResourceGroup, nics[i].Name, nics[i].IPConfigs[j].Name)}
			}
		}
	}

	return nil
}

// writeAcceptedAsync replies for create/delete operations. armnetwork's
// poller expects either:
//   - a sync 200 OK with the resource body whose ProvisioningState is
//     terminal (Succeeded), OR
//   - a 202 Accepted with Azure-AsyncOperation pointing to a polling URL
//     that returns Succeeded.
//
// We use sync-200-with-body for creates (when body is non-nil) and
// 202-with-async-header for deletes.
func writeAcceptedAsync(w http.ResponseWriter, r *http.Request, sub, opID string, body any) {
	if body != nil {
		azurearm.WriteJSON(w, http.StatusOK, body)
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	statusURL := scheme + "://" + r.Host +
		"/subscriptions/" + sub +
		"/providers/Microsoft.Network/locations/eastus/operationStatuses/" + opID +
		"?api-version=2023-09-01"

	w.Header().Set("Azure-AsyncOperation", statusURL)
	w.Header().Set("Location", statusURL)
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusAccepted)
}

// Tag helpers.

func mergeTags(in map[string]string, key, val string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[key] = val

	return out
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

func stripInternal(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if strings.HasPrefix(k, "cloudemu:") {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
