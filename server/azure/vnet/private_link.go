package vnet

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Microsoft.Network Private Link resource types.
const (
	typePrivateEndpoint    = "privateEndpoints"
	typePrivateLinkService = "privateLinkServices"

	// connectionStatusApproved is the default state the emulator stamps on a new
	// private endpoint connection: Private Link auto-approves here rather than
	// leaving it Pending for an out-of-band approval workflow.
	connectionStatusApproved = "Approved"

	// plsAliasHashLen is how many hash characters the synthesized private link
	// service alias carries (real Azure aliases embed a short opaque token).
	plsAliasHashLen = 12
)

// ARM JSON shapes — privateEndpoints.

type peConnectionState struct {
	Status          string `json:"status,omitempty"`
	Description     string `json:"description,omitempty"`
	ActionsRequired string `json:"actionsRequired,omitempty"`
}

type peConnectionProps struct {
	PrivateLinkServiceID              string             `json:"privateLinkServiceId,omitempty"`
	GroupIDs                          []string           `json:"groupIds,omitempty"`
	RequestMessage                    string             `json:"requestMessage,omitempty"`
	PrivateLinkServiceConnectionState *peConnectionState `json:"privateLinkServiceConnectionState,omitempty"`
}

type peConnection struct {
	Name       string            `json:"name,omitempty"`
	Properties peConnectionProps `json:"properties"`
}

type privateEndpointRequest struct {
	Location   string                      `json:"location"`
	Tags       map[string]string           `json:"tags,omitempty"`
	Properties privateEndpointRequestProps `json:"properties"`
}

type privateEndpointRequestProps struct {
	Subnet                        *armIDRef      `json:"subnet,omitempty"`
	CustomNetworkInterfaceName    string         `json:"customNetworkInterfaceName,omitempty"`
	PrivateLinkServiceConnections []peConnection `json:"privateLinkServiceConnections,omitempty"`
	ManualPrivateLinkServiceConns []peConnection `json:"manualPrivateLinkServiceConnections,omitempty"`
}

type privateEndpointResponse struct {
	ID         string                       `json:"id"`
	Name       string                       `json:"name"`
	Type       string                       `json:"type"`
	Location   string                       `json:"location"`
	Tags       map[string]string            `json:"tags,omitempty"`
	Etag       string                       `json:"etag,omitempty"`
	Properties privateEndpointResponseProps `json:"properties"`
}

type privateEndpointResponseProps struct {
	ProvisioningState             string         `json:"provisioningState"`
	Subnet                        *armIDRef      `json:"subnet,omitempty"`
	CustomNetworkInterfaceName    string         `json:"customNetworkInterfaceName,omitempty"`
	PrivateLinkServiceConnections []peConnection `json:"privateLinkServiceConnections,omitempty"`
	ManualPrivateLinkServiceConns []peConnection `json:"manualPrivateLinkServiceConnections,omitempty"`
	NetworkInterfaces             []armIDRef     `json:"networkInterfaces,omitempty"`
}

type privateEndpointListResponse struct {
	Value []privateEndpointResponse `json:"value"`
}

// ARM JSON shapes — privateLinkServices.

type plsIPConfigProps struct {
	PrivateIPAllocationMethod string    `json:"privateIPAllocationMethod,omitempty"`
	Primary                   bool      `json:"primary,omitempty"`
	Subnet                    *armIDRef `json:"subnet,omitempty"`
}

type plsIPConfig struct {
	Name       string           `json:"name,omitempty"`
	Properties plsIPConfigProps `json:"properties"`
}

type plsSubscriptions struct {
	Subscriptions []string `json:"subscriptions,omitempty"`
}

type privateLinkServiceRequest struct {
	Location   string                         `json:"location"`
	Tags       map[string]string              `json:"tags,omitempty"`
	Properties privateLinkServiceRequestProps `json:"properties"`
}

