package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	azureiam "github.com/stackshy/cloudemu/v2/providers/azure/iam"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
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

		h.getRoleDefinition(w, r, scope, id)
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

	// Built-in role GUIDs are reserved and immutable: a PUT that reuses one as
	// the {id} must not silently create a colliding custom role. Real Azure
	// rejects the write with 409 RoleDefinitionUpdateConflict, mirroring the
	// guard roleDefinitionExists relies on for assignments.
	if _, ok := h.builtins[id]; ok {
		writeARMError(w, http.StatusConflict, "RoleDefinitionUpdateConflict",
			"the role definition "+id+" is a built-in role and cannot be modified")
		return
	}

	props := in.Properties
	if props.Type == "" {
		props.Type = "CustomRole"
	}

	now := time.Now().UTC().Format(time.RFC3339)
	props.UpdatedOn = now

	// Preserve the original createdOn across updates: a PUT to an existing role
	// definition is an update, and real Azure keeps the first-create timestamp
	// in properties.createdOn while advancing updatedOn. Fresh creates fall back
	// to "now".
	props.CreatedOn = h.priorCreatedOn(r.Context(), id, now)

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
	//
	// Caveat: the delete+create dance is not atomic — a concurrent reader
	// between the two driver calls observes NotFound. The driver lacks an
	// Update entry point, so this is the simplest workaround. Real ARM does
	// an atomic upsert.
	//
	// Status code: armauthorization roleDefinitions.CreateOrUpdate models
	// only 201 Created as success (per the Azure REST API spec for this
	// specific endpoint — unlike e.g. managedClusters which returns 200 on
	// update). So we always return 201 regardless of create-vs-update.
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

// priorCreatedOn returns the createdOn timestamp of an already-stored role
// definition with the given id, or fallback when there is no prior definition
// (or it carried no timestamp). This keeps the first-create timestamp stable
// across subsequent update PUTs.
func (h *Handler) priorCreatedOn(ctx context.Context, id, fallback string) string {
	existing, err := h.iam.GetRole(ctx, id)
	if err != nil {
		return fallback
	}

	prior, perr := decodeRoleProperties(existing.AssumeRolePolicyDoc)
	if perr != nil || prior.CreatedOn == "" {
		return fallback
	}

	return prior.CreatedOn
}

