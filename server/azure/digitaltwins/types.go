package digitaltwins

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/digitaltwins"
)

// instanceRequest is the ARM PUT/PATCH body. location, tags and identity are
// top-level; publicNetworkAccess lives under properties.
type instanceRequest struct {
	Location   string             `json:"location"`
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

// propertiesRequest is the writable subset of properties.
type propertiesRequest struct {
	PublicNetworkAccess string `json:"publicNetworkAccess,omitempty"`
}

// instanceResponse is the ARM representation of a Digital Twins instance.
type instanceResponse struct {
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

// propertiesResponse is the properties block. hostName and provisioningState are
// computed and stable; createdTime is stamped once at create.
type propertiesResponse struct {
	HostName            string `json:"hostName"`
	ProvisioningState   string `json:"provisioningState"`
	PublicNetworkAccess string `json:"publicNetworkAccess,omitempty"`
	CreatedTime         string `json:"createdTime,omitempty"`
	LastUpdatedTime     string `json:"lastUpdatedTime,omitempty"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []instanceResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(inst *digitaltwins.Instance) instanceResponse {
	return instanceResponse{
		ID:       inst.ARMID(),
		Name:     inst.Name,
		Type:     armType,
		Location: inst.Location,
		Tags:     inst.Tags,
		Identity: toIdentityResponse(inst.Identity),
		Properties: propertiesResponse{
			HostName:            inst.HostName,
			ProvisioningState:   inst.ProvisioningState,
			PublicNetworkAccess: inst.PublicNetworkAccess,
			CreatedTime:         inst.CreatedTime,
			LastUpdatedTime:     inst.LastUpdatedTime,
		},
	}
}

func toIdentityResponse(in *digitaltwins.Identity) *identityResponse {
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
func toDriverIdentity(in *identityRequest) *digitaltwins.Identity {
	if in == nil {
		return nil
	}

	out := &digitaltwins.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]digitaltwins.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = digitaltwins.UserAssignedValue{}
		}
	}

	return out
}
