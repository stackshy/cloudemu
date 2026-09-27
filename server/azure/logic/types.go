package logic

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
)

// workflowRequest is the ARM PUT/PATCH body. location, tags and identity are
// top-level; state, definition, parameters, accessControl, integrationAccount,
// integrationServiceEnvironment and sku live under properties.
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
	State                         string          `json:"state,omitempty"`
	Definition                    json.RawMessage `json:"definition,omitempty"`
	Parameters                    json.RawMessage `json:"parameters,omitempty"`
	AccessControl                 json.RawMessage `json:"accessControl,omitempty"`
	IntegrationAccount            json.RawMessage `json:"integrationAccount,omitempty"`
	IntegrationServiceEnvironment json.RawMessage `json:"integrationServiceEnvironment,omitempty"`
	Sku                           json.RawMessage `json:"sku,omitempty"`
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
// changedTime, version, accessEndpoint and endpointsConfiguration are computed
// by the service.
type propertiesResponse struct {
	ProvisioningState             string                         `json:"provisioningState"`
	CreatedTime                   string                         `json:"createdTime"`
	ChangedTime                   string                         `json:"changedTime"`
	State                         string                         `json:"state"`
	Version                       string                         `json:"version"`
	AccessEndpoint                string                         `json:"accessEndpoint"`
	EndpointsConfiguration        endpointsConfigurationResponse `json:"endpointsConfiguration"`
	Definition                    json.RawMessage                `json:"definition,omitempty"`
	Parameters                    json.RawMessage                `json:"parameters,omitempty"`
	AccessControl                 json.RawMessage                `json:"accessControl,omitempty"`
	IntegrationAccount            json.RawMessage                `json:"integrationAccount,omitempty"`
	IntegrationServiceEnvironment json.RawMessage                `json:"integrationServiceEnvironment,omitempty"`
	Sku                           json.RawMessage                `json:"sku,omitempty"`
}

// endpointsConfigurationResponse is properties.endpointsConfiguration.
type endpointsConfigurationResponse struct {
	Workflow  flowEndpointsResponse `json:"workflow"`
	Connector flowEndpointsResponse `json:"connector"`
}

// flowEndpointsResponse is one half of endpointsConfiguration.
type flowEndpointsResponse struct {
	OutgoingIPAddresses       []ipAddress `json:"outgoingIpAddresses"`
	AccessEndpointIPAddresses []ipAddress `json:"accessEndpointIpAddresses"`
}

// ipAddress is the armlogic IPAddress wrapper.
type ipAddress struct {
	Address string `json:"address"`
}

// listResponse is the ARM list envelope. nextLink is set only when a $top-bounded
// page leaves workflows behind.
type listResponse struct {
	Value    []workflowResponse `json:"value"`
	NextLink string             `json:"nextLink,omitempty"`
}

// callbackURLResponse is the armlogic WorkflowTriggerCallbackURL body.
type callbackURLResponse struct {
	Value        string                `json:"value"`
	Method       string                `json:"method"`
	BasePath     string                `json:"basePath"`
	RelativePath string                `json:"relativePath,omitempty"`
	Queries      callbackQueryResponse `json:"queries"`
}

// callbackQueryResponse is the armlogic WorkflowTriggerListCallbackURLQueries body.
type callbackQueryResponse struct {
	APIVersion string `json:"api-version"`
	Sp         string `json:"sp"`
	Sv         string `json:"sv"`
	Sig        string `json:"sig"`
}

// toResponse projects a stored workflow onto its ARM wire representation.
func toResponse(wf *logic.Workflow) workflowResponse {
	endpoints := wf.Endpoints()

	return workflowResponse{
		ID:       wf.ARMID(),
		Name:     wf.Name,
		Type:     armType,
		Location: wf.Location,
		Tags:     wf.Tags,
		Identity: toIdentityResponse(wf.Identity),
		Properties: propertiesResponse{
			ProvisioningState:             wf.ProvisioningState,
			CreatedTime:                   wf.CreatedTime.UTC().Format(time.RFC3339Nano),
			ChangedTime:                   wf.ChangedTime.UTC().Format(time.RFC3339Nano),
			State:                         wf.State,
			Version:                       wf.Version(),
			AccessEndpoint:                wf.AccessEndpoint,
			EndpointsConfiguration:        toEndpointsResponse(&endpoints),
			Definition:                    wf.Definition,
			Parameters:                    wf.Parameters,
			AccessControl:                 wf.AccessControl,
			IntegrationAccount:            wf.IntegrationAccount,
			IntegrationServiceEnvironment: wf.IntegrationServiceEnvironment,
			Sku:                           wf.Sku,
		},
	}
}

func toEndpointsResponse(ec *logic.EndpointsConfiguration) endpointsConfigurationResponse {
	return endpointsConfigurationResponse{
		Workflow:  toFlowEndpoints(&ec.Workflow),
		Connector: toFlowEndpoints(&ec.Connector),
	}
}

func toFlowEndpoints(fe *logic.FlowEndpoints) flowEndpointsResponse {
	return flowEndpointsResponse{
		OutgoingIPAddresses:       toIPAddresses(fe.OutgoingIPAddresses),
		AccessEndpointIPAddresses: toIPAddresses(fe.AccessEndpointIPAddresses),
	}
}

func toIPAddresses(addrs []string) []ipAddress {
	out := make([]ipAddress, len(addrs))
	for i, a := range addrs {
		out[i] = ipAddress{Address: a}
	}

	return out
}

func toCallbackResponse(cb *logic.CallbackURL) callbackURLResponse {
	return callbackURLResponse{
		Value:        cb.Value,
		Method:       cb.Method,
		BasePath:     cb.BasePath,
		RelativePath: cb.RelativePath,
		Queries: callbackQueryResponse{
			APIVersion: cb.Queries.APIVersion,
			Sp:         cb.Queries.Sp,
			Sv:         cb.Queries.Sv,
			Sig:        cb.Queries.Sig,
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

// toPatch maps a decoded PATCH body onto the store's merge patch. A nil body is
// an empty patch.
func toPatch(req *workflowRequest) logic.Patch {
	if req == nil {
		return logic.Patch{}
	}

	patch := logic.Patch{Tags: req.Tags, Identity: toDriverIdentity(req.Identity)}

	if p := req.Properties; p != nil {
		patch.State = p.State
		patch.Definition = p.Definition
		patch.Parameters = p.Parameters
		patch.AccessControl = p.AccessControl
		patch.IntegrationAccount = p.IntegrationAccount
		patch.IntegrationServiceEnvironment = p.IntegrationServiceEnvironment
		patch.Sku = p.Sku
	}

	return patch
}
