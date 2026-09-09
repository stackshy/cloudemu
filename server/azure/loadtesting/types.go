package loadtesting

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/loadtesting"
)

// loadTestRequest is the ARM PUT/PATCH body. location, tags and identity are
// top-level; description and encryption live under properties.
type loadTestRequest struct {
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

// propertiesRequest is the writable subset of properties. Description is a
// pointer so a PATCH can distinguish "clear it" from "leave it".
type propertiesRequest struct {
	Description *string         `json:"description,omitempty"`
	Encryption  *encryptionWire `json:"encryption,omitempty"`
}

// encryptionWire is the customer-managed-key configuration on the wire.
type encryptionWire struct {
	KeyURL   string           `json:"keyUrl,omitempty"`
	Identity *encIdentityWire `json:"identity,omitempty"`
}

// encIdentityWire selects the identity used for the CMK.
type encIdentityWire struct {
	Type       string `json:"type,omitempty"`
	ResourceID string `json:"resourceId,omitempty"`
}

// loadTestResponse is the ARM representation of a load-test resource.
type loadTestResponse struct {
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

// propertiesResponse is the properties block. dataPlaneURI and provisioningState
// are computed and stable.
type propertiesResponse struct {
	Description       string          `json:"description,omitempty"`
	DataPlaneURI      string          `json:"dataPlaneURI"`
	ProvisioningState string          `json:"provisioningState"`
	Encryption        *encryptionWire `json:"encryption,omitempty"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []loadTestResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(lt *loadtesting.LoadTest) loadTestResponse {
	return loadTestResponse{
		ID:       lt.ARMID(),
		Name:     lt.Name,
		Type:     armType,
		Location: lt.Location,
		Tags:     lt.Tags,
		Identity: toIdentityResponse(lt.Identity),
		Properties: propertiesResponse{
			Description:       lt.Description,
			DataPlaneURI:      lt.DataPlaneURI,
			ProvisioningState: lt.ProvisioningState,
			Encryption:        toEncryptionWire(lt.Encryption),
		},
	}
}

func toIdentityResponse(in *loadtesting.Identity) *identityResponse {
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

func toEncryptionWire(in *loadtesting.Encryption) *encryptionWire {
	if in == nil {
		return nil
	}

	out := &encryptionWire{KeyURL: in.KeyURL}
	if in.Identity != nil {
		out.Identity = &encIdentityWire{Type: in.Identity.Type, ResourceID: in.Identity.ResourceID}
	}

	return out
}

// toDriverIdentity maps a wire identity request onto the driver identity. Only
// the type and the user-assigned id keys are carried; the mock mints the ids.
func toDriverIdentity(in *identityRequest) *loadtesting.Identity {
	if in == nil {
		return nil
	}

	out := &loadtesting.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]loadtesting.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = loadtesting.UserAssignedValue{}
		}
	}

	return out
}

// toDriverEncryption maps a wire encryption request onto the driver type.
func toDriverEncryption(in *encryptionWire) *loadtesting.Encryption {
	if in == nil {
		return nil
	}

	out := &loadtesting.Encryption{KeyURL: in.KeyURL}
	if in.Identity != nil {
		out.Identity = &loadtesting.EncryptionIdentity{Type: in.Identity.Type, ResourceID: in.Identity.ResourceID}
	}

	return out
}
