package vnet

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Microsoft.Network site-to-site VPN resource types.
const (
	typeVNGateway  = "virtualNetworkGateways"
	typeLNGateway  = "localNetworkGateways"
	typeConnection = "connections"

	connectionStatusConnected = "Connected"
)

// ARM JSON shapes.

type gatewaySKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type bgpSettings struct {
	ASN               int64  `json:"asn,omitempty"`
	BgpPeeringAddress string `json:"bgpPeeringAddress,omitempty"`
	PeerWeight        int32  `json:"peerWeight,omitempty"`
}

// virtualNetworkGateways.

type vngIPConfig struct {
	Name       string           `json:"name,omitempty"`
	Properties vngIPConfigProps `json:"properties"`
}

type vngIPConfigProps struct {
	PrivateIPAllocationMethod string    `json:"privateIPAllocationMethod,omitempty"`
	PublicIPAddress           *armIDRef `json:"publicIPAddress,omitempty"`
	Subnet                    *armIDRef `json:"subnet,omitempty"`
}

type vngRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties vngRequestProps   `json:"properties"`
}

type vngRequestProps struct {
	GatewayType          string        `json:"gatewayType,omitempty"`
	VPNType              string        `json:"vpnType,omitempty"`
	VPNGatewayGeneration string        `json:"vpnGatewayGeneration,omitempty"`
	SKU                  *gatewaySKU   `json:"sku,omitempty"`
	EnableBGP            *bool         `json:"enableBgp,omitempty"`
	ActiveActive         *bool         `json:"activeActive,omitempty"`
	IPConfigurations     []vngIPConfig `json:"ipConfigurations,omitempty"`
	BgpSettings          *bgpSettings  `json:"bgpSettings,omitempty"`
}

type vngResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Properties vngResponseProps  `json:"properties"`
}

type vngResponseProps struct {
	ProvisioningState string        `json:"provisioningState"`
	GatewayType       string        `json:"gatewayType,omitempty"`
	VPNType           string        `json:"vpnType,omitempty"`
	VPNGeneration     string        `json:"vpnGatewayGeneration,omitempty"`
	SKU               *gatewaySKU   `json:"sku,omitempty"`
	EnableBGP         bool          `json:"enableBgp"`
	ActiveActive      bool          `json:"activeActive"`
	IPConfigurations  []vngIPConfig `json:"ipConfigurations,omitempty"`
	BgpSettings       *bgpSettings  `json:"bgpSettings,omitempty"`
}

type vngListResponse struct {
	Value []vngResponse `json:"value"`
}

// localNetworkGateways.

type lngRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties lngRequestProps   `json:"properties"`
}

type lngRequestProps struct {
	GatewayIPAddress         string        `json:"gatewayIpAddress,omitempty"`
	Fqdn                     string        `json:"fqdn,omitempty"`
	LocalNetworkAddressSpace *addressSpace `json:"localNetworkAddressSpace,omitempty"`
	BgpSettings              *bgpSettings  `json:"bgpSettings,omitempty"`
}

type lngResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Properties lngResponseProps  `json:"properties"`
}

type lngResponseProps struct {
	ProvisioningState        string        `json:"provisioningState"`
	GatewayIPAddress         string        `json:"gatewayIpAddress,omitempty"`
	Fqdn                     string        `json:"fqdn,omitempty"`
	LocalNetworkAddressSpace *addressSpace `json:"localNetworkAddressSpace,omitempty"`
	BgpSettings              *bgpSettings  `json:"bgpSettings,omitempty"`
}

type lngListResponse struct {
	Value []lngResponse `json:"value"`
}

// connections (Microsoft.Network/connections).

type connRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties connRequestProps  `json:"properties"`
}