type privateLinkServiceRequestProps struct {
	LoadBalancerFrontendIPConfigurations []armIDRef        `json:"loadBalancerFrontendIpConfigurations,omitempty"`
	IPConfigurations                     []plsIPConfig     `json:"ipConfigurations,omitempty"`
	Visibility                           *plsSubscriptions `json:"visibility,omitempty"`
	AutoApproval                         *plsSubscriptions `json:"autoApproval,omitempty"`
	EnableProxyProtocol                  *bool             `json:"enableProxyProtocol,omitempty"`
	Fqdns                                []string          `json:"fqdns,omitempty"`
}

type privateLinkServiceResponse struct {
	ID         string                          `json:"id"`
	Name       string                          `json:"name"`
	Type       string                          `json:"type"`
	Location   string                          `json:"location"`
	Tags       map[string]string               `json:"tags,omitempty"`
	Etag       string                          `json:"etag,omitempty"`
	Properties privateLinkServiceResponseProps `json:"properties"`
}

type privateLinkServiceResponseProps struct {
	ProvisioningState                    string            `json:"provisioningState"`
	Alias                                string            `json:"alias,omitempty"`
	LoadBalancerFrontendIPConfigurations []armIDRef        `json:"loadBalancerFrontendIpConfigurations,omitempty"`
	IPConfigurations                     []plsIPConfig     `json:"ipConfigurations,omitempty"`
	Visibility                           *plsSubscriptions `json:"visibility,omitempty"`
	AutoApproval                         *plsSubscriptions `json:"autoApproval,omitempty"`
	EnableProxyProtocol                  bool              `json:"enableProxyProtocol"`
	Fqdns                                []string          `json:"fqdns,omitempty"`
	NetworkInterfaces                    []armIDRef        `json:"networkInterfaces,omitempty"`
	PrivateEndpointConnections           []any             `json:"privateEndpointConnections,omitempty"`
}

type privateLinkServiceListResponse struct {
	Value []privateLinkServiceResponse `json:"value"`
}

// privateLinkCap returns the Private Link surface if the networking driver
// implements it (the Azure provider does; AWS/GCP do not).
func (h *Handler) privateLinkCap() (netdriver.AzurePrivateLink, bool) {
	svc, ok := h.net.(netdriver.AzurePrivateLink)

	return svc, ok
}

// routePrivateLinkResourceType dispatches the Private Link resource types
// (privateEndpoints, privateLinkServices). Split out of routeByResourceType so
// its dispatch switch stays under the cyclomatic-complexity gate. It returns
// true when it handled the request.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routePrivateLinkResourceType(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) bool {
	switch rp.ResourceType {
	case typePrivateEndpoint:
		h.routePrivateEndpoint(w, r, rp)
	case typePrivateLinkService:
		h.routePrivateLinkService(w, r, rp)
	default:
		return false
	}

	return true
}

// routePrivateEndpoint dispatches Microsoft.Network/privateEndpoints requests.
//
//nolint:gocritic,dupl // rp is request-scoped; capability-gated dispatch mirrored by routePrivateLinkService over a distinct type
func (h *Handler) routePrivateEndpoint(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	svc, ok := h.privateLinkCap()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"private link is not supported by this networking driver")

		return
	}

	if rp.ResourceName == "" {
		h.listPrivateEndpoints(w, r, rp, svc)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createPrivateEndpoint(w, r, rp, svc)
	case http.MethodGet:
		h.getPrivateEndpoint(w, r, rp, svc)
	case http.MethodDelete:
		h.deletePrivateEndpoint(w, r, rp, svc)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) createPrivateEndpoint(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePrivateLink,
) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req privateEndpointRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	rec := netdriver.AzurePrivateEndpoint{
		Name:                          rp.ResourceName,
		ResourceGroup:                 rp.ResourceGroup,
		Location:                      loc,
		Tags:                          req.Tags,
		CustomNetworkInterfaceName:    req.Properties.CustomNetworkInterfaceName,
		PrivateLinkServiceConnections: toRecordConnections(req.Properties.PrivateLinkServiceConnections),
		ManualConnections:             toRecordConnections(req.Properties.ManualPrivateLinkServiceConns),
	}

	if req.Properties.Subnet != nil {
		rec.SubnetID = req.Properties.Subnet.ID
	}

	stored := svc.PutAzurePrivateEndpoint(r.Context(), rec)

	// Create is a long LRO in real Azure; a sync 200 with a terminal
	// provisioningState completes the armnetwork poller immediately (the same
	// convention the vnet/gateway creates use), so the SDK never hangs.
	writeAcceptedAsync(w, r, rp.Subscription, "pe-create-"+rp.ResourceName, privateEndpointResponseFrom(stored, rp))
}