func (h *Handler) getRoleDefinition(w http.ResponseWriter, r *http.Request, scope, id string) {
	// Built-in roles resolve by their fixed GUID at any scope, and are echoed
	// back rooted at the caller's requested scope (real Azure returns the id at
	// the scope you queried).
	if props, ok := h.builtins[id]; ok {
		writeARMJSON(w, http.StatusOK, buildRoleDefinitionEnvelope(scope, id, &props))
		return
	}

	role, err := h.iam.GetRole(r.Context(), id)
	if err != nil {
		writeCErr(w, err)
		return
	}

	scope = role.Path

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

	out := roleDefinitionList{Value: make([]roleDefinitionEnvelope, 0, len(roles)+len(h.builtins))}

	// Built-in roles are assignable at every scope, so they appear in a list at
	// any scope — rooted at the caller's requested scope, matching real Azure.
	for id := range h.builtins {
		props := h.builtins[id]
		out.Value = append(out.Value,
			buildRoleDefinitionEnvelope(scope, id, &props))
	}

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

// deleteRoleDefinition deletes the role and echoes back the prior resource,
// matching real Azure semantics (the SDK's RoleDefinitionsClientDeleteResponse
// carries a RoleDefinition body).
func (h *Handler) deleteRoleDefinition(w http.ResponseWriter, r *http.Request, id string) {
	// Built-in roles are platform-managed and cannot be deleted: real Azure
	// rejects the DELETE with a built-in-protection conflict rather than the
	// 404 the driver would surface for an unknown custom-role GUID.
	if _, ok := h.builtins[id]; ok {
		writeARMError(w, http.StatusConflict, "RoleDefinitionUpdateConflict",
			"the role definition "+id+" is a built-in role and cannot be deleted")
		return
	}

	role, err := h.iam.GetRole(r.Context(), id)
	if err != nil {
		writeCErr(w, err)
		return
	}

	// Referential integrity: real Azure rejects deleting a role definition
	// that active role assignments still reference (RoleDefinitionHasAssignments),
	// rather than leaving those assignments dangling.
	inUse, err := h.iam.RoleAssignmentsForRoleDefinition(r.Context(), id)
	if err != nil {
		writeCErr(w, err)
		return
	}

	if len(inUse) > 0 {
		msg := fmt.Sprintf("the role definition %s cannot be deleted because %d role assignment(s) still reference it",
			id, len(inUse))
		writeARMError(w, http.StatusConflict, "RoleDefinitionHasAssignments", msg)

		return
	}

	priorScope := role.Path

	priorProps, perr := decodeRoleProperties(role.AssumeRolePolicyDoc)
	if perr != nil {
		writeARMError(w, http.StatusInternalServerError, "InternalError",
			"could not decode stored role definition: "+perr.Error())
		return
	}

	if err := h.iam.DeleteRole(r.Context(), id); err != nil {
		writeCErr(w, err)
		return
	}

	writeARMJSON(w, http.StatusOK,
		buildRoleDefinitionEnvelope(priorScope, id, &priorProps))
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
			h.listRoleAssignments(w, r, scope)
			return
		}

		h.getRoleAssignment(w, r, scope, id)
	case http.MethodDelete:
		if id == "" {
			writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
				"DELETE requires a role assignment id")
			return
		}

		h.deleteRoleAssignment(w, r, scope, id)
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

	// Referential integrity: the roleDefinitionId must resolve to an existing
	// role definition (a seeded built-in or a custom one). Real Azure rejects a
	// dangling reference with 400 RoleDefinitionDoesNotExist.
	if !h.roleDefinitionExists(r.Context(), props.RoleDefinitionID) {
		writeARMError(w, http.StatusBadRequest, "RoleDefinitionDoesNotExist",
			"the role definition referenced by roleDefinitionId does not exist: "+props.RoleDefinitionID)
		return
	}

	// Re-creating the same assignment GUID, or creating a different GUID for an
	// already-assigned (principal, role, scope) triple, both conflict in real
	// Azure with 409 RoleAssignmentExists. CreateRoleAssignment enforces both
	// invariants atomically under the store's lock.
	info, err := h.iam.CreateRoleAssignment(r.Context(), azureiam.RoleAssignmentConfig{
		ID:               id,
		RoleDefinitionID: props.RoleDefinitionID,
		PrincipalID:      props.PrincipalID,
		PrincipalType:    props.PrincipalType,
		Scope:            props.Scope,
		Description:      props.Description,
	})
	if err != nil {
		if cerrors.IsAlreadyExists(err) {
			writeARMError(w, http.StatusConflict, "RoleAssignmentExists", err.Error())
			return
		}

		writeCErr(w, err)

		return
	}

	writeARMJSON(w, http.StatusCreated, buildRoleAssignmentEnvelope(scope, info))
}

func (h *Handler) getRoleAssignment(w http.ResponseWriter, r *http.Request, scope, id string) {
	info, err := h.iam.GetRoleAssignment(r.Context(), id)
	if err != nil {
		writeARMError(w, http.StatusNotFound, "RoleAssignmentNotFound",
			"role assignment "+id+" not found")
		return
	}
	// Rewrite the ID with the requested scope so the SDK round-trips the
	// caller's path back unchanged.
	writeARMJSON(w, http.StatusOK, buildRoleAssignmentEnvelope(scope, info))
}

