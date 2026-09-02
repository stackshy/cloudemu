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
//	PUT/GET/DELETE  .../routeTables/{name}               — route table CRUD
//	GET .../routeTables                                  — list route tables
//	PUT/GET/DELETE  .../networkInterfaces/{name}         — NIC CRUD
//	GET .../networkInterfaces                            — list NICs
//	PUT/GET/DELETE  .../virtualNetworks/{vn}/virtualNetworkPeerings/{n} — peering CRUD
//	GET .../virtualNetworks/{vn}/virtualNetworkPeerings  — list peerings
package vnet

import (
	"context"
	"maps"
	"net/http"
	"strings"
	"sync"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	providerName     = "Microsoft.Network"
	typeVNet         = "virtualNetworks"
	typeNSG          = "networkSecurityGroups"
	typeRouteTable   = "routeTables"
	typePublicIP     = "publicIPAddresses"
	typeLocations    = "locations"
	armNameTag       = "cloudemu:azureNetName"
	armSubnetTag     = "cloudemu:azureSubnet"
	armNSGTag        = "cloudemu:azureNSGName"
	armPublicIPTag   = "cloudemu:azurePublicIP"
	armPublicIPRGTag = "cloudemu:azurePublicIPResourceGroup"
	// armVNetRGTag / armNSGRGTag record the resource group a virtual network or
	// network security group was created under. The cross-cloud networking
	// driver has a single flat namespace, so without this a resource is globally
	// name-addressable and a GET/DELETE under the wrong resource group would
	// resolve it and echo back that wrong group. Scoping the direct CRUD lookups
	// by this tag makes a same-named resource in a different group a 404, the
	// same way NAT gateways (armNATGatewayRGTag) and public IPs (armPublicIPRGTag)
	// are already scoped.
	armVNetRGTag = "cloudemu:azureVNetResourceGroup"
	armNSGRGTag  = "cloudemu:azureNSGResourceGroup"
	// armSyntheticAnchorTag marks a VPC that was fabricated purely to satisfy the
	// networking driver's mandatory VPCID when creating a standalone NSG or route
	// table (which are top-level, VNet-independent resources in Azure). Such an
	// anchor is internal plumbing, not a user resource, so it is excluded from the
	// virtualNetworks list/get responses.
	armSyntheticAnchorTag = "cloudemu:azureSyntheticVNetAnchor"
	// armSubnetNATTag stores the full ARM resource id of the NAT gateway a
	// subnet is associated with (set via the subnet's own natGateway
	// property), so both the subnet response and the NAT gateway's
	// read-only subnets list can reflect the association.
	armSubnetNATTag = "cloudemu:azureSubnetNATGateway"
	// armSubnetNSGTag stores the full ARM resource id of the network security
	// group a subnet is associated with (set via the subnet's own
	// networkSecurityGroup property), mirroring armSubnetNATTag.
	armSubnetNSGTag = "cloudemu:azureSubnetNSG"
	// armSubnetRouteTableTag stores the full ARM resource id of the route table a
	// subnet is associated with (set via the subnet's own routeTable property),
	// mirroring armSubnetNSGTag.
	armSubnetRouteTableTag = "cloudemu:azureSubnetRouteTable"
	// armRouteTableTag / armRouteTableRGTag record a route table's ARM name and
	// resource group on its driver anchor so it is addressable by (rg, name), the
	// same way armNSGTag / armNSGRGTag scope network security groups.
	armRouteTableTag    = "cloudemu:azureRouteTableName"
	armRouteTableRGTag  = "cloudemu:azureRouteTableResourceGroup"
	defaultLoc          = "eastus"
	subResSubnets       = "subnets"
	subResSecurityRules = "securityRules"
	subResVNetPeerings  = "virtualNetworkPeerings"
	subResCheckIPAvail  = "CheckIPAddressAvailability"
)

// Handler serves Microsoft.Network ARM requests against a networking driver.
type Handler struct {
	// patchMu serializes the read-modify-write UpdateTags PATCH handlers so two
	// concurrent PATCHes to the same resource cannot drop a write (the driver's
	// Get and Put are individually locked, but the get-then-put across them is
	// not atomic). It guards only that get-modify-put window.
	patchMu sync.Mutex
	net     netdriver.Networking
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
	case typeVNet, typeNSG, typeRouteTable, typePublicIP, typePublicIPPrefix, typeNIC, typeNATGateway, typeASG,
		typeVNGateway, typeLNGateway, typeConnection, typePrivateEndpoint, typePrivateLinkService, typeLocations:
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

	if serveLocationsOperationStatus(w, rp) {
		return
	}

	h.routeByResourceType(w, r, rp)
}

// routeByResourceType dispatches to the per-type router. Split out of ServeHTTP
// so the dispatch stays under the cyclomatic-complexity gate as resource types
// are added (the same reason serveLocationsOperationStatus is separate).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeByResourceType(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	switch rp.ResourceType {
	case typeVNet:
		h.routeVNet(w, r, rp)
	case typeNSG:
		h.routeNSG(w, r, rp)
	case typeRouteTable:
		h.routeRouteTable(w, r, rp)
	case typePublicIP:
		h.routePublicIP(w, r, rp)
	case typePublicIPPrefix:
		h.routePublicIPPrefix(w, r, rp)
	case typeNIC:
		h.routeNIC(w, r, rp)
	case typeNATGateway:
		h.routeNATGateway(w, r, rp)
	case typeASG:
		h.routeASG(w, r, rp)
	default:
		if h.routeOptionalResourceType(w, r, rp) {
			return
		}

		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"unsupported resource type: "+rp.ResourceType)
	}
}

