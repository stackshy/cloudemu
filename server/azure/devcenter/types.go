package devcenter

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/devcenter"
)

// devCenterRequest is the ARM PUT/PATCH body. location, tags and identity are
// top-level; the remaining configuration lives under properties.
type devCenterRequest struct {
	Location   string             `json:"location,omitempty"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Identity   *identityRequest   `json:"identity,omitempty"`
	Properties *propertiesRequest `json:"properties,omitempty"`
}

// identityRequest is the writable half of the identity block; the minted ids are
// read-only. userAssignedIdentities is a map of ARM ids to (empty) objects on
// input.
type identityRequest struct {
	Type         string                     `json:"type"`
	UserAssigned map[string]json.RawMessage `json:"userAssignedIdentities,omitempty"`
}

// propertiesRequest is the writable subset of properties. Pointer fields let a
// PATCH overlay only what it names.
type propertiesRequest struct {
	DisplayName            *string              `json:"displayName,omitempty"`
	ProjectCatalogSettings *catalogSettingsWire `json:"projectCatalogSettings,omitempty"`
}

// catalogSettingsWire is the projectCatalogSettings block. Only the
// catalog-item-sync toggle is modeled.
type catalogSettingsWire struct {
	CatalogItemSyncEnableStatus *string `json:"catalogItemSyncEnableStatus,omitempty"`
}

// devCenterResponse is the ARM representation of a dev center resource.
type devCenterResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Identity   *identityResponse  `json:"identity,omitempty"`
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
// (provisioningState, devCenterUri) are stable.
type propertiesResponse struct {
	ProvisioningState      string               `json:"provisioningState"`
	DevCenterURI           string               `json:"devCenterUri"`
	DisplayName            string               `json:"displayName,omitempty"`
	ProjectCatalogSettings *catalogSettingsResp `json:"projectCatalogSettings,omitempty"`
}

// catalogSettingsResp is the projectCatalogSettings block on the response.
type catalogSettingsResp struct {
	CatalogItemSyncEnableStatus string `json:"catalogItemSyncEnableStatus"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []devCenterResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *devcenter.DevCenter) devCenterResponse {
	return devCenterResponse{
		ID:         s.ARMID(),
		Name:       s.Name,
		Type:       armType,
		Location:   s.Location,
		Tags:       s.Tags,
		Identity:   toIdentityResponse(s.Identity),
		Properties: toPropertiesResponse(s),
	}
}

func toPropertiesResponse(s *devcenter.DevCenter) propertiesResponse {
	out := propertiesResponse{
		ProvisioningState: s.ProvisioningState,
		DevCenterURI:      s.DevCenterURI,
		DisplayName:       s.DisplayName,
	}

	if s.CatalogItemSyncEnableStatus != "" {
		out.ProjectCatalogSettings = &catalogSettingsResp{CatalogItemSyncEnableStatus: s.CatalogItemSyncEnableStatus}
	}

	return out
}

func toIdentityResponse(in *devcenter.Identity) *identityResponse {
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
func toDriverIdentity(in *identityRequest) *devcenter.Identity {
	if in == nil {
		return nil
	}

	out := &devcenter.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]devcenter.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = devcenter.UserAssignedValue{}
		}
	}

	return out
}
