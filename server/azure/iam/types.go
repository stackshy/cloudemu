package iam

import (
	"encoding/json"
	"net/http"
)

// armErrorEnvelope is the ARM error JSON shape every SDK expects on a non-2xx
// response: {"error": {"code": "...", "message": "..."}}.
type armErrorEnvelope struct {
	Error armErrorBody `json:"error"`
}

type armErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeARMError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(armErrorEnvelope{
		Error: armErrorBody{Code: code, Message: msg},
	})
}

func writeARMJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Resource type identifiers used in the ARM "type" field of envelopes.
const (
	typeRoleDefinition = "Microsoft.Authorization/roleDefinitions"
	typeRoleAssignment = "Microsoft.Authorization/roleAssignments"
)

// permission is the {actions, notActions, dataActions, notDataActions} bag
// inside a RoleDefinition.properties.permissions[] entry.
type permission struct {
	Actions        []string `json:"actions,omitempty"`
	NotActions     []string `json:"notActions,omitempty"`
	DataActions    []string `json:"dataActions,omitempty"`
	NotDataActions []string `json:"notDataActions,omitempty"`
}

// roleDefinitionProperties is the ARM properties block for a RoleDefinition.
type roleDefinitionProperties struct {
	RoleName         string       `json:"roleName"`
	Description      string       `json:"description,omitempty"`
	Type             string       `json:"type,omitempty"` // "CustomRole" or "BuiltInRole"
	Permissions      []permission `json:"permissions,omitempty"`
	AssignableScopes []string     `json:"assignableScopes,omitempty"`
	CreatedOn        string       `json:"createdOn,omitempty"`
	UpdatedOn        string       `json:"updatedOn,omitempty"`
}

// roleDefinitionEnvelope is the full ARM envelope returned on GET/PUT.
type roleDefinitionEnvelope struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"` // the {id} segment of the URL
	Type       string                   `json:"type"` // typeRoleDefinition
	Properties roleDefinitionProperties `json:"properties"`
}

// roleDefinitionList is the ARM paged-list shape returned on a collection GET.
// NextLink is omitted (single page only).
type roleDefinitionList struct {
	Value []roleDefinitionEnvelope `json:"value"`
}

// roleAssignmentProperties is the ARM properties block for a RoleAssignment.
type roleAssignmentProperties struct {
	RoleDefinitionID string `json:"roleDefinitionId"`
	PrincipalID      string `json:"principalId"`
	PrincipalType    string `json:"principalType,omitempty"`
	Scope            string `json:"scope,omitempty"`
	Description      string `json:"description,omitempty"`
	CreatedOn        string `json:"createdOn,omitempty"`
	UpdatedOn        string `json:"updatedOn,omitempty"`
}

type roleAssignmentEnvelope struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"` // the {id} segment of the URL
	Type       string                   `json:"type"` // typeRoleAssignment
	Properties roleAssignmentProperties `json:"properties"`
}

type roleAssignmentList struct {
	Value []roleAssignmentEnvelope `json:"value"`
}

// createOrUpdateRoleDefinitionInput is the request body for PUT roleDefinitions.
type createOrUpdateRoleDefinitionInput struct {
	Properties roleDefinitionProperties `json:"properties"`
}

// createRoleAssignmentInput is the request body for PUT roleAssignments.
type createRoleAssignmentInput struct {
	Properties roleAssignmentProperties `json:"properties"`
}