type connRequestProps struct {
	ConnectionType         string    `json:"connectionType,omitempty"`
	ConnectionProtocol     string    `json:"connectionProtocol,omitempty"`
	VirtualNetworkGateway1 *armIDRef `json:"virtualNetworkGateway1,omitempty"`
	VirtualNetworkGateway2 *armIDRef `json:"virtualNetworkGateway2,omitempty"`
	LocalNetworkGateway2   *armIDRef `json:"localNetworkGateway2,omitempty"`
	SharedKey              string    `json:"sharedKey,omitempty"`
	RoutingWeight          int32     `json:"routingWeight,omitempty"`
	EnableBGP              *bool     `json:"enableBgp,omitempty"`
}

type connResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Properties connResponseProps `json:"properties"`
}

type connResponseProps struct {
	ProvisioningState      string    `json:"provisioningState"`
	ConnectionStatus       string    `json:"connectionStatus,omitempty"`
	ConnectionType         string    `json:"connectionType,omitempty"`
	ConnectionProtocol     string    `json:"connectionProtocol,omitempty"`
	VirtualNetworkGateway1 *armIDRef `json:"virtualNetworkGateway1,omitempty"`
	VirtualNetworkGateway2 *armIDRef `json:"virtualNetworkGateway2,omitempty"`
	LocalNetworkGateway2   *armIDRef `json:"localNetworkGateway2,omitempty"`
	SharedKey              string    `json:"sharedKey,omitempty"`
	RoutingWeight          int32     `json:"routingWeight,omitempty"`
	EnableBGP              bool      `json:"enableBgp"`
}

type connListResponse struct {
	Value []connResponse `json:"value"`
}

// gatewayCap returns the site-to-site VPN surface if the networking driver
// implements it (the Azure provider does; AWS/GCP do not).
func (h *Handler) gatewayCap() (netdriver.AzureNetworkGateways, bool) {
	svc, ok := h.net.(netdriver.AzureNetworkGateways)

	return svc, ok
}

// routeVNGateway dispatches Microsoft.Network/virtualNetworkGateways requests.
//
//nolint:gocritic,dupl // rp is request-scoped; capability-gated dispatch mirrored by routeLNGateway over a distinct type
func (h *Handler) routeVNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	svc, ok := h.gatewayCap()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"network gateways are not supported by this networking driver")

		return
	}

	if rp.ResourceName == "" {
		h.listVNGateways(w, r, rp, svc)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createVNGateway(w, r, rp, svc)
	case http.MethodPatch:
		h.patchVNGateway(w, r, rp, svc)
	case http.MethodGet:
		h.getVNGateway(w, r, rp, svc)
	case http.MethodDelete:
		h.deleteVNGateway(w, r, rp, svc)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic,dupl // rp is request-scoped; the capability-gated create shape is mirrored by createPrivateLinkService
func (*Handler) createVNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req vngRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	stored := svc.PutAzureVirtualNetworkGateway(r.Context(), vngRecord(rp, loc, &req))

	// Create is a long LRO in real Azure; a sync 200 with a terminal
	// provisioningState completes the armnetwork poller immediately (the same
	// convention the vnet/NAT-gateway creates use), so the SDK never hangs.
	writeAcceptedAsync(w, r, rp.Subscription, "vng-create-"+rp.ResourceName, vngResponseFrom(stored, rp))
}

// vngRecord maps a decoded PUT body to the driver record.
//
//nolint:gocritic // rp is a request-scoped value
func vngRecord(rp azurearm.ResourcePath, loc string, req *vngRequest) netdriver.AzureVirtualNetworkGateway {
	rec := netdriver.AzureVirtualNetworkGateway{
		Name:             rp.ResourceName,
		ResourceGroup:    rp.ResourceGroup,
		Location:         loc,
		Tags:             req.Tags,
		GatewayType:      req.Properties.GatewayType,
		VPNType:          req.Properties.VPNType,
		VPNGeneration:    req.Properties.VPNGatewayGeneration,
		EnableBGP:        derefBool(req.Properties.EnableBGP),
		ActiveActive:     derefBool(req.Properties.ActiveActive),
		IPConfigurations: toRecordIPConfigs(req.Properties.IPConfigurations),
		BgpSettings:      toRecordBgp(req.Properties.BgpSettings),
	}

	if req.Properties.SKU != nil {
		rec.SKUName = req.Properties.SKU.Name
		rec.SKUTier = req.Properties.SKU.Tier
	}

	return rec
}