// toRecordConnections maps decoded privateLinkServiceConnections to driver
// records, defaulting an unspecified connection state to Approved (the emulator
// auto-approves).
func toRecordConnections(in []peConnection) []netdriver.AzurePrivateLinkServiceConnection {
	if len(in) == 0 {
		return nil
	}

	out := make([]netdriver.AzurePrivateLinkServiceConnection, 0, len(in))

	for i := range in {
		conn := netdriver.AzurePrivateLinkServiceConnection{
			Name:                 in[i].Name,
			PrivateLinkServiceID: in[i].Properties.PrivateLinkServiceID,
			GroupIDs:             in[i].Properties.GroupIDs,
			RequestMessage:       in[i].Properties.RequestMessage,
			Status:               connectionStatusApproved,
		}

		if st := in[i].Properties.PrivateLinkServiceConnectionState; st != nil {
			if st.Status != "" {
				conn.Status = st.Status
			}

			conn.Description = st.Description
		}

		out = append(out, conn)
	}

	return out
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getPrivateEndpoint(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePrivateLink,
) {
	pe, ok := svc.GetAzurePrivateEndpoint(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"private endpoint "+rp.ResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, privateEndpointResponseFrom(pe, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deletePrivateEndpoint(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePrivateLink,
) {
	svc.DeleteAzurePrivateEndpoint(r.Context(), rp.ResourceGroup, rp.ResourceName)
	writeAcceptedAsync(w, r, rp.Subscription, "pe-delete-"+rp.ResourceName, nil)
}

//nolint:gocritic,dupl // rp is request-scoped; mirrors listPrivateLinkServices over a distinct resource type
func (*Handler) listPrivateEndpoints(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePrivateLink,
) {
	pes := svc.ListAzurePrivateEndpoints(r.Context(), rp.ResourceGroup)

	out := privateEndpointListResponse{Value: make([]privateEndpointResponse, 0, len(pes))}

	for i := range pes {
		scope := rp
		scope.ResourceGroup = pes[i].ResourceGroup
		scope.ResourceName = pes[i].Name
		out.Value = append(out.Value, privateEndpointResponseFrom(pes[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func privateEndpointResponseFrom(pe netdriver.AzurePrivateEndpoint, rp azurearm.ResourcePath) privateEndpointResponse {
	loc := pe.Location
	if loc == "" {
		loc = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typePrivateEndpoint, rp.ResourceName)

	props := privateEndpointResponseProps{
		ProvisioningState:             provisioningSucceeded,
		CustomNetworkInterfaceName:    pe.CustomNetworkInterfaceName,
		PrivateLinkServiceConnections: fromRecordConnections(pe.PrivateLinkServiceConnections),
		ManualPrivateLinkServiceConns: fromRecordConnections(pe.ManualConnections),
		// A real private endpoint materializes a NIC; expose the synthesized
		// read-only reference so a caller inspecting networkInterfaces sees one.
		NetworkInterfaces: []armIDRef{{ID: azurearm.BuildResourceID(
			rp.Subscription, rp.ResourceGroup, providerName, typeNIC, rp.ResourceName+".nic")}},
	}

	if pe.SubnetID != "" {
		props.Subnet = &armIDRef{ID: pe.SubnetID}
	}

	return privateEndpointResponse{
		ID:         id,
		Name:       rp.ResourceName,
		Type:       providerName + "/" + typePrivateEndpoint,
		Location:   loc,
		Tags:       pe.Tags,
		Etag:       etagOf(id),
		Properties: props,
	}
}

func fromRecordConnections(in []netdriver.AzurePrivateLinkServiceConnection) []peConnection {
	if len(in) == 0 {
		return nil
	}

	out := make([]peConnection, 0, len(in))

	for i := range in {
		out = append(out, peConnection{
			Name: in[i].Name,
			Properties: peConnectionProps{
				PrivateLinkServiceID: in[i].PrivateLinkServiceID,
				GroupIDs:             in[i].GroupIDs,
				RequestMessage:       in[i].RequestMessage,
				PrivateLinkServiceConnectionState: &peConnectionState{
					Status:      in[i].Status,
					Description: in[i].Description,
				},
			},
		})
	}

	return out
}

// routePrivateLinkService dispatches Microsoft.Network/privateLinkServices requests.
//
//nolint:gocritic,dupl // rp is request-scoped; capability-gated dispatch mirrored by routePrivateEndpoint over a distinct type
func (h *Handler) routePrivateLinkService(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	svc, ok := h.privateLinkCap()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"private link is not supported by this networking driver")

		return
	}

	if rp.ResourceName == "" {
		h.listPrivateLinkServices(w, r, rp, svc)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createPrivateLinkService(w, r, rp, svc)
	case http.MethodGet:
		h.getPrivateLinkService(w, r, rp, svc)
	case http.MethodDelete:
		h.deletePrivateLinkService(w, r, rp, svc)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic,dupl // rp is request-scoped; the capability-gated create shape is mirrored by createVNGateway
func (*Handler) createPrivateLinkService(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePrivateLink,
) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req privateLinkServiceRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	stored := svc.PutAzurePrivateLinkService(r.Context(), plsRecord(rp, loc, &req))

	writeAcceptedAsync(w, r, rp.Subscription, "pls-create-"+rp.ResourceName, privateLinkServiceResponseFrom(stored, rp))
}

// plsRecord maps a decoded PUT body to the driver record.
//
//nolint:gocritic // rp is a request-scoped value
func plsRecord(rp azurearm.ResourcePath, loc string, req *privateLinkServiceRequest) netdriver.AzurePrivateLinkService {
	rec := netdriver.AzurePrivateLinkService{
		Name:                    rp.ResourceName,
		ResourceGroup:           rp.ResourceGroup,
		Location:                loc,
		Tags:                    req.Tags,
		LoadBalancerFrontendIDs: refIDs(req.Properties.LoadBalancerFrontendIPConfigurations),
		IPConfigurations:        toRecordPLSIPConfigs(req.Properties.IPConfigurations),
		EnableProxyProtocol:     derefBool(req.Properties.EnableProxyProtocol),
		Fqdns:                   req.Properties.Fqdns,
	}

	if req.Properties.Visibility != nil {
		rec.VisibilitySubscriptions = req.Properties.Visibility.Subscriptions
	}

	if req.Properties.AutoApproval != nil {
		rec.AutoApprovalSubs = req.Properties.AutoApproval.Subscriptions
	}

	return rec
}

func toRecordPLSIPConfigs(in []plsIPConfig) []netdriver.AzurePrivateLinkServiceIPConfiguration {
	if len(in) == 0 {
		return nil
	}

	out := make([]netdriver.AzurePrivateLinkServiceIPConfiguration, 0, len(in))

	for i := range in {
		cfg := netdriver.AzurePrivateLinkServiceIPConfiguration{
			Name:                      in[i].Name,
			PrivateIPAllocationMethod: in[i].Properties.PrivateIPAllocationMethod,
			Primary:                   in[i].Properties.Primary,
		}

		if in[i].Properties.Subnet != nil {
			cfg.SubnetID = in[i].Properties.Subnet.ID
		}

		out = append(out, cfg)
	}

	return out
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getPrivateLinkService(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePrivateLink,
) {
	pls, ok := svc.GetAzurePrivateLinkService(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"private link service "+rp.ResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, privateLinkServiceResponseFrom(pls, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deletePrivateLinkService(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePrivateLink,
) {
	svc.DeleteAzurePrivateLinkService(r.Context(), rp.ResourceGroup, rp.ResourceName)
	writeAcceptedAsync(w, r, rp.Subscription, "pls-delete-"+rp.ResourceName, nil)
}

//nolint:gocritic,dupl // rp is request-scoped; mirrors listPrivateEndpoints over a distinct resource type
func (*Handler) listPrivateLinkServices(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePrivateLink,
) {
	svcs := svc.ListAzurePrivateLinkServices(r.Context(), rp.ResourceGroup)

	out := privateLinkServiceListResponse{Value: make([]privateLinkServiceResponse, 0, len(svcs))}

	for i := range svcs {
		scope := rp
		scope.ResourceGroup = svcs[i].ResourceGroup
		scope.ResourceName = svcs[i].Name
		out.Value = append(out.Value, privateLinkServiceResponseFrom(svcs[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func privateLinkServiceResponseFrom(pls netdriver.AzurePrivateLinkService, rp azurearm.ResourcePath) privateLinkServiceResponse {
	loc := pls.Location
	if loc == "" {
		loc = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typePrivateLinkService, rp.ResourceName)

	props := privateLinkServiceResponseProps{
		ProvisioningState:                    provisioningSucceeded,
		Alias:                                rp.ResourceName + "." + shortEtag(id) + "." + loc + ".azure.privatelinkservice",
		LoadBalancerFrontendIPConfigurations: refsOf(pls.LoadBalancerFrontendIDs),
		IPConfigurations:                     fromRecordPLSIPConfigs(pls.IPConfigurations),
		EnableProxyProtocol:                  pls.EnableProxyProtocol,
		Fqdns:                                pls.Fqdns,
	}

	if len(pls.VisibilitySubscriptions) > 0 {
		props.Visibility = &plsSubscriptions{Subscriptions: pls.VisibilitySubscriptions}
	}

	if len(pls.AutoApprovalSubs) > 0 {
		props.AutoApproval = &plsSubscriptions{Subscriptions: pls.AutoApprovalSubs}
	}

	return privateLinkServiceResponse{
		ID:         id,
		Name:       rp.ResourceName,
		Type:       providerName + "/" + typePrivateLinkService,
		Location:   loc,
		Tags:       pls.Tags,
		Etag:       etagOf(id),
		Properties: props,
	}
}

func fromRecordPLSIPConfigs(in []netdriver.AzurePrivateLinkServiceIPConfiguration) []plsIPConfig {
	if len(in) == 0 {
		return nil
	}

	out := make([]plsIPConfig, 0, len(in))

	for i := range in {
		props := plsIPConfigProps{
			PrivateIPAllocationMethod: in[i].PrivateIPAllocationMethod,
			Primary:                   in[i].Primary,
		}

		if in[i].SubnetID != "" {
			props.Subnet = &armIDRef{ID: in[i].SubnetID}
		}

		out = append(out, plsIPConfig{Name: in[i].Name, Properties: props})
	}

	return out
}

// shortEtag reuses etagOf's hash to synthesize the stable alias segment real
// Azure assigns a private link service.
func shortEtag(id string) string {
	e := etagOf(id)
	if len(e) > plsAliasHashLen {
		return e[:plsAliasHashLen]
	}

	return e
}

// refsOf wraps a slice of ids back into armIDRefs.
func refsOf(ids []string) []armIDRef {
	if len(ids) == 0 {
		return nil
	}

	out := make([]armIDRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, armIDRef{ID: id})
	}

	return out
}

// purgePrivateLink deletes every private endpoint and private link service in
// the resource group, part of the PurgeResourceGroup cascade. Private endpoints
// are removed first as they reference the services.
func (h *Handler) purgePrivateLink(ctx context.Context, resourceGroup string) {
	svc, ok := h.privateLinkCap()
	if !ok {
		return
	}

	pes := svc.ListAzurePrivateEndpoints(ctx, resourceGroup)
	for i := range pes {
		svc.DeleteAzurePrivateEndpoint(ctx, pes[i].ResourceGroup, pes[i].Name)
	}

	svcs := svc.ListAzurePrivateLinkServices(ctx, resourceGroup)
	for i := range svcs {
		svc.DeleteAzurePrivateLinkService(ctx, svcs[i].ResourceGroup, svcs[i].Name)
	}
}
