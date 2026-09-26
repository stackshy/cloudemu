package logic

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
)

// workflowRequest is the ARM PUT/PATCH body. location, tags and identity are
// top-level; state, definition, parameters, accessControl and
// integrationAccount live under properties.
type workflowRequest struct {
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

// propertiesRequest is the writable subset of properties. The opaque blocks are
// raw JSON so they round-trip byte-for-byte; a nil value means "not supplied",
// which lets a PATCH distinguish "leave it" from "replace it".
type propertiesRequest struct {
	State              string          `json:"state,omitempty"`
	Definition         json.RawMessage `json:"definition,omitempty"`
	Parameters         json.RawMessage `json:"parameters,omitempty"`
	AccessControl      json.RawMessage `json:"accessControl,omitempty"`
	IntegrationAccount json.RawMessage `json:"integrationAccount,omitempty"`
}

// workflowResponse is the ARM representation of a workflow.
type workflowResponse struct {
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

// propertiesResponse is the properties block. provisioningState, createdTime,
// changedTime, version and accessEndpoint are computed by the service.
type propertiesResponse struct {
	ProvisioningState  string          `json:"provisioningState"`
	CreatedTime        string          `json:"createdTime"`
	ChangedTime        string          `json:"changedTime"`
	State              string          `json:"state"`
	Version            string          `json:"version"`
	AccessEndpoint     string          `json:"accessEndpoint"`
	Definition         json.RawMessage `json:"definition,omitempty"`
	Parameters         json.RawMessage `json:"parameters,omitempty"`
	AccessControl      json.RawMessage `json:"accessControl,omitempty"`
	IntegrationAccount json.RawMessage `json:"integrationAccount,omitempty"`
}

// listResponse is the ARM list envelope. nextLink is omitted: the emulator
// returns a single page.
type listResponse struct {
	Value []workflowResponse `json:"value"`
}

// toResponse projects a stored workflow onto its ARM wire representation.
func toResponse(wf *logic.Workflow) workflowResponse {
	return workflowResponse{
		ID:       wf.ARMID(),
		Name:     wf.Name,
		Type:     armType,
		Location: wf.Location,
		Tags:     wf.Tags,
		Identity: toIdentityResponse(wf.Identity),
		Properties: propertiesResponse{
			ProvisioningState:  wf.ProvisioningState,
			CreatedTime:        wf.CreatedTime.UTC().Format(time.RFC3339Nano),
			ChangedTime:        wf.ChangedTime.UTC().Format(time.RFC3339Nano),
			State:              wf.State,
			Version:            wf.Version(),
			AccessEndpoint:     wf.AccessEndpoint,
			Definition:         wf.Definition,
			Parameters:         wf.Parameters,
			AccessControl:      wf.AccessControl,
			IntegrationAccount: wf.IntegrationAccount,
		},
	}
}

func toIdentityResponse(in *logic.Identity) *identityResponse {
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
func toDriverIdentity(in *identityRequest) *logic.Identity {
	if in == nil {
		return nil
	}

	out := &logic.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]logic.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = logic.UserAssignedValue{}
		}
	}

	return out
}

// identityInputFrom rebuilds a create/update identity input from a stored
// identity, so a PATCH that omits identity preserves it. Only the type and the
// user-assigned id keys are carried; the mock re-mints the ids deterministically.
func identityInputFrom(in *logic.Identity) *logic.Identity {
	if in == nil {
		return nil
	}

	out := &logic.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]logic.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = logic.UserAssignedValue{}
		}
	}

	return out
}
