package purview

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/purview"
)

// accountRequest is the ARM PUT/PATCH body. location, tags, identity and sku are
// top-level; the remaining configuration lives under properties.
type accountRequest struct {
	Location   string             `json:"location,omitempty"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Identity   *identityRequest   `json:"identity,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Properties *propertiesRequest `json:"properties,omitempty"`
}

// identityRequest is the writable half of the identity block; the minted ids are
// read-only. userAssignedIdentities is a map of ARM ids to (empty) objects on
// input.
type identityRequest struct {
	Type         string                     `json:"type"`
	UserAssigned map[string]json.RawMessage `json:"userAssignedIdentities,omitempty"`
}

// skuWire is the account sku block, top-level on both request and response.
type skuWire struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

// propertiesRequest is the writable subset of properties. Pointer fields let a
// PATCH overlay only what it names.
type propertiesRequest struct {
	PublicNetworkAccess      *string `json:"publicNetworkAccess,omitempty"`
	ManagedEventHubState     *string `json:"managedEventHubState,omitempty"`
	ManagedResourceGroupName *string `json:"managedResourceGroupName,omitempty"`
}

// accountResponse is the ARM representation of a Purview account resource.
type accountResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Identity   *identityResponse  `json:"identity,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Properties propertiesResponse `json:"properties"`
}

// identityResponse carries the identity block, including the service-minted ids.
type identityResponse struct {
	Type         string                         `json:"type"`
	PrincipalID  string                         `json:"principalId,omitempty"`
	TenantID     string                         `json:"tenantId,omitempty"`
	UserAssigned map[string]userAssignedWireVal `json:"userAssignedIdentities,omitempty"`
}

// userAssignedWireVal is the minted id pair for a user-assigned identity.
type userAssignedWireVal struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// propertiesResponse is the properties block. The computed fields
// (provisioningState, endpoints, managedResources) are stable.
type propertiesResponse struct {
	ProvisioningState        string                `json:"provisioningState"`
	PublicNetworkAccess      string                `json:"publicNetworkAccess"`
	ManagedEventHubState     string                `json:"managedEventHubState"`
	ManagedResourceGroupName string                `json:"managedResourceGroupName"`
	FriendlyName             string                `json:"friendlyName"`
	Endpoints                *endpointsWire        `json:"endpoints,omitempty"`
	ManagedResources         *managedResourcesWire `json:"managedResources,omitempty"`
}

// endpointsWire is the properties.endpoints block.
type endpointsWire struct {
	Catalog  string `json:"catalog"`
	Guardian string `json:"guardian"`
	Scan     string `json:"scan"`
}

// managedResourcesWire is the properties.managedResources block.
type managedResourcesWire struct {
	ResourceGroup     string `json:"resourceGroup"`
	StorageAccount    string `json:"storageAccount"`
	EventHubNamespace string `json:"eventHubNamespace"`
}

// listKeysResponse is the ListKeys action body — the Atlas Kafka connection
// strings.
type listKeysResponse struct {
	AtlasKafkaPrimaryEndpoint   string `json:"atlasKafkaPrimaryEndpoint"`
	AtlasKafkaSecondaryEndpoint string `json:"atlasKafkaSecondaryEndpoint"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []accountResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *purview.Account) accountResponse {
	out := accountResponse{
		ID:         s.ARMID(),
		Name:       s.Name,
		Type:       armType,
		Location:   s.Location,
		Tags:       s.Tags,
		Identity:   toIdentityResponse(s.Identity),
		Properties: toPropertiesResponse(s),
	}

	if s.Sku != nil {
		out.Sku = &skuWire{Name: s.Sku.Name, Capacity: s.Sku.Capacity}
	}

	return out
}

func toPropertiesResponse(s *purview.Account) propertiesResponse {
	out := propertiesResponse{
		ProvisioningState:        s.ProvisioningState,
		PublicNetworkAccess:      s.PublicNetworkAccess,
		ManagedEventHubState:     s.ManagedEventHubState,
		ManagedResourceGroupName: s.ManagedResourceGroupName,
		FriendlyName:             s.FriendlyName,
	}

	if s.Endpoints != nil {
		out.Endpoints = &endpointsWire{
			Catalog:  s.Endpoints.Catalog,
			Guardian: s.Endpoints.Guardian,
			Scan:     s.Endpoints.Scan,
		}
	}

	if s.ManagedResources != nil {
		out.ManagedResources = &managedResourcesWire{
			ResourceGroup:     s.ManagedResources.ResourceGroup,
			StorageAccount:    s.ManagedResources.StorageAccount,
			EventHubNamespace: s.ManagedResources.EventHubNamespace,
		}
	}

	return out
}

func toIdentityResponse(in *purview.Identity) *identityResponse {
	if in == nil {
		return nil
	}

	out := &identityResponse{Type: in.Type, PrincipalID: in.PrincipalID, TenantID: in.TenantID}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]userAssignedWireVal, len(in.UserAssigned))
		for id, v := range in.UserAssigned {
			out.UserAssigned[id] = userAssignedWireVal{PrincipalID: v.PrincipalID, ClientID: v.ClientID}
		}
	}

	return out
}

// toDriverIdentity maps a wire identity request onto the driver identity. Only
// the type and the user-assigned id keys are carried; the mock mints the ids.
func toDriverIdentity(in *identityRequest) *purview.Identity {
	if in == nil {
		return nil
	}

	out := &purview.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]purview.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = purview.UserAssignedValue{}
		}
	}

	return out
}

// inputFromRequest builds a create/update Input from a request body. Pointer
// fields are carried through verbatim so an absent field falls back to the
// stored (or default) value in the driver — which makes a PATCH body, where
// every field is optional, merge correctly on its own.
func inputFromRequest(req *accountRequest) purview.Input {
	in := purview.Input{
		Tags:     req.Tags,
		Identity: toDriverIdentity(req.Identity),
	}

	if req.Sku != nil {
		in.Sku = &purview.Sku{Name: req.Sku.Name, Capacity: req.Sku.Capacity}
	}

	if req.Properties != nil {
		in.PublicNetworkAccess = req.Properties.PublicNetworkAccess
		in.ManagedEventHubState = req.Properties.ManagedEventHubState
		in.ManagedResourceGroupName = req.Properties.ManagedResourceGroupName
	}

	return in
}
