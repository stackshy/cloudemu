package chaosstudio

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/chaosstudio"
)

// experimentRequest is the ARM PUT/PATCH body. location, tags and identity are
// top-level; the experiment configuration lives under properties. selectors and
// steps are carried as raw JSON so the nested branch/action blocks round-trip
// byte-for-byte.
type experimentRequest struct {
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

// propertiesRequest is the writable subset of properties. selectors and steps
// are raw so a PATCH that omits one preserves the stored value.
type propertiesRequest struct {
	Selectors json.RawMessage `json:"selectors,omitempty"`
	Steps     json.RawMessage `json:"steps,omitempty"`
}

// experimentResponse is the ARM representation of an experiment resource.
type experimentResponse struct {
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

// propertiesResponse is the properties block. provisioningState is the stable
// computed field; selectors and steps are echoed verbatim.
type propertiesResponse struct {
	ProvisioningState string          `json:"provisioningState"`
	Selectors         json.RawMessage `json:"selectors"`
	Steps             json.RawMessage `json:"steps"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []experimentResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *chaosstudio.Experiment) experimentResponse {
	return experimentResponse{
		ID:         s.ARMID(),
		Name:       s.Name,
		Type:       armType,
		Location:   s.Location,
		Tags:       s.Tags,
		Identity:   toIdentityResponse(s.Identity),
		Properties: toPropertiesResponse(s),
	}
}

func toPropertiesResponse(s *chaosstudio.Experiment) propertiesResponse {
	out := propertiesResponse{
		ProvisioningState: s.ProvisioningState,
		Selectors:         s.Selectors,
		Steps:             s.Steps,
	}

	// A resource restored from an older snapshot, or one created bare, may hold a
	// nil array; emit a valid empty JSON array so the wire body always parses.
	if out.Selectors == nil {
		out.Selectors = json.RawMessage("[]")
	}

	if out.Steps == nil {
		out.Steps = json.RawMessage("[]")
	}

	return out
}

func toIdentityResponse(in *chaosstudio.Identity) *identityResponse {
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
func toDriverIdentity(in *identityRequest) *chaosstudio.Identity {
	if in == nil {
		return nil
	}

	out := &chaosstudio.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]chaosstudio.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = chaosstudio.UserAssignedValue{}
		}
	}

	return out
}
