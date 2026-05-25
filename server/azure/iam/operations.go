package iam

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
)

const maxBodyBytes = 1 << 20

// --- Role Definitions ---

// serveRoleDefinitions dispatches PUT/GET/DELETE by verb. id is "" for a
// collection (list) request.
func (h *Handler) serveRoleDefinitions(w http.ResponseWriter, r *http.Request, scope, id string) {
	switch r.Method {
	case http.MethodPut:
		if id == "" {
			writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
				"PUT requires a role definition id")
			return
		}

		h.createOrUpdateRoleDefinition(w, r, scope, id)
	case http.MethodGet:
		if id == "" {
			h.listRoleDefinitions(w, r, scope)
			return
		}

		h.getRoleDefinition(w, r, id)
	case http.MethodDelete:
		if id == "" {
			writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
				"DELETE requires a role definition id")
			return
		}

		h.deleteRoleDefinition(w, r, id)
	default:
		writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"unsupported verb on roleDefinitions: "+r.Method)
	}
}

func (h *Handler) createOrUpdateRoleDefinition(
	w http.ResponseWriter, r *http.Request, scope, id string,
) {
	var in createOrUpdateRoleDefinitionInput
	if !decodeJSONBody(w, r, &in) {
		return
	}

	props := in.Properties
	if props.Type == "" {
		props.Type = "CustomRole"
	}

	now := time.Now().UTC().Format(time.RFC3339)
	props.CreatedOn = now
	props.UpdatedOn = now

	if len(props.AssignableScopes) == 0 {
		props.AssignableScopes = []string{scope}
	}

	propsJSON, err := json.Marshal(props)
	if err != nil {
		writeARMError(w, http.StatusInternalServerError, "InternalError",
			"could not encode role definition properties: "+err.Error())
		return
	}

	// Upsert: try create first, fall back to delete+create on AlreadyExists
	// so subsequent PUTs to the same id behave as updates per ARM semantics.
	if _, err := h.iam.CreateRole(r.Context(), iamdriver.RoleConfig{
		Name:                id,
		AssumeRolePolicyDoc: string(propsJSON),
		Path:                scope,
	}); err != nil {
		if !cerrors.IsAlreadyExists(err) {
			writeCErr(w, err)
			return
		}

		if delErr := h.iam.DeleteRole(r.Context(), id); delErr != nil {
			writeCErr(w, delErr)
			return
		}

		if _, err := h.iam.CreateRole(r.Context(), iamdriver.RoleConfig{
			Name:                id,
			AssumeRolePolicyDoc: string(propsJSON),
			Path:                scope,
		}); err != nil {
			writeCErr(w, err)
			return
		}
	}

	writeARMJSON(w, http.StatusCreated,
		buildRoleDefinitionEnvelope(scope, id, &props))
}

func (h *Handler) getRoleDefinition(w http.ResponseWriter, r *http.Request, id string) {
	role, err := h.iam.GetRole(r.Context(), id)
	if err != nil {
		writeCErr(w, err)
		return
	}

	scope := role.Path

	props, perr := decodeRoleProperties(role.AssumeRolePolicyDoc)
	if perr != nil {
		writeARMError(w, http.StatusInternalServerError, "InternalError",
			"could not decode stored role definition: "+perr.Error())
		return
	}

	writeARMJSON(w, http.StatusOK, buildRoleDefinitionEnvelope(scope, id, &props))
}

func (h *Handler) listRoleDefinitions(w http.ResponseWriter, r *http.Request, scope string) {
	roles, err := h.iam.ListRoles(r.Context())
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := roleDefinitionList{Value: make([]roleDefinitionEnvelope, 0, len(roles))}

	for i := range roles {
		role := &roles[i]
		if !scopeMatches(scope, role.Path) {
			continue
		}

		props, perr := decodeRoleProperties(role.AssumeRolePolicyDoc)
		if perr != nil {
			continue
		}

		out.Value = append(out.Value,
			buildRoleDefinitionEnvelope(role.Path, role.Name, &props))
	}

	writeARMJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteRoleDefinition(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.iam.DeleteRole(r.Context(), id); err != nil {
		writeCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// --- Role Assignments ---

func (h *Handler) serveRoleAssignments(w http.ResponseWriter, r *http.Request, scope, id string) {
	switch r.Method {
	case http.MethodPut:
		if id == "" {
			writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
				"PUT requires a role assignment id")
			return
		}

		h.createRoleAssignment(w, r, scope, id)
	case http.MethodGet:
		if id == "" {
			h.listRoleAssignments(w, scope)
			return
		}

		h.getRoleAssignment(w, scope, id)
	case http.MethodDelete:
		if id == "" {
			writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
				"DELETE requires a role assignment id")
			return
		}

		h.deleteRoleAssignment(w, scope, id)
	default:
		writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"unsupported verb on roleAssignments: "+r.Method)
	}
}

