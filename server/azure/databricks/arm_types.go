package databricks

import dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"

// JSON wire shapes for the extended Microsoft.Databricks ARM surface (#209).
// Field names match what the real armdatabricks client emits and expects.

// --- Access connectors ---

type armAccessConnector struct {
	ID         string                `json:"id,omitempty"`
	Name       string                `json:"name,omitempty"`
	Type       string                `json:"type,omitempty"`
	Location   string                `json:"location,omitempty"`
	Tags       map[string]string     `json:"tags,omitempty"`
	Identity   *armIdentity          `json:"identity,omitempty"`
	Properties *accessConnectorProps `json:"properties,omitempty"`
}

type accessConnectorProps struct {
	ProvisioningState string `json:"provisioningState,omitempty"`
}

// armIdentity is the ARM managed-service-identity envelope.
type armIdentity struct {
	Type                   string                      `json:"type,omitempty"`
	PrincipalID            string                      `json:"principalId,omitempty"`
	TenantID               string                      `json:"tenantId,omitempty"`
	UserAssignedIdentities map[string]*armUserAssigned `json:"userAssignedIdentities,omitempty"`
}

type armUserAssigned struct {
	PrincipalID string `json:"principalId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
}

// accessConnectorUpdate is the PATCH body (tags and/or identity).
type accessConnectorUpdate struct {
	Tags     map[string]string `json:"tags,omitempty"`
	Identity *armIdentity      `json:"identity,omitempty"`
}

type armAccessConnectorList struct {
	Value    []armAccessConnector `json:"value"`
	NextLink string               `json:"nextLink,omitempty"`
}

func toARMAccessConnector(ac *dbxdriver.AccessConnector) armAccessConnector {
	out := armAccessConnector{
		ID:       ac.ID,
		Name:     ac.Name,
		Type:     providerName + "/" + accessConnectorsType,
		Location: ac.Location,
		Tags:     ac.Tags,
		Identity: toARMIdentity(ac.Identity),
		Properties: &accessConnectorProps{
			ProvisioningState: ac.ProvisioningState,
		},
	}

	return out
}

func toARMIdentity(id *dbxdriver.ManagedIdentity) *armIdentity {
	if id == nil {
		return nil
	}

	out := &armIdentity{
		Type:        id.Type,
		PrincipalID: id.PrincipalID,
		TenantID:    id.TenantID,
	}

	if len(id.UserAssigned) > 0 {
		out.UserAssignedIdentities = make(map[string]*armUserAssigned, len(id.UserAssigned))
		for _, u := range id.UserAssigned {
			out.UserAssignedIdentities[u] = &armUserAssigned{}
		}
	}

	return out
}

// fromARMIdentity converts an inbound ARM identity to the driver shape.
func fromARMIdentity(id *armIdentity) *dbxdriver.ManagedIdentity {
	if id == nil {
		return nil
	}

	out := &dbxdriver.ManagedIdentity{Type: id.Type}
	for k := range id.UserAssignedIdentities {
		out.UserAssigned = append(out.UserAssigned, k)
	}

	return out
}

// --- Private endpoint connections ---

type armPEC struct {
	ID         string    `json:"id,omitempty"`
	Name       string    `json:"name,omitempty"`
	Type       string    `json:"type,omitempty"`
	Properties *pecProps `json:"properties,omitempty"`
}

type pecProps struct {
	PrivateEndpoint                   *armSubResource `json:"privateEndpoint,omitempty"`
	PrivateLinkServiceConnectionState *plsConnState   `json:"privateLinkServiceConnectionState,omitempty"`
	GroupIDs                          []string        `json:"groupIds,omitempty"`
	ProvisioningState                 string          `json:"provisioningState,omitempty"`
}

type armSubResource struct {
	ID string `json:"id,omitempty"`
}

type plsConnState struct {
	Status          string `json:"status,omitempty"`
	Description     string `json:"description,omitempty"`
	ActionsRequired string `json:"actionsRequired,omitempty"`
}

type armPECList struct {
	Value    []armPEC `json:"value"`
	NextLink string   `json:"nextLink,omitempty"`
}

func toARMPEC(c *dbxdriver.PrivateEndpointConnection) armPEC {
	props := &pecProps{
		GroupIDs:          c.GroupIDs,
		ProvisioningState: c.ProvisioningState,
		PrivateLinkServiceConnectionState: &plsConnState{
			Status:          c.Status,
			Description:     c.Description,
			ActionsRequired: c.ActionsRequired,
		},
	}
	if c.PrivateEndpointID != "" {
		props.PrivateEndpoint = &armSubResource{ID: c.PrivateEndpointID}
	}

	return armPEC{
		ID:         c.ID,
		Name:       c.Name,
		Type:       providerName + "/" + resourceType + "/" + subPEC,
		Properties: props,
	}
}

// --- Private link resources ---

type armGroupIDInformation struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Properties *groupIDInfoProps `json:"properties,omitempty"`
}

type groupIDInfoProps struct {
	GroupID           string   `json:"groupId,omitempty"`
	RequiredMembers   []string `json:"requiredMembers,omitempty"`
	RequiredZoneNames []string `json:"requiredZoneNames,omitempty"`
}

type armPLRList struct {
	Value    []armGroupIDInformation `json:"value"`
	NextLink string                  `json:"nextLink,omitempty"`
}

func toARMGroupIDInformation(g *dbxdriver.GroupIDInformation) armGroupIDInformation {
	return armGroupIDInformation{
		ID:   g.ID,
		Name: g.Name,
		Type: providerName + "/" + resourceType + "/" + subPLR,
		Properties: &groupIDInfoProps{
			GroupID:           g.GroupID,
			RequiredMembers:   g.RequiredMembers,
			RequiredZoneNames: g.RequiredZoneNames,
		},
	}
}

// --- Virtual network peerings ---

type armPeering struct {
	ID         string        `json:"id,omitempty"`
	Name       string        `json:"name,omitempty"`
	Type       string        `json:"type,omitempty"`
	Properties *peeringProps `json:"properties,omitempty"`
}

type peeringProps struct {
	AllowForwardedTraffic     bool             `json:"allowForwardedTraffic"`
	AllowGatewayTransit       bool             `json:"allowGatewayTransit"`
	AllowVirtualNetworkAccess bool             `json:"allowVirtualNetworkAccess"`
	UseRemoteGateways         bool             `json:"useRemoteGateways"`
	DatabricksVirtualNetwork  *armSubResource  `json:"databricksVirtualNetwork,omitempty"`
	DatabricksAddressSpace    *armAddressSpace `json:"databricksAddressSpace,omitempty"`
	RemoteVirtualNetwork      *armSubResource  `json:"remoteVirtualNetwork,omitempty"`
	RemoteAddressSpace        *armAddressSpace `json:"remoteAddressSpace,omitempty"`
	PeeringState              string           `json:"peeringState,omitempty"`
	ProvisioningState         string           `json:"provisioningState,omitempty"`
}

type armAddressSpace struct {
	AddressPrefixes []string `json:"addressPrefixes,omitempty"`
}

type armPeeringList struct {
	Value    []armPeering `json:"value"`
	NextLink string       `json:"nextLink,omitempty"`
}

func toARMPeering(p *dbxdriver.VirtualNetworkPeering) armPeering {
	props := &peeringProps{
		AllowForwardedTraffic:     p.AllowForwardedTraffic,
		AllowGatewayTransit:       p.AllowGatewayTransit,
		AllowVirtualNetworkAccess: p.AllowVirtualNetworkAccess,
		UseRemoteGateways:         p.UseRemoteGateways,
		DatabricksAddressSpace:    toARMAddressSpace(p.DatabricksAddressSpace),
		RemoteAddressSpace:        toARMAddressSpace(p.RemoteAddressSpace),
		PeeringState:              p.PeeringState,
		ProvisioningState:         p.ProvisioningState,
	}
	if p.DatabricksVNetID != "" {
		props.DatabricksVirtualNetwork = &armSubResource{ID: p.DatabricksVNetID}
	}

	if p.RemoteVNetID != "" {
		props.RemoteVirtualNetwork = &armSubResource{ID: p.RemoteVNetID}
	}

	return armPeering{
		ID:         p.ID,
		Name:       p.Name,
		Type:       providerName + "/" + resourceType + "/" + subPeering,
		Properties: props,
	}
}

func toARMAddressSpace(in *dbxdriver.AddressSpace) *armAddressSpace {
	if in == nil {
		return nil
	}

	return &armAddressSpace{AddressPrefixes: in.AddressPrefixes}
}

func fromARMAddressSpace(in *armAddressSpace) *dbxdriver.AddressSpace {
	if in == nil {
		return nil
	}

	return &dbxdriver.AddressSpace{AddressPrefixes: in.AddressPrefixes}
}

// --- Outbound network dependencies ---

type armOutboundEndpoint struct {
	Category  string                  `json:"category,omitempty"`
	Endpoints []armEndpointDependency `json:"endpoints,omitempty"`
}

type armEndpointDependency struct {
	DomainName      string              `json:"domainName,omitempty"`
	EndpointDetails []armEndpointDetail `json:"endpointDetails,omitempty"`
}

type armEndpointDetail struct {
	Port int32 `json:"port,omitempty"`
}

func toARMOutbound(e *dbxdriver.OutboundEndpoint) armOutboundEndpoint {
	out := armOutboundEndpoint{Category: e.Category}

	for i := range e.Endpoints {
		dep := armEndpointDependency{DomainName: e.Endpoints[i].DomainName}
		for j := range e.Endpoints[i].EndpointDetails {
			dep.EndpointDetails = append(dep.EndpointDetails,
				armEndpointDetail{Port: e.Endpoints[i].EndpointDetails[j].Port})
		}

		out.Endpoints = append(out.Endpoints, dep)
	}

	return out
}

// --- Operations ---

type armOperation struct {
	Name         string            `json:"name,omitempty"`
	IsDataAction bool              `json:"isDataAction"`
	Display      *armOperationDisp `json:"display,omitempty"`
}

type armOperationDisp struct {
	Provider    string `json:"provider,omitempty"`
	Resource    string `json:"resource,omitempty"`
	Operation   string `json:"operation,omitempty"`
	Description string `json:"description,omitempty"`
}

type armOperationList struct {
	Value    []armOperation `json:"value"`
	NextLink string         `json:"nextLink,omitempty"`
}

func toARMOperation(o *dbxdriver.Operation) armOperation {
	return armOperation{
		Name:         o.Name,
		IsDataAction: o.IsDataAction,
		Display: &armOperationDisp{
			Provider:    o.Provider,
			Resource:    o.Resource,
			Operation:   o.Operation,
			Description: o.Description,
		},
	}
}
