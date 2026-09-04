package vnet

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const typeNIC = "networkInterfaces"

// subResEffectiveNSGs is the POST action segment InterfacesClient.
// BeginListEffectiveNetworkSecurityGroups sends: .../networkInterfaces/{nic}/effectiveNetworkSecurityGroups.
const subResEffectiveNSGs = "effectiveNetworkSecurityGroups"

// ARM JSON shapes for Microsoft.Network/networkInterfaces.

type nicRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties nicRequestProps   `json:"properties"`
}

type nicRequestProps struct {
	IPConfigurations     []nicIPConfigRequest `json:"ipConfigurations"`
	EnableIPForwarding   bool                 `json:"enableIPForwarding,omitempty"`
	NetworkSecurityGroup *armIDRef            `json:"networkSecurityGroup,omitempty"`
}

type nicIPConfigRequest struct {
	Name       string                  `json:"name"`
	Properties nicIPConfigRequestProps `json:"properties"`
}

type nicIPConfigRequestProps struct {
	PrivateIPAddress                string     `json:"privateIPAddress,omitempty"`
	PrivateIPAllocationMethod       string     `json:"privateIPAllocationMethod,omitempty"`
	Primary                         bool       `json:"primary,omitempty"`
	Subnet                          *armIDRef  `json:"subnet,omitempty"`
	PublicIPAddress                 *armIDRef  `json:"publicIPAddress,omitempty"`
	LoadBalancerBackendAddressPools []armIDRef `json:"loadBalancerBackendAddressPools,omitempty"`
	ApplicationSecurityGroups       []armIDRef `json:"applicationSecurityGroups,omitempty"`
}

type armIDRef struct {
	ID string `json:"id"`
}

type nicResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	ETag       string            `json:"etag,omitempty"`
	Properties nicResponseProps  `json:"properties"`
}

type nicResponseProps struct {
	ProvisioningState    string                `json:"provisioningState"`
	ResourceGUID         string                `json:"resourceGuid,omitempty"`
	MACAddress           string                `json:"macAddress,omitempty"`
	EnableIPForwarding   bool                  `json:"enableIPForwarding"`
	IPConfigurations     []nicIPConfigResponse `json:"ipConfigurations"`
	NetworkSecurityGroup *armIDRef             `json:"networkSecurityGroup,omitempty"`
	VirtualMachine       *armIDRef             `json:"virtualMachine,omitempty"`
}

type nicIPConfigResponse struct {
	Name       string                   `json:"name"`
	Properties nicIPConfigResponseProps `json:"properties"`
}

type nicIPConfigResponseProps struct {
	ProvisioningState               string     `json:"provisioningState"`
	PrivateIPAddress                string     `json:"privateIPAddress,omitempty"`
	PrivateIPAllocationMethod       string     `json:"privateIPAllocationMethod"`
	Primary                         bool       `json:"primary"`
	Subnet                          *armIDRef  `json:"subnet,omitempty"`
	PublicIPAddress                 *armIDRef  `json:"publicIPAddress,omitempty"`
	LoadBalancerBackendAddressPools []armIDRef `json:"loadBalancerBackendAddressPools,omitempty"`
	ApplicationSecurityGroups       []armIDRef `json:"applicationSecurityGroups,omitempty"`
}

type nicListResponse struct {
	Value []nicResponse `json:"value"`
}

