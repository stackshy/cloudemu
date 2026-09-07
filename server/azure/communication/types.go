package communication

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/communication"
)

// communicationRequest is the ARM PUT/PATCH body. location, tags and identity
// are top-level; the remaining configuration lives under properties. location is
// ignored (the resource is always global) and is not carried into the driver.
type communicationRequest struct {
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

// propertiesRequest is the writable subset of properties. dataLocation is
// consumed only at create (it is immutable).
type propertiesRequest struct {
	DataLocation      string   `json:"dataLocation,omitempty"`
	NotificationHubID string   `json:"notificationHubId,omitempty"`
	LinkedDomains     []string `json:"linkedDomains,omitempty"`
}

// communicationResponse is the ARM representation of a communicationServices
// resource.
type communicationResponse struct {
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

// propertiesResponse is the properties block. The computed fields (hostName,
// immutableResourceId, version, provisioningState) are stable.
type propertiesResponse struct {
	ProvisioningState   string   `json:"provisioningState"`
	HostName            string   `json:"hostName"`
	DataLocation        string   `json:"dataLocation"`
	ImmutableResourceID string   `json:"immutableResourceId"`
	NotificationHubID   string   `json:"notificationHubId,omitempty"`
	LinkedDomains       []string `json:"linkedDomains,omitempty"`
	Version             string   `json:"version"`
}

// keysResponse is the ARM listKeys action body.
type keysResponse struct {
	PrimaryKey              string `json:"primaryKey"`
	SecondaryKey            string `json:"secondaryKey"`
	PrimaryConnectionString string `json:"primaryConnectionString"`
	//nolint:tagliatelle // ARM wire name is "secondaryConnectionString"
	SecondaryConnectionString string `json:"secondaryConnectionString"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []communicationResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *communication.Communication) communicationResponse {
	return communicationResponse{
		ID:         s.ARMID(),
		Name:       s.Name,
		Type:       armType,
		Location:   s.Location,
		Tags:       s.Tags,
		Identity:   toIdentityResponse(s.Identity),
		Properties: toPropertiesResponse(s),
	}
}

func toPropertiesResponse(s *communication.Communication) propertiesResponse {
	return propertiesResponse{
		ProvisioningState:   s.ProvisioningState,
		HostName:            s.HostName,
		DataLocation:        s.DataLocation,
		ImmutableResourceID: s.ImmutableResourceID,
		NotificationHubID:   s.NotificationHubID,
		LinkedDomains:       s.LinkedDomains,
		Version:             s.Version,
	}
}

func toKeysResponse(s *communication.Communication) keysResponse {
	return keysResponse{
		PrimaryKey:                s.PrimaryKey,
		SecondaryKey:              s.SecondaryKey,
		PrimaryConnectionString:   s.PrimaryConnectionString(),
		SecondaryConnectionString: s.SecondaryConnectionString(),
	}
}

func toIdentityResponse(in *communication.Identity) *identityResponse {
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
func toDriverIdentity(in *identityRequest) *communication.Identity {
	if in == nil {
		return nil
	}

	out := &communication.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]communication.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = communication.UserAssignedValue{}
		}
	}

	return out
}