// routeOptionalResourceType dispatches the Azure-only resource types served
// through type-asserted networking-driver capabilities (site-to-site VPN
// gateways, Private Link). Split out of routeByResourceType so that switch stays
// under the cyclomatic-complexity gate as capabilities are added. It returns
// true when it handled the request.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeOptionalResourceType(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) bool {
	return h.routeGatewayResourceType(w, r, rp) || h.routePrivateLinkResourceType(w, r, rp)
}

// routeGatewayResourceType dispatches the site-to-site VPN resource types
// (virtualNetworkGateways, localNetworkGateways, connections). Split out of
// routeByResourceType so its dispatch switch stays under the cyclomatic-complexity
// gate. It returns true when it handled the request.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeGatewayResourceType(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) bool {
	switch rp.ResourceType {
	case typeVNGateway:
		h.routeVNGateway(w, r, rp)
	case typeLNGateway:
		h.routeLNGateway(w, r, rp)
	case typeConnection:
		h.routeConnection(w, r, rp)
	default:
		return false
	}

	return true
}

// serveLocationsOperationStatus answers the locations/operationStatuses poll
// URL that async (202) operations point at, reporting Succeeded. It returns true
// when it handled the request. Split out of ServeHTTP so the main dispatch stays
// under the cyclomatic-complexity gate as resource types are added.
//
//nolint:gocritic // rp is a request-scoped value
func serveLocationsOperationStatus(w http.ResponseWriter, rp azurearm.ResourcePath) bool {
	if rp.ResourceType != typeLocations || rp.SubResource != "operationStatuses" {
		return false
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]string{
		"name":   rp.SubResourceName,
		"status": "Succeeded",
	})

	return true
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	// Subnet sub-resource: SubResource="subnets", SubResourceName="{name}".
	if rp.SubResource == subResSubnets {
		h.routeSubnet(w, r, rp)
		return
	}

	// VirtualNetworkPeerings sub-resource (VirtualNetworkPeeringsClient):
	// SubResource="virtualNetworkPeerings", SubResourceName="{peeringName}".
	// Routed before the whole-VNet method switch below — BLOCKER fix: without
	// this, a peering PUT/GET/DELETE fell through to createVNet/getVNet/deleteVNet
	// keyed on rp.ResourceName (the parent VNet's own name), so a peering DELETE
	// deleted the entire virtual network instead of just the peering.
	if rp.SubResource == subResVNetPeerings {
		h.routeVNetPeering(w, r, rp)
		return
	}

	// CheckIPAddressAvailability is a GET action on the vnet itself
	// (.../virtualNetworks/{name}/CheckIPAddressAvailability?ipAddress=...),
	// not a nested resource — route it before the plain vnet GET/PUT/DELETE
	// switch below, or it falls through and answers with the vnet body instead
	// of an IPAddressAvailabilityResult.
	if strings.EqualFold(rp.SubResource, subResCheckIPAvail) {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.checkIPAddressAvailability(w, r, rp)

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

	// SecurityRule sub-resource (SecurityRulesClient / azurerm_network_security_rule):
	// SubResource="securityRules", SubResourceName="{ruleName}". Routed before
	// the whole-NSG method switch below so a standalone rule PUT/GET/DELETE
	// never hits createNSG/getNSG/deleteNSG, which are scoped to rp.ResourceName
	// (the NSG's own name, not a rule).
	if rp.SubResource == subResSecurityRules {
		h.routeSecurityRule(w, r, rp)
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

	info, err := h.upsertVNet(r.Context(), rp.ResourceGroup, rp.ResourceName, prefixes, req.Tags)
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
	// (Subnets.Get resolves them) rather than a body-only echo, and make this
	// PUT authoritative over the whole subnets collection.
	if err := h.materializeSubnets(r.Context(), info.ID, req.Properties.Subnets); err != nil {
		// A subnet this PUT's body omits but that's still in use by a NIC: ARM
		// answers 400 InUseSubnetCannotBeDeleted, not the generic 409 WriteCErr
		// would emit for FailedPrecondition.
		if cerrors.IsFailedPrecondition(err) {
			azurearm.WriteError(w, http.StatusBadRequest, "InUseSubnetCannotBeDeleted", err.Error())
			return
		}

		writeSubnetValidationError(w, err)

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
func (h *Handler) upsertVNet(ctx context.Context, rg, name string, prefixes []string,
	tags map[string]string,
) (*netdriver.VPCInfo, error) {
	if existing, err := findVNetInGroup(ctx, h.net, rg, name); err == nil {
		if len(tags) > 0 {
			_ = h.net.UpdateVPCTags(ctx, existing.ID, tags)

			if refreshed, rerr := findVNetInGroup(ctx, h.net, rg, name); rerr == nil {
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
		Tags:      mergeTags(mergeTags(tags, armNameTag, name), armVNetRGTag, rg),
	})
}

// materializeSubnets makes a whole-VNet PUT authoritative over its subnets
// collection, matching real ARM's full-replace semantics for
// VirtualNetworksClient.BeginCreateOrUpdate: a subnet present in this PUT's
// body but not yet stored is created; a subnet already stored but omitted
// from this PUT's body is deleted (refused with the same in-use guard
// deleteSubnet applies, so a body that silently drops a live subnet fails the
// whole PUT rather than orphaning a NIC's reference).
//
// subs == nil (the properties.subnets key was absent from the JSON body
// entirely, not sent as []) leaves every existing subnet untouched — Azure
// added this exact carve-out so tag-only/address-space-only updates don't
// need to round-trip the subnet list. See "Azure Virtual Network now supports
// updates without subnet property" (Microsoft Community Hub). An explicit
// empty array, by contrast, is a request to delete every subnet — nil vs.
// non-nil is exactly what encoding/json's Unmarshal already distinguishes for
// a JSON array field, so no extra presence-tracking is needed here.
func (h *Handler) materializeSubnets(ctx context.Context, vpcID string, subs []subnetRequest) error {
	if subs == nil {
		return nil
	}

	existing, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return err
	}

	haveByName := make(map[string]netdriver.SubnetInfo, len(existing))

	for i := range existing {
		if existing[i].VPCID == vpcID {
			haveByName[tagOr(existing[i].Tags, armSubnetTag, "")] = existing[i]
		}
	}

	wanted, err := h.upsertWantedSubnets(ctx, vpcID, subs, haveByName)
	if err != nil {
		return err
	}

	return h.deleteOmittedSubnets(ctx, haveByName, wanted)
}

// upsertWantedSubnets creates every named subnet in subs that isn't already in
// haveByName and applies an in-place CreateOrUpdate to those that are (changed
// address prefix, replaced NSG / NAT gateway associations), validating each
// CIDR first, and returns the set of names this PUT's body wants — the input
// deleteOmittedSubnets needs to find what to remove.
func (h *Handler) upsertWantedSubnets(
	ctx context.Context, vpcID string, subs []subnetRequest, haveByName map[string]netdriver.SubnetInfo,
) (map[string]struct{}, error) {
	wanted := make(map[string]struct{}, len(subs))

	for i := range subs {
		if subs[i].Name == "" {
			continue
		}

		wanted[subs[i].Name] = struct{}{}

		if existing, ok := haveByName[subs[i].Name]; ok {
			if err := h.updateInlineSubnet(ctx, vpcID, &subs[i], &existing); err != nil {
				return nil, err
			}

			continue
		}

		if verr := h.validateSubnetCIDR(ctx, vpcID, subs[i].Name, subs[i].Properties.AddressPrefix); verr != nil {
			return nil, verr
		}

		if _, err := h.net.CreateSubnet(ctx, netdriver.SubnetConfig{
			VPCID:     vpcID,
			CIDRBlock: subs[i].Properties.AddressPrefix,
			Tags:      inlineSubnetTags(&subs[i]),
		}); err != nil {
			return nil, err
		}
	}

	return wanted, nil
}

// updateInlineSubnet applies a whole-VNet PUT's inline subnet body to an
// existing subnet: it validates and changes the address prefix when it differs
// and REPLACES the NSG / NAT gateway associations from the body (an omitted
// reference clears the association), the same full-replacement semantics as the
// standalone subnet CreateOrUpdate.
func (h *Handler) updateInlineSubnet(
	ctx context.Context, vpcID string, sub *subnetRequest, existing *netdriver.SubnetInfo,
) error {
	cidr := sub.Properties.AddressPrefix
	if cidr != "" && cidr != existing.CIDRBlock {
		if verr := h.validateSubnetCIDR(ctx, vpcID, sub.Name, cidr); verr != nil {
			return verr
		}
	}

	natID := inlineRefID(sub.Properties.NatGateway)
	nsgID := inlineRefID(sub.Properties.NetworkSecurityGroup)
	routeTableID := inlineRefID(sub.Properties.RouteTable)

	_, err := h.updateExistingSubnet(ctx, existing, cidr, natID, nsgID, routeTableID)

	return err
}

// inlineRefID returns an armIDRef's id, or "" when the reference is nil (the
// property was omitted from the request body).
func inlineRefID(ref *armIDRef) string {
	if ref == nil {
		return ""
	}

	return ref.ID
}

// inlineSubnetTags builds the tag set for a subnet materialized from a
// whole-VNet PUT body, carrying over its NAT gateway / NSG references.
func inlineSubnetTags(sub *subnetRequest) map[string]string {
	tags := mergeTags(nil, armSubnetTag, sub.Name)

	if sub.Properties.NatGateway != nil {
		tags = mergeTags(tags, armSubnetNATTag, sub.Properties.NatGateway.ID)
	}

	if sub.Properties.NetworkSecurityGroup != nil {
		tags = mergeTags(tags, armSubnetNSGTag, sub.Properties.NetworkSecurityGroup.ID)
	}

	if sub.Properties.RouteTable != nil {
		tags = mergeTags(tags, armSubnetRouteTableTag, sub.Properties.RouteTable.ID)
	}

	return tags
}

// deleteOmittedSubnets removes every previously-existing subnet whose name
// isn't in wanted, refusing (without deleting anything further) the first one
// still in use by a NIC — the same guard deleteSubnet applies standalone.
func (h *Handler) deleteOmittedSubnets(ctx context.Context, haveByName map[string]netdriver.SubnetInfo, wanted map[string]struct{}) error {
	for name, info := range haveByName {
		if _, ok := wanted[name]; ok {
			continue
		}

		if h.subnetInUseByNICs(ctx, info.ID) {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"subnet %q is in use by a network interface and cannot be deleted", name)
		}

		if err := h.net.DeleteSubnet(ctx, info.ID); err != nil {
			return err
		}
	}

	return nil
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findVNetInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
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
		// Skip anchors fabricated only to satisfy the driver's mandatory VPCID
		// when creating a standalone NSG/route table — they are not user vnets.
		if tagOr(infos[i].Tags, armSyntheticAnchorTag, "") == "true" {
			continue
		}

		itemRG := tagOr(infos[i].Tags, armVNetRGTag, "")
		// An RG-scoped list (rp.ResourceGroup set) returns only that group's
		// networks; a subscription-scoped list (empty) returns all, each stamped
		// with its own group so the response ids are correct.
		if rp.ResourceGroup != "" && !strings.EqualFold(itemRG, rp.ResourceGroup) {
			continue
		}

		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, armNameTag, infos[i].ID)

		if scope.ResourceGroup == "" {
			scope.ResourceGroup = itemRG
		}

		out.Value = append(out.Value, h.vnetResponse(r.Context(), &infos[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findVNetInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
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

// PurgeResourceGroup deletes every Microsoft.Network resource this handler owns
// in the given resource group: network interfaces, NAT gateways, public IPs,
// virtual networks (with their subnets) and network security groups. It backs
// the resource-group cascade delete: when an RG is removed, the resources
// created under it must go too rather than lingering as globally addressable
// orphans. Deletion is forceful (the in-use guards that gate an individual
// DELETE do not apply — the whole group is going away), and best-effort: a
// single driver-level failure is returned but does not stop the remaining
// teardown. The subscription is unused (the emulator is single-estate).
//
// Order matters for the reference chains the emulator models: NICs come first
// (the owning VMs are purged by the compute purger before this one runs, so a
// NIC's virtualMachine back-reference is already cleared and its DELETE
// succeeds); NAT gateways next (deleting one frees the Elastic IP association it
// holds); then public IPs (now unassociated, so ReleaseAddress succeeds); and
// finally the virtual networks and NSGs.
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	var firstErr error

	recordErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	recordErr(h.purgeNICs(ctx, resourceGroup))
	recordErr(h.purgeNATGateways(ctx, resourceGroup))
	recordErr(h.purgePublicIPs(ctx, resourceGroup))
	recordErr(h.purgeVNets(ctx, resourceGroup))
	recordErr(h.purgeNSGs(ctx, resourceGroup))
	recordErr(h.purgeRouteTables(ctx, resourceGroup))
	h.purgeASGs(ctx, resourceGroup)
	h.purgePublicIPPrefixes(ctx, resourceGroup)
	h.purgeNetworkGateways(ctx, resourceGroup)
	h.purgePrivateLink(ctx, resourceGroup)

	return firstErr
}

// purgeNICs deletes every network interface in the resource group. NICs are
// stored resource-group-natively (not tag-scoped), so the driver list already
// filters to the group. The owning VMs are torn down by the compute purger
// first, so each NIC's virtualMachine back-reference is cleared and the delete
// is not blocked by the attached-NIC guard.
func (h *Handler) purgeNICs(ctx context.Context, resourceGroup string) error {
	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		return nil
	}

	nics, err := svc.ListNetworkInterfaces(ctx, resourceGroup)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range nics {
		if derr := svc.DeleteNetworkInterface(ctx, nics[i].ResourceGroup, nics[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

// purgeNATGateways deletes every NAT gateway tagged for the resource group,
// freeing any Elastic IP allocation each one held so the public IPs can then be
// released.
func (h *Handler) purgeNATGateways(ctx context.Context, resourceGroup string) error {
	nats, err := h.net.DescribeNATGateways(ctx, nil)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range nats {
		if !strings.EqualFold(tagOr(nats[i].Tags, armNATGatewayRGTag, ""), resourceGroup) {
			continue
		}

		if derr := h.net.DeleteNATGateway(ctx, nats[i].ID); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

// purgePublicIPs releases every public IP tagged for the resource group. NAT
// gateways are purged first, so a public IP that backed one is no longer
// associated and ReleaseAddress succeeds.
func (h *Handler) purgePublicIPs(ctx context.Context, resourceGroup string) error {
	eips, err := h.net.DescribeAddresses(ctx, nil)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range eips {
		if !strings.EqualFold(tagOr(eips[i].Tags, armPublicIPRGTag, ""), resourceGroup) {
			continue
		}

		if derr := h.net.ReleaseAddress(ctx, eips[i].AllocationID); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

// purgeVNets deletes every virtual network (with its subnets and metadata) in
// the resource group, returning the first delete error encountered.
func (h *Handler) purgeVNets(ctx context.Context, resourceGroup string) error {
	vpcs, err := h.net.DescribeVPCs(ctx, nil)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range vpcs {
		if !strings.EqualFold(tagOr(vpcs[i].Tags, armVNetRGTag, ""), resourceGroup) {
			continue
		}

		h.deleteChildSubnets(ctx, vpcs[i].ID)

		if meta, ok := h.azureMeta(); ok {
			meta.DeleteAzureVNetMetadata(ctx, vpcs[i].ID)
		}

		if derr := h.net.DeleteVPC(ctx, vpcs[i].ID); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

// purgeNSGs deletes every network security group (with its metadata) in the
// resource group, returning the first delete error encountered.
func (h *Handler) purgeNSGs(ctx context.Context, resourceGroup string) error {
	nsgs, err := h.net.DescribeSecurityGroups(ctx, nil)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range nsgs {
		if !strings.EqualFold(tagOr(nsgs[i].Tags, armNSGRGTag, ""), resourceGroup) {
			continue
		}

		if meta, ok := h.azureMeta(); ok {
			meta.DeleteAzureNSGMetadata(ctx, nsgs[i].ID)
		}

		if derr := h.net.DeleteSecurityGroup(ctx, nsgs[i].ID); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
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
	vnet, err := findVNetInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req subnetRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	natGatewayID, ok := h.resolveSubnetNATGateway(w, r, req.Properties.NatGateway)
	if !ok {
		return
	}

	nsgID, ok := h.resolveSubnetNSG(w, r, req.Properties.NetworkSecurityGroup)
	if !ok {
		return
	}

	routeTableID, ok := h.resolveSubnetRouteTable(w, r, req.Properties.RouteTable)
	if !ok {
		return
	}

	if verr := h.validateSubnetCIDR(r.Context(), vnet.ID, rp.SubResourceName, req.Properties.AddressPrefix); verr != nil {
		writeSubnetValidationError(w, verr)
		return
	}

	info, err := h.upsertSubnet(r.Context(), vnet.ID, rp.SubResourceName,
		req.Properties.AddressPrefix, natGatewayID, nsgID, routeTableID)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	body := toSubnetResponse(info, rp)

	writeAcceptedAsync(w, r, rp.Subscription, "subnet-create-"+rp.SubResourceName, body)
}

// resolveSubnetNATGateway validates and resolves an optional natGateway
// reference on a subnet PUT body, writing the appropriate error response
// itself. ok is false when a response was already written and the caller
// must stop.
func (h *Handler) resolveSubnetNATGateway(w http.ResponseWriter, r *http.Request, ref *armIDRef) (id string, ok bool) {
	return h.resolveSubnetRef(w, r, ref, typeNATGateway, "natGateway",
		func(ctx context.Context, rg, name string) error {
			_, err := findNATGatewayByName(ctx, h.net, rg, name)
			return err
		})
}

// resolveSubnetNSG validates and resolves an optional networkSecurityGroup
// reference on a subnet PUT body, writing the appropriate error response
// itself. ok is false when a response was already written and the caller
// must stop. The reference is resolved within its own resource group so a
// subnet cannot associate an NSG that only exists in a different group.
func (h *Handler) resolveSubnetNSG(w http.ResponseWriter, r *http.Request, ref *armIDRef) (id string, ok bool) {
	return h.resolveSubnetRef(w, r, ref, typeNSG, "networkSecurityGroup",
		func(ctx context.Context, rg, name string) error {
			_, err := findNSGInGroup(ctx, h.net, rg, name)
			return err
		})
}

// resolveSubnetRouteTable validates and resolves an optional routeTable
// reference on a subnet PUT body, writing the appropriate error response
// itself. ok is false when a response was already written and the caller must
// stop. The reference is resolved within its own resource group so a subnet
// cannot associate a route table that only exists in a different group.
func (h *Handler) resolveSubnetRouteTable(w http.ResponseWriter, r *http.Request, ref *armIDRef) (id string, ok bool) {
	return h.resolveSubnetRef(w, r, ref, typeRouteTable, "routeTable",
		func(ctx context.Context, rg, name string) error {
			_, err := h.findRouteTableInGroup(ctx, rg, name)
			return err
		})
}

// resolveSubnetRef validates an optional armIDRef on a subnet PUT body: an
// absent reference is a no-op, a malformed one (unparseable or the wrong
// resource type) writes a 400, and validate resolves the reference within its
// own resource group. It returns the reference's own id when valid; ok is false
// when a response was already written and the caller must stop.
func (*Handler) resolveSubnetRef(
	w http.ResponseWriter, r *http.Request, ref *armIDRef, expectType, label string,
	validate func(ctx context.Context, rg, name string) error,
) (id string, ok bool) {
	if ref == nil {
		return "", true
	}

	rp, parsed := azurearm.ParsePath(ref.ID)
	if !parsed || rp.ResourceType != expectType {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "malformed "+label+" id")
		return "", false
	}

	if err := validate(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return "", false
	}

	return ref.ID, true
}

// upsertSubnet creates the named subnet, or — when it already exists — applies
// an in-place update (armnetwork's SubnetsClient.BeginCreateOrUpdate, the real
// ARM mechanism). CreateOrUpdate is a full replacement: the address prefix is
// changed when it differs, and the NAT gateway / NSG associations are REPLACED
// from the request body so an omitted reference (empty id) CLEARS the existing
// association. Both associations live on the subnet's own natGateway /
// networkSecurityGroup properties.
func (h *Handler) upsertSubnet(
	ctx context.Context, vpcID, name, cidr, natGatewayID, nsgID, routeTableID string,
) (*netdriver.SubnetInfo, error) {
	if existing, err := findSubnetInVNet(ctx, h.net, vpcID, name); err == nil {
		return h.updateExistingSubnet(ctx, existing, cidr, natGatewayID, nsgID, routeTableID)
	}

	tags := mergeTags(nil, armSubnetTag, name)
	if natGatewayID != "" {
		tags = mergeTags(tags, armSubnetNATTag, natGatewayID)
	}

	if nsgID != "" {
		tags = mergeTags(tags, armSubnetNSGTag, nsgID)
	}

	if routeTableID != "" {
		tags = mergeTags(tags, armSubnetRouteTableTag, routeTableID)
	}

	return h.net.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID:     vpcID,
		CIDRBlock: cidr,
		Tags:      tags,
	})
}

// updateExistingSubnet applies an in-place CreateOrUpdate to an existing subnet:
// it changes the address prefix when it differs and REPLACES the NSG / NAT
// gateway associations from the request body (an omitted reference — empty id —
// clears that association, matching ARM's full-replacement semantics).
func (h *Handler) updateExistingSubnet(
	ctx context.Context, existing *netdriver.SubnetInfo, cidr, natGatewayID, nsgID, routeTableID string,
) (*netdriver.SubnetInfo, error) {
	if cidr != "" && cidr != existing.CIDRBlock {
		if u, ok := h.net.(netdriver.SubnetCIDRUpdater); ok {
			if err := u.UpdateSubnetCIDR(ctx, existing.ID, cidr); err != nil {
				return nil, err
			}

			existing.CIDRBlock = cidr
		}
	}

	if err := h.replaceSubnetAssociation(ctx, existing, armSubnetNSGTag, nsgID); err != nil {
		return nil, err
	}

	if err := h.replaceSubnetAssociation(ctx, existing, armSubnetNATTag, natGatewayID); err != nil {
		return nil, err
	}

	if err := h.replaceSubnetAssociation(ctx, existing, armSubnetRouteTableTag, routeTableID); err != nil {
		return nil, err
	}

	return existing, nil
}

// replaceSubnetAssociation sets the subnet tag identified by tagKey to id, or —
// when id is empty (the reference was omitted from the request body) — clears
// it, so a CreateOrUpdate that drops an NSG / NAT gateway reference removes the
// association. existing.Tags is updated to mirror the store mutation.
func (h *Handler) replaceSubnetAssociation(ctx context.Context, existing *netdriver.SubnetInfo, tagKey, id string) error {
	if id != "" {
		if err := h.net.UpdateSubnetTags(ctx, existing.ID, map[string]string{tagKey: id}); err != nil {
			return err
		}

		existing.Tags = mergeTags(existing.Tags, tagKey, id)

		return nil
	}

	if tagOr(existing.Tags, tagKey, "") == "" {
		return nil
	}

	if err := h.net.RemoveSubnetTags(ctx, existing.ID, []string{tagKey}); err != nil {
		return err
	}

	existing.Tags = tagsWithout(existing.Tags, tagKey)

	return nil
}

// tagsWithout returns a copy of in with key removed.
func tagsWithout(in map[string]string, key string) map[string]string {
	out := make(map[string]string, len(in))

	for k, v := range in {
		if k != key {
			out[k] = v
		}
	}

	return out
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
	// A subnet is addressed under its parent virtual network; resolve that vnet
	// scoped to the request's resource group so a subnet is not readable under
	// the wrong group (the parent vnet gates the child).
	vnet, err := findVNetInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	info, err := findSubnetInVNet(r.Context(), h.net, vnet.ID, rp.SubResourceName)
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
	vnet, err := findVNetInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	info, err := findSubnetInVNet(r.Context(), h.net, vnet.ID, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// A subnet still bound to a NIC ipConfiguration cannot be deleted; real
	// ARM answers 400 InUseSubnetCannotBeDeleted (verified against the real
	// error: https://learn.microsoft.com/en-us/troubleshoot/azure/azure-kubernetes/error-codes/publicipaddr-inusesubnet-netsecgrp-error).
	if h.subnetInUseByNICs(r.Context(), info.ID) {
		azurearm.WriteError(w, http.StatusBadRequest, "InUseSubnetCannotBeDeleted",
			"subnet "+rp.SubResourceName+" is in use by a network interface and cannot be deleted")

		return
	}

	if err := h.net.DeleteSubnet(r.Context(), info.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "subnet-delete-"+rp.SubResourceName, nil)
}

// subnetInUseByNICs reports whether any network interface's ipConfiguration
// references the subnet with the given driver id, resolving each NIC's
// ARM subnet reference the same way subnetCIDR does.
func (h *Handler) subnetInUseByNICs(ctx context.Context, subnetDriverID string) bool {
	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		return false
	}

	nics, err := svc.ListNetworkInterfaces(ctx, "")
	if err != nil {
		return false
	}

	subs, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return false
	}

	for i := range nics {
		for j := range nics[i].IPConfigs {
			if resolveSubnetDriverID(ctx, h.net, subs, nics[i].IPConfigs[j].SubnetID) == subnetDriverID {
				return true
			}
		}
	}

	return false
}

// resolveSubnetDriverID maps an ARM subnet resource id
// (.../virtualNetworks/{vn}/subnets/{name}) to the driver's own subnet id, or
// "" if it doesn't resolve to any known subnet.
func resolveSubnetDriverID(ctx context.Context, n netdriver.Networking, subs []netdriver.SubnetInfo, armSubnetID string) string {
	sp, ok := azurearm.ParsePath(armSubnetID)
	if !ok || sp.ResourceType != typeVNet || sp.SubResource != subResSubnets {
		return ""
	}

	vnet, err := findVNetByName(ctx, n, sp.ResourceName)
	if err != nil {
		return ""
	}

	for i := range subs {
		if subs[i].VPCID == vnet.ID && tagOr(subs[i].Tags, armSubnetTag, "") == sp.SubResourceName {
			return subs[i].ID
		}
	}

	return ""
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

	rules := toAzureNSGRules(req.Properties.SecurityRules)

	if verr := validateSecurityRuleBatch(rules); verr != nil {
		azurearm.WriteCErr(w, verr)
		return
	}

	info, err := h.upsertNSG(r.Context(), rp.ResourceGroup, rp.ResourceName, req.Tags)
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
			SecurityRules: rules,
		})
	}

	writeAcceptedAsync(w, r, rp.Subscription, "nsg-create-"+rp.ResourceName, h.nsgResponse(r.Context(), info, rp))
}

// upsertNSG reuses an existing NSG of the same name (idempotent re-PUT) or
// creates one, anchoring the driver security group to any VPC (creating a
// synthetic one when none exists, as the driver requires a VPC id).
func (h *Handler) upsertNSG(ctx context.Context, rg, name string,
	tags map[string]string,
) (*netdriver.SecurityGroupInfo, error) {
	if existing, err := findNSGInGroup(ctx, h.net, rg, name); err == nil {
		if len(tags) > 0 {
			_ = h.net.UpdateSecurityGroupTags(ctx, existing.ID, tags)

			if refreshed, rerr := findNSGInGroup(ctx, h.net, rg, name); rerr == nil {
				existing = refreshed
			}
		}

		return existing, nil
	}

	vpcs, _ := h.net.DescribeVPCs(ctx, nil)

	var anchor string

	if len(vpcs) > 0 {
		anchor = vpcs[0].ID
	} else {
		v, vErr := h.net.CreateVPC(ctx, netdriver.VPCConfig{
			CIDRBlock: "10.0.0.0/16",
			Tags:      map[string]string{armSyntheticAnchorTag: "true"},
		})
		if vErr != nil {
			return nil, vErr
		}

		anchor = v.ID
	}

	return h.net.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		Name:  name,
		VPCID: anchor,
		Tags:  mergeTags(mergeTags(tags, armNSGTag, name), armNSGRGTag, rg),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getNSG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNSGInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
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
		itemRG := tagOr(infos[i].Tags, armNSGRGTag, "")
		// An RG-scoped list returns only that group's NSGs; a subscription-scoped
		// list returns all, each stamped with its own group.
		if rp.ResourceGroup != "" && !strings.EqualFold(itemRG, rp.ResourceGroup) {
			continue
		}

		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, armNSGTag, infos[i].ID)

		if scope.ResourceGroup == "" {
			scope.ResourceGroup = itemRG
		}

		out.Value = append(out.Value, h.nsgResponse(r.Context(), &infos[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteNSG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNSGInGroup(r.Context(), h.net, rp.ResourceGroup, rp.ResourceName)
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

	// A public IP may be drawn from a public IP prefix. The mock only records the
	// reference (deferred child-IP allocation); the prefix rebuilds its read-only
	// publicIPAddresses[] back-reference by scanning for this internal tag.
	if req.Properties.PublicIPPrefix != nil && req.Properties.PublicIPPrefix.ID != "" {
		tags = mergeTags(tags, armPublicIPPrefixTag, req.Properties.PublicIPPrefix.ID)
	}

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

	info, err := h.upsertPublicIP(r.Context(), rp.ResourceGroup, rp.ResourceName, cfg)
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

// upsertPublicIP reuses an existing public IP of the same name (idempotent
// re-PUT — real ARM CreateOrUpdate) or allocates a new one. On the found branch
// it mutates the existing allocation in place rather than allocating a second,
// hidden one, so LIST returns exactly one entry per name and the allocation
// never leaks.
//
//nolint:gocritic // hugeParam: cfg mirrors AllocateAddress's driver signature.
func (h *Handler) upsertPublicIP(ctx context.Context, rg, name string,
	cfg netdriver.ElasticIPConfig,
) (*netdriver.ElasticIP, error) {
	existing, err := findPublicIPByName(ctx, h.net, rg, name)
	if err != nil {
		return h.net.AllocateAddress(ctx, cfg)
	}

	meta, ok := h.azureMeta()
	if !ok {
		return existing, nil
	}

	if uerr := meta.UpdateAzurePublicIP(ctx, existing.AllocationID, cfg); uerr != nil {
		return nil, uerr
	}

	if refreshed, rerr := findPublicIPByName(ctx, h.net, rg, name); rerr == nil {
		existing = refreshed
	}

	return existing, nil
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
		if rp.ResourceGroup != "" && !strings.EqualFold(tagOr(infos[i].Tags, armPublicIPRGTag, ""), rp.ResourceGroup) {
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

// findVNetInGroup resolves a virtual network by both its ARM name and resource
// group, so a same-named vnet in a different group is not returned. rg == ""
// falls back to name-only (an unscoped lookup, e.g. a legacy path with no
// group). Resource-group comparison is case-insensitive, matching ARM.
//
//nolint:dupl // mirrors findNSGInGroup over a distinct resource type and tag by design
func findVNetInGroup(ctx context.Context, n netdriver.Networking, rg, name string) (*netdriver.VPCInfo, error) {
	infos, err := n.DescribeVPCs(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, armNameTag, "") != name {
			continue
		}

		if rg != "" && !strings.EqualFold(tagOr(infos[i].Tags, armVNetRGTag, ""), rg) {
			continue
		}

		return &infos[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "virtualNetwork %s not found", name)
}

// findNSGInGroup resolves a network security group by both its ARM name and
// resource group; see findVNetInGroup for the rg semantics.
//
//nolint:dupl // mirrors findVNetInGroup over a distinct resource type and tag by design
func findNSGInGroup(ctx context.Context, n netdriver.Networking, rg, name string) (*netdriver.SecurityGroupInfo, error) {
	infos, err := n.DescribeSecurityGroups(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		if tagOr(infos[i].Tags, armNSGTag, "") != name {
			continue
		}

		if rg != "" && !strings.EqualFold(tagOr(infos[i].Tags, armNSGRGTag, ""), rg) {
			continue
		}

		return &infos[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "networkSecurityGroup %s not found", name)
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

	if nsgID := tagOr(info.Tags, armSubnetNSGTag, ""); nsgID != "" {
		out.Properties.NetworkSecurityGroup = &armIDRef{ID: nsgID}
	}

	if rtID := tagOr(info.Tags, armSubnetRouteTableTag, ""); rtID != "" {
		out.Properties.RouteTable = &armIDRef{ID: rtID}
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
			Subnets:              h.nsgAssociatedSubnets(ctx, id),
			NetworkInterfaces:    h.nsgAssociatedNICs(ctx, id),
		},
	}
}

// nsgAssociatedSubnets scans every subnet for a networkSecurityGroup
// reference (armSubnetNSGTag) matching nsgARMID, the read-only back-reference
// real ARM reports on a networkSecurityGroups GET.
func (h *Handler) nsgAssociatedSubnets(ctx context.Context, nsgARMID string) []armIDRef {
	return h.subnetsAssociatedByTag(ctx, armSubnetNSGTag, nsgARMID)
}

// subnetsAssociatedByTag scans every subnet for an association tag (tagKey)
// whose value matches armID and returns the subnets' ARM ids — the read-only
// back-reference an NSG / route table reports once a subnet points at it. armID
// supplies the subscription/resource-group for the built subnet ids.
func (h *Handler) subnetsAssociatedByTag(ctx context.Context, tagKey, armID string) []armIDRef {
	subs, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil
	}

	vnetNames := make(map[string]string)

	var out []armIDRef

	for i := range subs {
		if !strings.EqualFold(tagOr(subs[i].Tags, tagKey, ""), armID) {
			continue
		}

		vnetName, ok := vnetNames[subs[i].VPCID]
		if !ok {
			vnet, verr := h.net.DescribeVPCs(ctx, []string{subs[i].VPCID})
			if verr != nil || len(vnet) == 0 {
				continue
			}

			vnetName = tagOr(vnet[0].Tags, armNameTag, vnet[0].ID)
			vnetNames[subs[i].VPCID] = vnetName
		}

		out = append(out, armIDRef{ID: subnetResourceID(armID, vnetName, tagOr(subs[i].Tags, armSubnetTag, ""))})
	}

	return out
}

// nsgAssociatedNICs scans every network interface for a top-level
// networkSecurityGroup reference matching nsgARMID.
func (h *Handler) nsgAssociatedNICs(ctx context.Context, nsgARMID string) []armIDRef {
	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		return nil
	}

	nics, err := svc.ListNetworkInterfaces(ctx, "")
	if err != nil {
		return nil
	}

	var out []armIDRef

	for i := range nics {
		if strings.EqualFold(nics[i].NetworkSecurityGroupID, nsgARMID) {
			out = append(out, armIDRef{ID: nicResourceID(nsgARMID, nics[i].ResourceGroup, nics[i].Name)})
		}
	}

	return out
}

// subnetResourceID builds a subnet ARM id sharing the subscription of
// nsgARMID (both resources live in the same ARM path shape), used only for
// the NSG's read-only associated-subnets back-reference.
func subnetResourceID(nsgARMID, vnetName, subnetName string) string {
	nsgRP, ok := azurearm.ParsePath(nsgARMID)
	if !ok {
		return ""
	}

	return azurearm.BuildResourceID(nsgRP.Subscription, nsgRP.ResourceGroup, providerName, typeVNet, vnetName) +
		"/subnets/" + subnetName
}

// nicResourceID builds a NIC ARM id sharing the subscription of nsgARMID,
// used only for the NSG's read-only associated-NICs back-reference.
func nicResourceID(nsgARMID, nicResourceGroup, nicName string) string {
	nsgRP, ok := azurearm.ParsePath(nsgARMID)
	if !ok {
		return ""
	}

	return azurearm.BuildResourceID(nsgRP.Subscription, nicResourceGroup, providerName, typeNIC, nicName)
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

	if prefixID := tagOr(info.Tags, armPublicIPPrefixTag, ""); prefixID != "" {
		out.Properties.PublicIPPrefix = &armIDRef{ID: prefixID}
	}

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

// armTagsObject is the ARM UpdateTags PATCH body ({"tags": {...}}) — the
// TagsObject the armnetwork *Client.UpdateTags / BeginUpdateTags methods send.
// Only tags are updatable through this operation; the resource's properties are
// left intact.
type armTagsObject struct {
	Tags map[string]string `json:"tags,omitempty"`
}

// replacementTags normalizes an UpdateTags PATCH body's tags for wholesale
// replacement — real Azure's resource-level UpdateTags SETS the tag collection,
// it does not merge (the merge/replace/delete modes live only on the generic
// Microsoft.Resources/tags API). A populated map replaces the stored set (cloned
// so the store never aliases the request); a present-but-empty map ({}) wipes it
// (nil). The caller applies this only when the body carried a tags key, so an
// absent tags key leaves the stored set untouched.
func replacementTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	return maps.Clone(in)
}

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