func toRecordIPConfigs(in []vngIPConfig) []netdriver.AzureGatewayIPConfiguration {
	if len(in) == 0 {
		return nil
	}

	out := make([]netdriver.AzureGatewayIPConfiguration, 0, len(in))

	for i := range in {
		cfg := netdriver.AzureGatewayIPConfiguration{
			Name:                      in[i].Name,
			PrivateIPAllocationMethod: in[i].Properties.PrivateIPAllocationMethod,
		}

		if in[i].Properties.Subnet != nil {
			cfg.SubnetID = in[i].Properties.Subnet.ID
		}

		if in[i].Properties.PublicIPAddress != nil {
			cfg.PublicIPAddressID = in[i].Properties.PublicIPAddress.ID
		}

		out = append(out, cfg)
	}

	return out
}

func toRecordBgp(in *bgpSettings) *netdriver.AzureGatewayBgpSettings {
	if in == nil {
		return nil
	}

	return &netdriver.AzureGatewayBgpSettings{
		ASN:               in.ASN,
		BgpPeeringAddress: in.BgpPeeringAddress,
		PeerWeight:        in.PeerWeight,
	}
}

// patchVNGateway applies an ARM UpdateTags PATCH (VirtualNetworkGateways
// Client.BeginUpdateTags — an LRO): the body's tags are merged into the stored
// set, the gateway's other fields are left intact, and the full resource is
// returned. A sync 200 with a terminal provisioningState completes the poller
// immediately (the same convention the create uses). A PATCH on a missing
// gateway is a 404.
//
//nolint:gocritic // rp is a request-scoped value
func (*Handler) patchVNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	existing, ok := svc.GetAzureVirtualNetworkGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"virtual network gateway "+rp.ResourceName+" not found")

		return
	}

	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	existing.Tags = mergedTagMap(existing.Tags, req.Tags)
	stored := svc.PutAzureVirtualNetworkGateway(r.Context(), existing)

	writeAcceptedAsync(w, r, rp.Subscription, "vng-updatetags-"+rp.ResourceName, vngResponseFrom(stored, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getVNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	gw, ok := svc.GetAzureVirtualNetworkGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"virtual network gateway "+rp.ResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, vngResponseFrom(gw, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deleteVNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	svc.DeleteAzureVirtualNetworkGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	writeAcceptedAsync(w, r, rp.Subscription, "vng-delete-"+rp.ResourceName, nil)
}

//nolint:gocritic,dupl // rp is request-scoped; mirrors listLNGateways/listConnections over a distinct resource type
func (*Handler) listVNGateways(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	gws := svc.ListAzureVirtualNetworkGateways(r.Context(), rp.ResourceGroup)

	out := vngListResponse{Value: make([]vngResponse, 0, len(gws))}

	for i := range gws {
		scope := rp
		scope.ResourceGroup = gws[i].ResourceGroup
		scope.ResourceName = gws[i].Name
		out.Value = append(out.Value, vngResponseFrom(gws[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func vngResponseFrom(gw netdriver.AzureVirtualNetworkGateway, rp azurearm.ResourcePath) vngResponse {
	loc := gw.Location
	if loc == "" {
		loc = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeVNGateway, rp.ResourceName)

	props := vngResponseProps{
		ProvisioningState: provisioningSucceeded,
		GatewayType:       gw.GatewayType,
		VPNType:           gw.VPNType,
		VPNGeneration:     gw.VPNGeneration,
		EnableBGP:         gw.EnableBGP,
		ActiveActive:      gw.ActiveActive,
		IPConfigurations:  fromRecordIPConfigs(gw.IPConfigurations),
		BgpSettings:       fromRecordBgp(gw.BgpSettings),
	}

	if gw.SKUName != "" || gw.SKUTier != "" {
		props.SKU = &gatewaySKU{Name: gw.SKUName, Tier: gw.SKUTier}
	}

	return vngResponse{
		ID:         id,
		Name:       rp.ResourceName,
		Type:       providerName + "/" + typeVNGateway,
		Location:   loc,
		Tags:       gw.Tags,
		Etag:       etagOf(id),
		Properties: props,
	}
}

func fromRecordIPConfigs(in []netdriver.AzureGatewayIPConfiguration) []vngIPConfig {
	if len(in) == 0 {
		return nil
	}

	out := make([]vngIPConfig, 0, len(in))

	for i := range in {
		props := vngIPConfigProps{PrivateIPAllocationMethod: in[i].PrivateIPAllocationMethod}

		if in[i].SubnetID != "" {
			props.Subnet = &armIDRef{ID: in[i].SubnetID}
		}

		if in[i].PublicIPAddressID != "" {
			props.PublicIPAddress = &armIDRef{ID: in[i].PublicIPAddressID}
		}

		out = append(out, vngIPConfig{Name: in[i].Name, Properties: props})
	}

	return out
}

func fromRecordBgp(in *netdriver.AzureGatewayBgpSettings) *bgpSettings {
	if in == nil {
		return nil
	}

	return &bgpSettings{
		ASN:               in.ASN,
		BgpPeeringAddress: in.BgpPeeringAddress,
		PeerWeight:        in.PeerWeight,
	}
}

// routeLNGateway dispatches Microsoft.Network/localNetworkGateways requests.
//
//nolint:gocritic,dupl // rp is request-scoped; capability-gated dispatch mirrored by routeVNGateway over a distinct type
func (h *Handler) routeLNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	svc, ok := h.gatewayCap()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"network gateways are not supported by this networking driver")

		return
	}

	if rp.ResourceName == "" {
		h.listLNGateways(w, r, rp, svc)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createLNGateway(w, r, rp, svc)
	case http.MethodPatch:
		h.patchLNGateway(w, r, rp, svc)
	case http.MethodGet:
		h.getLNGateway(w, r, rp, svc)
	case http.MethodDelete:
		h.deleteLNGateway(w, r, rp, svc)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) createLNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req lngRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	rec := netdriver.AzureLocalNetworkGateway{
		Name:             rp.ResourceName,
		ResourceGroup:    rp.ResourceGroup,
		Location:         loc,
		Tags:             req.Tags,
		GatewayIPAddress: req.Properties.GatewayIPAddress,
		FQDN:             req.Properties.Fqdn,
		BgpSettings:      toRecordBgp(req.Properties.BgpSettings),
	}

	if req.Properties.LocalNetworkAddressSpace != nil {
		rec.AddressPrefixes = req.Properties.LocalNetworkAddressSpace.AddressPrefixes
	}

	stored := svc.PutAzureLocalNetworkGateway(r.Context(), rec)

	writeAcceptedAsync(w, r, rp.Subscription, "lng-create-"+rp.ResourceName, lngResponseFrom(stored, rp))
}

// patchLNGateway applies an ARM UpdateTags PATCH (LocalNetworkGatewaysClient.
// UpdateTags — a synchronous 200): the body's tags are merged into the stored
// set, the gateway's other fields are left intact, and the full resource is
// returned. A PATCH on a missing gateway is a 404.
//
//nolint:gocritic,dupl // rp is request-scoped; the tag-merge shape is shared by the sibling patch handlers
func (*Handler) patchLNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	existing, ok := svc.GetAzureLocalNetworkGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"local network gateway "+rp.ResourceName+" not found")

		return
	}

	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	existing.Tags = mergedTagMap(existing.Tags, req.Tags)
	stored := svc.PutAzureLocalNetworkGateway(r.Context(), existing)

	azurearm.WriteJSON(w, http.StatusOK, lngResponseFrom(stored, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getLNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	gw, ok := svc.GetAzureLocalNetworkGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"local network gateway "+rp.ResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, lngResponseFrom(gw, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deleteLNGateway(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	svc.DeleteAzureLocalNetworkGateway(r.Context(), rp.ResourceGroup, rp.ResourceName)
	writeAcceptedAsync(w, r, rp.Subscription, "lng-delete-"+rp.ResourceName, nil)
}

//nolint:gocritic,dupl // rp is request-scoped; mirrors listVNGateways/listConnections over a distinct resource type
func (*Handler) listLNGateways(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	gws := svc.ListAzureLocalNetworkGateways(r.Context(), rp.ResourceGroup)

	out := lngListResponse{Value: make([]lngResponse, 0, len(gws))}

	for i := range gws {
		scope := rp
		scope.ResourceGroup = gws[i].ResourceGroup
		scope.ResourceName = gws[i].Name
		out.Value = append(out.Value, lngResponseFrom(gws[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func lngResponseFrom(gw netdriver.AzureLocalNetworkGateway, rp azurearm.ResourcePath) lngResponse {
	loc := gw.Location
	if loc == "" {
		loc = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeLNGateway, rp.ResourceName)

	props := lngResponseProps{
		ProvisioningState: provisioningSucceeded,
		GatewayIPAddress:  gw.GatewayIPAddress,
		Fqdn:              gw.FQDN,
		BgpSettings:       fromRecordBgp(gw.BgpSettings),
	}

	if len(gw.AddressPrefixes) > 0 {
		props.LocalNetworkAddressSpace = &addressSpace{AddressPrefixes: gw.AddressPrefixes}
	}

	return lngResponse{
		ID:         id,
		Name:       rp.ResourceName,
		Type:       providerName + "/" + typeLNGateway,
		Location:   loc,
		Tags:       gw.Tags,
		Etag:       etagOf(id),
		Properties: props,
	}
}

// routeConnection dispatches Microsoft.Network/connections requests.
//
//nolint:gocritic,dupl // rp is request-scoped; capability-gated dispatch mirrored by routeVNGateway over a distinct type
func (h *Handler) routeConnection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	svc, ok := h.gatewayCap()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"network gateways are not supported by this networking driver")

		return
	}

	if rp.ResourceName == "" {
		h.listConnections(w, r, rp, svc)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createConnection(w, r, rp, svc)
	case http.MethodPatch:
		h.patchConnection(w, r, rp, svc)
	case http.MethodGet:
		h.getConnection(w, r, rp, svc)
	case http.MethodDelete:
		h.deleteConnection(w, r, rp, svc)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) createConnection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req connRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	rec := netdriver.AzureVirtualNetworkGatewayConnection{
		Name:                     rp.ResourceName,
		ResourceGroup:            rp.ResourceGroup,
		Location:                 loc,
		Tags:                     req.Tags,
		ConnectionType:           req.Properties.ConnectionType,
		ConnectionProtocol:       req.Properties.ConnectionProtocol,
		VirtualNetworkGateway1ID: refID(req.Properties.VirtualNetworkGateway1),
		VirtualNetworkGateway2ID: refID(req.Properties.VirtualNetworkGateway2),
		LocalNetworkGateway2ID:   refID(req.Properties.LocalNetworkGateway2),
		SharedKey:                req.Properties.SharedKey,
		RoutingWeight:            req.Properties.RoutingWeight,
		EnableBGP:                derefBool(req.Properties.EnableBGP),
	}

	stored := svc.PutAzureVirtualNetworkGatewayConnection(r.Context(), rec)

	writeAcceptedAsync(w, r, rp.Subscription, "conn-create-"+rp.ResourceName, connResponseFrom(stored, rp))
}

// patchConnection applies an ARM UpdateTags PATCH (VirtualNetworkGateway
// ConnectionsClient.BeginUpdateTags — an LRO): the body's tags are merged into
// the stored set, the connection's other fields are left intact, and the full
// resource is returned. A sync 200 with a terminal provisioningState completes
// the poller immediately (the same convention the create uses). A PATCH on a
// missing connection is a 404.
//
//nolint:gocritic // rp is a request-scoped value
func (*Handler) patchConnection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	existing, ok := svc.GetAzureVirtualNetworkGatewayConnection(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"connection "+rp.ResourceName+" not found")

		return
	}

	var req armTagsObject

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	existing.Tags = mergedTagMap(existing.Tags, req.Tags)
	stored := svc.PutAzureVirtualNetworkGatewayConnection(r.Context(), existing)

	writeAcceptedAsync(w, r, rp.Subscription, "conn-updatetags-"+rp.ResourceName, connResponseFrom(stored, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getConnection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	conn, ok := svc.GetAzureVirtualNetworkGatewayConnection(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"connection "+rp.ResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, connResponseFrom(conn, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deleteConnection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	svc.DeleteAzureVirtualNetworkGatewayConnection(r.Context(), rp.ResourceGroup, rp.ResourceName)
	writeAcceptedAsync(w, r, rp.Subscription, "conn-delete-"+rp.ResourceName, nil)
}

//nolint:gocritic,dupl // rp is request-scoped; mirrors listVNGateways/listLNGateways over a distinct resource type
func (*Handler) listConnections(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkGateways,
) {
	conns := svc.ListAzureVirtualNetworkGatewayConnections(r.Context(), rp.ResourceGroup)

	out := connListResponse{Value: make([]connResponse, 0, len(conns))}

	for i := range conns {
		scope := rp
		scope.ResourceGroup = conns[i].ResourceGroup
		scope.ResourceName = conns[i].Name
		out.Value = append(out.Value, connResponseFrom(conns[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func connResponseFrom(conn netdriver.AzureVirtualNetworkGatewayConnection, rp azurearm.ResourcePath) connResponse {
	loc := conn.Location
	if loc == "" {
		loc = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeConnection, rp.ResourceName)

	return connResponse{
		ID:       id,
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeConnection,
		Location: loc,
		Tags:     conn.Tags,
		Etag:     etagOf(id),
		Properties: connResponseProps{
			ProvisioningState:      provisioningSucceeded,
			ConnectionStatus:       connectionStatusConnected,
			ConnectionType:         conn.ConnectionType,
			ConnectionProtocol:     conn.ConnectionProtocol,
			VirtualNetworkGateway1: refOf(conn.VirtualNetworkGateway1ID),
			VirtualNetworkGateway2: refOf(conn.VirtualNetworkGateway2ID),
			LocalNetworkGateway2:   refOf(conn.LocalNetworkGateway2ID),
			SharedKey:              conn.SharedKey,
			RoutingWeight:          conn.RoutingWeight,
			EnableBGP:              conn.EnableBGP,
		},
	}
}

// refID returns an armIDRef's id, or "" when the reference is nil.
func refID(ref *armIDRef) string {
	if ref == nil {
		return ""
	}

	return ref.ID
}

// refOf wraps a non-empty id back into an armIDRef, or nil when the id is empty.
func refOf(id string) *armIDRef {
	if id == "" {
		return nil
	}

	return &armIDRef{ID: id}
}

// derefBool returns the pointed-to bool, or false when the pointer is nil.
func derefBool(b *bool) bool {
	return b != nil && *b
}

// purgeNetworkGateways deletes every connection, then every virtual and local
// network gateway in the resource group, part of the PurgeResourceGroup cascade.
// Connections are removed first as they reference the gateways.
func (h *Handler) purgeNetworkGateways(ctx context.Context, resourceGroup string) {
	svc, ok := h.gatewayCap()
	if !ok {
		return
	}

	conns := svc.ListAzureVirtualNetworkGatewayConnections(ctx, resourceGroup)
	for i := range conns {
		svc.DeleteAzureVirtualNetworkGatewayConnection(ctx, conns[i].ResourceGroup, conns[i].Name)
	}

	vngs := svc.ListAzureVirtualNetworkGateways(ctx, resourceGroup)
	for i := range vngs {
		svc.DeleteAzureVirtualNetworkGateway(ctx, vngs[i].ResourceGroup, vngs[i].Name)
	}

	lngs := svc.ListAzureLocalNetworkGateways(ctx, resourceGroup)
	for i := range lngs {
		svc.DeleteAzureLocalNetworkGateway(ctx, lngs[i].ResourceGroup, lngs[i].Name)
	}
}