// listRoleAssignments answers GET at collection scope, honoring the real
// Azure $filter subset cloudemu supports:
//
//   - no filter (or $filter=atScope()): assignments at the queried scope and
//     its ancestors (permissions inherit downward, so this is the default
//     real Azure ListForScope narrowing).
//   - $filter=principalId eq '{guid}': every assignment for that principal,
//     at, above, or below the queried scope — real Azure widens the match to
//     the whole scope subtree for a principal-scoped query, so scope
//     narrowing is skipped unless combined with atScope().
//   - $filter=atScope() and principalId eq '{guid}': both applied together.
func (h *Handler) listRoleAssignments(w http.ResponseWriter, r *http.Request, scope string) {
	filter := parseRoleAssignmentFilter(r.URL.Query().Get("$filter"))

	items, err := h.iam.ListRoleAssignments(r.Context())
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := roleAssignmentList{Value: make([]roleAssignmentEnvelope, 0, len(items))}

	for i := range items {
		info := &items[i]

		if filter.principalID != "" && info.PrincipalID != filter.principalID {
			continue
		}

		if narrowByScope := filter.principalID == "" || filter.atScope; narrowByScope &&
			!scopeAssignmentMatches(scope, info.Scope) {
			continue
		}

		out.Value = append(out.Value, buildRoleAssignmentEnvelope(info.Scope, info))
	}

	writeARMJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteRoleAssignment(w http.ResponseWriter, r *http.Request, scope, id string) {
	info, err := h.iam.DeleteRoleAssignment(r.Context(), id)
	if err != nil {
		writeARMError(w, http.StatusNotFound, "RoleAssignmentNotFound",
			"role assignment "+id+" not found")
		return
	}

	writeARMJSON(w, http.StatusOK, buildRoleAssignmentEnvelope(scope, info))
}

// buildRoleAssignmentEnvelope returns the ARM JSON envelope for a single role
// assignment, rooted at the given scope (the caller's request scope for
// create/get/delete, or the assignment's own stored scope when listing).
func buildRoleAssignmentEnvelope(scope string, info *azureiam.RoleAssignmentInfo) roleAssignmentEnvelope {
	return roleAssignmentEnvelope{
		ID:   scope + providerSegmentCanonical + roleAssignmentsCanonical + "/" + info.ID,
		Name: info.ID,
		Type: typeRoleAssignment,
		Properties: roleAssignmentProperties{
			RoleDefinitionID: info.RoleDefinitionID,
			PrincipalID:      info.PrincipalID,
			PrincipalType:    info.PrincipalType,
			Scope:            info.Scope,
			Description:      info.Description,
			CreatedOn:        info.CreatedOn,
			UpdatedOn:        info.UpdatedOn,
		},
	}
}

// roleAssignmentFilter is the parsed subset of a roleAssignments $filter
// query cloudemu recognizes: atScope() and/or principalId eq '{guid}'. Real
// Azure's $filter grammar is broader (assignedTo(), roleDefinitionId eq, and
// OData combinators); this covers the two forms real IaC/SDK callers use most.
type roleAssignmentFilter struct {
	atScope     bool
	principalID string
}

func parseRoleAssignmentFilter(raw string) roleAssignmentFilter {
	var filter roleAssignmentFilter

	for _, clause := range strings.Split(raw, " and ") {
		clause = strings.TrimSpace(clause)

		switch {
		case strings.EqualFold(clause, "atScope()"):
			filter.atScope = true
		case strings.HasPrefix(strings.ToLower(clause), "principalid eq "):
			filter.principalID = trimODataString(clause[len("principalId eq "):])
		}
	}

	return filter
}

// trimODataString strips the single or double quotes an OData string literal
// ('...' or "...") is wrapped in.
func trimODataString(s string) string {
	return strings.Trim(strings.TrimSpace(s), `'"`)
}

// scopeAssignmentMatches reports whether a stored assignment's scope is
// visible from a query scope. Real Azure RoleAssignments.ListForScope returns
// assignments AT the queried scope and at all ANCESTOR scopes (permissions
// inherit downward) — never at descendant scopes. An empty or root ("/")
// query returns everything.
func scopeAssignmentMatches(query, stored string) bool {
	return query == "" || query == "/" ||
		stored == query || strings.HasPrefix(query, stored+"/")
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

// roleDefinitionExists reports whether roleDefinitionID references a role
// definition known to this handler — either a seeded built-in or a
// driver-backed custom role. The id may be relative
// ("/providers/Microsoft.Authorization/roleDefinitions/{guid}") or fully
// scope-qualified; only the trailing GUID segment identifies the definition.
func (h *Handler) roleDefinitionExists(ctx context.Context, roleDefinitionID string) bool {
	guid := roleDefinitionGUID(roleDefinitionID)
	if guid == "" {
		return false
	}

	if _, ok := h.builtins[guid]; ok {
		return true
	}

	if _, err := h.iam.GetRole(ctx, guid); err == nil {
		return true
	}

	return false
}

// roleDefinitionGUID returns the trailing GUID segment of a roleDefinitionId,
// i.e. everything after the final "/". A bare id with no slash is returned
// unchanged.
func roleDefinitionGUID(roleDefinitionID string) string {
	trimmed := strings.TrimRight(roleDefinitionID, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:]
	}

	return trimmed
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
// scope for "list all in subscription"). Real Azure's RoleDefinitions List
// returns definitions "applicable at scope and above" (MS Learn:
// rest/api/authorization/role-definitions/list) — the query scope itself or
// one of its ancestors (management group / subscription / resource group)
// — never a definition scoped only to a descendant resource; that requires
// the separate atScopeAndBelow $filter, which we don't model.
func scopeMatches(query, stored string) bool {
	if query == "" || query == "/" {
		return true
	}

	if stored == "" || query == stored {
		return true
	}

	return strings.HasPrefix(query, stored+"/")
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