func (h *Handler) createRoleAssignment(
	w http.ResponseWriter, r *http.Request, scope, id string,
) {
	var in createRoleAssignmentInput
	if !decodeJSONBody(w, r, &in) {
		return
	}

	props := in.Properties

	if props.RoleDefinitionID == "" {
		writeARMError(w, http.StatusBadRequest, "MissingProperty",
			"properties.roleDefinitionId is required")
		return
	}

	if props.PrincipalID == "" {
		writeARMError(w, http.StatusBadRequest, "MissingProperty",
			"properties.principalId is required")
		return
	}

	if props.Scope == "" {
		props.Scope = scope
	}

	now := time.Now().UTC().Format(time.RFC3339)
	props.CreatedOn = now
	props.UpdatedOn = now

	env := roleAssignmentEnvelope{
		ID:         scope + providerSegmentCanonical + roleAssignmentsCanonical + "/" + id,
		Name:       id,
		Type:       typeRoleAssignment,
		Properties: props,
	}

	stored := h.assignments.put(&env)
	writeARMJSON(w, http.StatusCreated, stored)
}

func (h *Handler) getRoleAssignment(w http.ResponseWriter, scope, id string) {
	env, ok := h.assignments.get(id)
	if !ok {
		writeARMError(w, http.StatusNotFound, "RoleAssignmentNotFound",
			"role assignment "+id+" not found")
		return
	}
	// Rewrite the ID with the requested scope so the SDK round-trips the
	// caller's path back unchanged.
	env.ID = scope + providerSegmentCanonical + roleAssignmentsCanonical + "/" + id

	writeARMJSON(w, http.StatusOK, env)
}

func (h *Handler) listRoleAssignments(w http.ResponseWriter, scope string) {
	items := h.assignments.listAtScope(scope)
	writeARMJSON(w, http.StatusOK, roleAssignmentList{Value: items})
}

func (h *Handler) deleteRoleAssignment(w http.ResponseWriter, scope, id string) {
	env, ok := h.assignments.delete(id)
	if !ok {
		writeARMError(w, http.StatusNotFound, "RoleAssignmentNotFound",
			"role assignment "+id+" not found")
		return
	}

	env.ID = scope + providerSegmentCanonical + roleAssignmentsCanonical + "/" + id

	writeARMJSON(w, http.StatusOK, env)
}

// --- helpers ---

// buildRoleDefinitionEnvelope returns the ARM JSON envelope for a single
// role definition. props is passed by pointer because the struct is wider
// than the gocritic hugeParam threshold; the function dereferences once.
func buildRoleDefinitionEnvelope(
	scope, id string, props *roleDefinitionProperties,
) roleDefinitionEnvelope {
	return roleDefinitionEnvelope{
		ID:         scope + providerSegmentCanonical + roleDefinitionsCanonical + "/" + id,
		Name:       id,
		Type:       typeRoleDefinition,
		Properties: *props,
	}
}

// decodeRoleProperties extracts the properties JSON we stashed in
// AssumeRolePolicyDoc during create. Empty doc returns a zero-value
// properties struct so listing pre-existing driver roles (created via the
// portable API rather than this handler) doesn't surface as an error.
func decodeRoleProperties(doc string) (roleDefinitionProperties, error) {
	if doc == "" {
		return roleDefinitionProperties{}, nil
	}

	var props roleDefinitionProperties
	if err := json.Unmarshal([]byte(doc), &props); err != nil {
		return roleDefinitionProperties{}, err
	}

	return props, nil
}

// scopeMatches returns true when a stored role's scope is acceptable for a
// query scope. Empty query returns everything (azure SDK calls this with no
// scope for "list all in subscription"). Exact-match or ancestor prefix is
// allowed; we err on the permissive side rather than mirror Azure's exact
// inheritance semantics, which the SDK doesn't enforce client-side.
func scopeMatches(query, stored string) bool {
	if query == "" || query == "/" {
		return true
	}

	if stored == "" || query == stored {
		return true
	}

	if strings.HasPrefix(query, stored+"/") {
		return true
	}

	return strings.HasPrefix(stored, query+"/")
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer func() { _ = r.Body.Close() }()

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeARMError(w, http.StatusBadRequest, "InvalidBody",
			"could not read request body: "+err.Error())
		return false
	}

	if len(raw) == 0 {
		writeARMError(w, http.StatusBadRequest, "InvalidBody", "empty request body")
		return false
	}

	if err := json.Unmarshal(raw, v); err != nil {
		writeARMError(w, http.StatusBadRequest, "InvalidBody",
			"could not parse JSON body: "+err.Error())
		return false
	}

	return true
}

// writeCErr maps canonical cloudemu errors to ARM JSON error responses.
func writeCErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeARMError(w, http.StatusNotFound, "ResourceNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeARMError(w, http.StatusConflict, "ResourceAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeARMError(w, http.StatusBadRequest, "InvalidArgument", err.Error())
	default:
		writeARMError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
