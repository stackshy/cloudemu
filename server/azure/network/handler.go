// Package network implements the Microsoft.Network ARM resources we expose:
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
package network

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName  = "Microsoft.Network"
	typeVNet      = "virtualNetworks"
	typeNSG       = "networkSecurityGroups"
	typeLocations = "locations"
	armNameTag    = "cloudemu:azureNetName"
	armSubnetTag  = "cloudemu:azureSubnet"
	armNSGTag     = "cloudemu:azureNSGName"
	defaultLoc    = "eastus"
	subResSubnets = "subnets"
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
	case typeVNet, typeNSG, typeLocations:
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

	cidr := ""
	if req.Properties.AddressSpace != nil && len(req.Properties.AddressSpace.AddressPrefixes) > 0 {
		cidr = req.Properties.AddressSpace.AddressPrefixes[0]
	}

	cfg := netdriver.VPCConfig{
		CIDRBlock: cidr,
		Tags:      mergeTags(req.Tags, armNameTag, rp.ResourceName),
	}

	info, err := h.net.CreateVPC(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	body := toVNetResponse(info, rp, loc, req.Properties.AddressSpace, nil)

	writeAcceptedAsync(w, r, rp.Subscription, "vnet-create-"+rp.ResourceName, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getVNet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	subs, _ := h.net.DescribeSubnets(r.Context(), nil)

	azurearm.WriteJSON(w, http.StatusOK, toVNetResponseFromInfo(info, rp, subs))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listVNets(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	infos, err := h.net.DescribeVPCs(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	subs, _ := h.net.DescribeSubnets(r.Context(), nil)
	out := vnetListResponse{}

	for i := range infos {
		scope := rp
		scope.ResourceName = tagOr(infos[i].Tags, armNameTag, infos[i].ID)
		out.Value = append(out.Value, toVNetResponseFromInfo(&infos[i], scope, subs))
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

	if err := h.net.DeleteVPC(r.Context(), info.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "vnet-delete-"+rp.ResourceName, nil)
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

	cfg := netdriver.SubnetConfig{
		VPCID:     vnet.ID,
		CIDRBlock: req.Properties.AddressPrefix,
		Tags:      mergeTags(nil, armSubnetTag, rp.SubResourceName),
	}

	info, err := h.net.CreateSubnet(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	body := toSubnetResponse(info, rp)

	writeAcceptedAsync(w, r, rp.Subscription, "subnet-create-"+rp.SubResourceName, body)
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

	// Find any VPC to anchor the SG; the driver requires a VPC ID. If no
	// VPC exists we create a synthetic one.
	vpcs, _ := h.net.DescribeVPCs(r.Context(), nil)

	var anchor string

	if len(vpcs) > 0 {
		anchor = vpcs[0].ID
	} else {
		v, vErr := h.net.CreateVPC(r.Context(), netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
		if vErr != nil {
			azurearm.WriteCErr(w, vErr)
			return
		}

		anchor = v.ID
	}

	cfg := netdriver.SecurityGroupConfig{
		Name:  rp.ResourceName,
		VPCID: anchor,
		Tags:  mergeTags(req.Tags, armNSGTag, rp.ResourceName),
	}

	info, err := h.net.CreateSecurityGroup(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	body := toNSGResponse(info, rp, loc, req.Properties.SecurityRules)

	writeAcceptedAsync(w, r, rp.Subscription, "nsg-create-"+rp.ResourceName, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getNSG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNSGByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toNSGResponse(info, rp, defaultLoc, nil))
}

//nolint:gocritic // rp is a request-scoped value
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
		out.Value = append(out.Value, toNSGResponse(&infos[i], scope, defaultLoc, nil))
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

	if err := h.net.DeleteSecurityGroup(r.Context(), info.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "nsg-delete-"+rp.ResourceName, nil)
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

// Response shaping helpers.

//nolint:gocritic // rp is a request-scoped value
func toVNetResponse(info *netdriver.VPCInfo, rp azurearm.ResourcePath, location string,
	addr *addressSpace, subs []netdriver.SubnetInfo,
) vnetResponse {
	if location == "" {
		location = defaultLoc
	}

	if addr == nil && info != nil {
		addr = &addressSpace{AddressPrefixes: []string{info.CIDRBlock}}
	}

	out := vnetResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeVNet, rp.ResourceName),
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeVNet,
		Location: location,
		Tags:     stripInternal(info.Tags),
		Properties: vnetResponseProps{
			ProvisioningState: "Succeeded",
			AddressSpace:      addr,
		},
	}

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

//nolint:gocritic // rp is a request-scoped value
func toVNetResponseFromInfo(info *netdriver.VPCInfo, rp azurearm.ResourcePath, subs []netdriver.SubnetInfo) vnetResponse {
	return toVNetResponse(info, rp, defaultLoc, nil, subs)
}

//nolint:gocritic // rp is a request-scoped value
func toSubnetResponse(info *netdriver.SubnetInfo, rp azurearm.ResourcePath) subnetResponse {
	name := tagOr(info.Tags, armSubnetTag, rp.SubResourceName)

	return subnetResponse{
		ID: "/subscriptions/" + rp.Subscription +
			"/resourceGroups/" + rp.ResourceGroup +
			"/providers/" + providerName + "/" + typeVNet +
			"/" + rp.ResourceName + "/subnets/" + name,
		Name: name,
		Properties: subnetResponseProps{
			ProvisioningState: "Succeeded",
			AddressPrefix:     info.CIDRBlock,
		},
	}
}

//nolint:gocritic // rp is a request-scoped value
func toNSGResponse(info *netdriver.SecurityGroupInfo, rp azurearm.ResourcePath, location string,
	rules []securityRule,
) nsgResponse {
	return nsgResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNSG, rp.ResourceName),
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeNSG,
		Location: location,
		Tags:     stripInternal(info.Tags),
		Properties: nsgResponseProps{
			ProvisioningState: "Succeeded",
			SecurityRules:     rules,
		},
	}
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