// routeNIC dispatches Microsoft.Network/networkInterfaces requests. The
// networking driver serves NICs through the Azure-specific optional capability;
// a driver that doesn't implement it reports NotImplemented.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeNIC(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"network interfaces are not supported by this networking driver")

		return
	}

	if rp.ResourceName == "" {
		h.listNICs(w, r, rp, svc)
		return
	}

	// InterfacesClient.BeginListEffectiveNetworkSecurityGroups: a POST action
	// on the NIC, not a nested collection — routed before the whole-NIC method
	// switch so it never falls through to createNIC/getNIC/deleteNIC.
	if strings.EqualFold(rp.SubResource, subResEffectiveNSGs) {
		if r.Method != http.MethodPost {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.listEffectiveNSGs(w, r, rp, svc)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createNIC(w, r, rp, svc)
	case http.MethodPatch:
		h.patchNIC(w, r, rp, svc)
	case http.MethodGet:
		h.getNIC(w, r, rp, svc)
	case http.MethodDelete:
		h.deleteNIC(w, r, rp, svc)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createNIC(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkInterfaces,
) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req nicRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	ipConfigs, err := h.buildIPConfigs(r.Context(), rp.ResourceGroup, rp.ResourceName, req.Properties.IPConfigurations)
	if err != nil {
		if cerrors.IsFailedPrecondition(err) {
			azurearm.WriteError(w, http.StatusBadRequest, "PublicIPAddressCannotBeAssignedToMultipleIpConfigs", err.Error())
			return
		}

		azurearm.WriteCErr(w, err)

		return
	}

	var nsgID string

	if req.Properties.NetworkSecurityGroup != nil {
		nsgRP, ok := azurearm.ParsePath(req.Properties.NetworkSecurityGroup.ID)
		if !ok || nsgRP.ResourceType != typeNSG {
			azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "malformed networkSecurityGroup id")
			return
		}

		if _, nsgErr := findNSGInGroup(r.Context(), h.net, nsgRP.ResourceGroup, nsgRP.ResourceName); nsgErr != nil {
			azurearm.WriteCErr(w, nsgErr)
			return
		}

		nsgID = req.Properties.NetworkSecurityGroup.ID
	}

	cfg := netdriver.AzureNICConfig{
		Location:               req.Location,
		Tags:                   req.Tags,
		IPConfigs:              ipConfigs,
		IPForwarding:           req.Properties.EnableIPForwarding,
		NetworkSecurityGroupID: nsgID,
	}

	nic, err := svc.CreateOrUpdateNetworkInterface(r.Context(), rp.ResourceGroup, rp.ResourceName, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "nic-create-"+rp.ResourceName, toNICResponse(nic, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getNIC(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkInterfaces,
) {
	nic, err := svc.GetNetworkInterface(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toNICResponse(nic, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deleteNIC(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkInterfaces,
) {
	if err := svc.DeleteNetworkInterface(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		// A NIC attached to a VM: ARM answers 400 with this specific code, which
		// armnetwork clients switch on — not the generic 409 WriteCErr would emit.
		if cerrors.IsFailedPrecondition(err) {
			azurearm.WriteError(w, http.StatusBadRequest, "InUseNetworkInterfaceCannotBeDeleted", err.Error())
			return
		}

		azurearm.WriteCErr(w, err)

		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "nic-delete-"+rp.ResourceName, nil)
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) listNICs(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkInterfaces,
) {
	// An empty ResourceGroup in the path means a subscription-wide list.
	nics, err := svc.ListNetworkInterfaces(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := nicListResponse{Value: make([]nicResponse, 0, len(nics))}

	for i := range nics {
		scope := rp
		scope.ResourceGroup = nics[i].ResourceGroup
		scope.ResourceName = nics[i].Name
		out.Value = append(out.Value, toNICResponse(&nics[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// buildIPConfigs maps the wire ipConfigurations to driver configs, resolving
// each referenced subnet to its address prefix so the mock can allocate a
// dynamic private IP from the right range. rg/nicName identify the NIC being
// created or updated, so a re-PUT of the same NIC doesn't conflict with its
// own existing public IP reference.
func (h *Handler) buildIPConfigs(
	ctx context.Context, rg, nicName string, in []nicIPConfigRequest,
) ([]netdriver.AzureIPConfig, error) {
	out := make([]netdriver.AzureIPConfig, 0, len(in))

	for i := range in {
		p := in[i].Properties

		cfg := netdriver.AzureIPConfig{
			Name:             in[i].Name,
			PrivateIP:        p.PrivateIPAddress,
			AllocationMethod: p.PrivateIPAllocationMethod,
			Primary:          p.Primary,
			LBBackendPoolIDs: refIDs(p.LoadBalancerBackendAddressPools),
			ASGIDs:           refIDs(p.ApplicationSecurityGroups),
		}

		if p.Subnet != nil {
			cfg.SubnetID = p.Subnet.ID

			cidr, err := h.subnetCIDR(ctx, p.Subnet.ID)
			if err != nil {
				return nil, err
			}

			cfg.SubnetCIDR = cidr
		}

		if p.PublicIPAddress != nil {
			pipRP, ok := azurearm.ParsePath(p.PublicIPAddress.ID)
			if !ok || pipRP.ResourceType != typePublicIP {
				return nil, cerrors.Newf(cerrors.InvalidArgument, "malformed public IP id %q", p.PublicIPAddress.ID)
			}

			pip, err := findPublicIPByName(ctx, h.net, pipRP.ResourceGroup, pipRP.ResourceName)
			if err != nil {
				return nil, cerrors.Newf(cerrors.InvalidArgument,
					"ipConfiguration references public IP %q which does not exist", pipRP.ResourceName)
			}

			if kind, owner, claimed := h.publicIPClaimant(
				ctx, p.PublicIPAddress.ID, pip.AllocationID, claimKindNIC, rg, nicName,
			); claimed {
				return nil, cerrors.Newf(cerrors.FailedPrecondition,
					"public IP %q is already associated with %s %q", p.PublicIPAddress.ID, kind, owner)
			}

			cfg.PublicIPID = p.PublicIPAddress.ID
		}

		out = append(out, cfg)
	}

	return out, nil
}

// refIDs extracts the non-empty ARM resource ids from a slice of id references
// (an ipConfiguration's loadBalancerBackendAddressPools or
// applicationSecurityGroups), dropping empty references. It returns nil when
// none are present so an unassociated ipConfiguration carries no membership.
func refIDs(refs []armIDRef) []string {
	if len(refs) == 0 {
		return nil
	}

	out := make([]string, 0, len(refs))

	for i := range refs {
		if refs[i].ID != "" {
			out = append(out, refs[i].ID)
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// toIDRefs wraps ARM resource id strings back into id references for the wire
// response, the inverse of refIDs. It returns nil for an empty input so a
// reference-less field stays omitted.
func toIDRefs(ids []string) []armIDRef {
	if len(ids) == 0 {
		return nil
	}

	out := make([]armIDRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, armIDRef{ID: id})
	}

	return out
}

// Owner kinds a public IP can be claimed by, used to exclude the resource being
// (re-)written from its own conflict check.
const (
	claimKindNIC = "nic"
	claimKindNAT = "nat"
)

// publicIPClaimant returns the owner already bound to the public IP other than
// the resource being written (selfKind + selfRG + selfName), so a NIC and a NAT
// gateway both refuse to steal a static public IP that is already in use — real
// Azure binds one to a single owner (PublicIPAddressCannotBeAssignedToMultiple…).
// NICs reference the IP by its ARM id; NAT gateways reference it by the driver
// Elastic-IP allocationID, so the caller passes both.
func (h *Handler) publicIPClaimant(
	ctx context.Context, pipArmID, allocationID, selfKind, selfRG, selfName string,
) (kind, name string, claimed bool) {
	if n, ok := h.nicPublicIPClaimant(ctx, pipArmID, selfKind, selfRG, selfName); ok {
		return "network interface", n, true
	}

	if n, ok := h.natPublicIPClaimant(ctx, allocationID, selfKind, selfRG, selfName); ok {
		return "NAT gateway", n, true
	}

	return "", "", false
}

// nicPublicIPClaimant reports the name of a network interface (other than self)
// whose ipConfiguration already references pipArmID.
func (h *Handler) nicPublicIPClaimant(ctx context.Context, pipArmID, selfKind, selfRG, selfName string) (string, bool) {
	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		return "", false
	}

	nics, err := svc.ListNetworkInterfaces(ctx, "")
	if err != nil {
		return "", false
	}

	for i := range nics {
		if selfKind == claimKindNIC && strings.EqualFold(nics[i].ResourceGroup, selfRG) &&
			strings.EqualFold(nics[i].Name, selfName) {
			continue
		}

		for j := range nics[i].IPConfigs {
			if strings.EqualFold(nics[i].IPConfigs[j].PublicIPID, pipArmID) {
				return nics[i].Name, true
			}
		}
	}

	return "", false
}

// natPublicIPClaimant reports the name of a NAT gateway (other than self) already
// bound to the Elastic-IP allocationID. An empty allocationID never matches.
func (h *Handler) natPublicIPClaimant(ctx context.Context, allocationID, selfKind, selfRG, selfName string) (string, bool) {
	if allocationID == "" {
		return "", false
	}

	gws, err := h.net.DescribeNATGateways(ctx, nil)
	if err != nil {
		return "", false
	}

	for i := range gws {
		name := tagOr(gws[i].Tags, armNATGatewayTag, "")
		rg := tagOr(gws[i].Tags, armNATGatewayRGTag, "")

		if selfKind == claimKindNAT && strings.EqualFold(rg, selfRG) && strings.EqualFold(name, selfName) {
			continue
		}

		if strings.EqualFold(gws[i].AllocationID, allocationID) {
			return name, true
		}
	}

	return "", false
}

// subnetCIDR resolves the address prefix of the subnet named by an ARM subnet
// resource id (.../virtualNetworks/{vn}/subnets/{name}).
func (h *Handler) subnetCIDR(ctx context.Context, subnetID string) (string, error) {
	// Resolve scoped by (vnet, subnet): subnet names are only unique within a
	// vnet, so a name-only lookup would allocate from the wrong address space
	// when two vnets share a subnet name (e.g. every vnet has a "default").
	sp, ok := azurearm.ParsePath(subnetID)
	if !ok || sp.ResourceType != typeVNet || sp.SubResource != subResSubnets || sp.SubResourceName == "" {
		return "", cerrors.Newf(cerrors.InvalidArgument, "malformed subnet id %q", subnetID)
	}

	vnet, err := findVNetByName(ctx, h.net, sp.ResourceName)
	if err != nil {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"ipConfiguration references vnet %q which does not exist", sp.ResourceName)
	}

	subs, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return "", err
	}

	for i := range subs {
		if subs[i].VPCID == vnet.ID && tagOr(subs[i].Tags, armSubnetTag, "") == sp.SubResourceName {
			return subs[i].CIDRBlock, nil
		}
	}

	return "", cerrors.Newf(cerrors.InvalidArgument,
		"ipConfiguration references subnet %q which does not exist in vnet %q", sp.SubResourceName, sp.ResourceName)
}

// ipConfigResourceID builds the nested ARM resource id of one NIC
// ipConfiguration, used as the publicIPAddresses.ipConfiguration back-reference.
func ipConfigResourceID(sub, rg, nicName, configName string) string {
	return "/subscriptions/" + sub +
		"/resourceGroups/" + rg +
		"/providers/" + providerName + "/" + typeNIC +
		"/" + nicName + "/ipConfigurations/" + configName
}

//nolint:gocritic // rp is a request-scoped value
func toNICResponse(nic *netdriver.AzureNIC, rp azurearm.ResourcePath) nicResponse {
	loc := nic.Location
	if loc == "" {
		loc = defaultLoc
	}

	out := nicResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNIC, rp.ResourceName),
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeNIC,
		Location: loc,
		Tags:     nic.Tags,
		ETag:     nic.ETag,
		Properties: nicResponseProps{
			ProvisioningState:  nic.ProvisioningState,
			ResourceGUID:       nic.ResourceGUID,
			MACAddress:         nic.MACAddress,
			EnableIPForwarding: nic.IPForwarding,
			IPConfigurations:   make([]nicIPConfigResponse, 0, len(nic.IPConfigs)),
		},
	}

	for i := range nic.IPConfigs {
		c := nic.IPConfigs[i]

		rc := nicIPConfigResponse{
			Name: c.Name,
			Properties: nicIPConfigResponseProps{
				ProvisioningState:         "Succeeded",
				PrivateIPAddress:          c.PrivateIP,
				PrivateIPAllocationMethod: c.AllocationMethod,
				Primary:                   c.Primary,
			},
		}

		if c.SubnetID != "" {
			rc.Properties.Subnet = &armIDRef{ID: c.SubnetID}
		}

		if c.PublicIPID != "" {
			rc.Properties.PublicIPAddress = &armIDRef{ID: c.PublicIPID}
		}

		for _, poolID := range c.LBBackendPoolIDs {
			rc.Properties.LoadBalancerBackendAddressPools = append(
				rc.Properties.LoadBalancerBackendAddressPools, armIDRef{ID: poolID})
		}

		for _, asgID := range c.ASGIDs {
			rc.Properties.ApplicationSecurityGroups = append(
				rc.Properties.ApplicationSecurityGroups, armIDRef{ID: asgID})
		}

		out.Properties.IPConfigurations = append(out.Properties.IPConfigurations, rc)
	}

	if nic.NetworkSecurityGroupID != "" {
		out.Properties.NetworkSecurityGroup = &armIDRef{ID: nic.NetworkSecurityGroupID}
	}

	if nic.VirtualMachineID != "" {
		out.Properties.VirtualMachine = &armIDRef{ID: nic.VirtualMachineID}
	}

	return out
}
