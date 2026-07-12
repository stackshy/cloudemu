// Package iam implements the Azure Microsoft.Authorization ARM REST API as
// a server.Handler. Real azure-sdk-for-go armauthorization clients pointed
// at this server can CRUD RoleDefinitions and RoleAssignments end-to-end.
//
// Coverage (api-version 2022-04-01):
//
//	PUT    /{scope}/providers/Microsoft.Authorization/roleDefinitions/{id}   — CreateOrUpdate
//	GET    /{scope}/providers/Microsoft.Authorization/roleDefinitions/{id}   — Get
//	DELETE /{scope}/providers/Microsoft.Authorization/roleDefinitions/{id}   — Delete
//	GET    /{scope}/providers/Microsoft.Authorization/roleDefinitions        — List
//	PUT    /{scope}/providers/Microsoft.Authorization/roleAssignments/{id}   — Create
//	GET    /{scope}/providers/Microsoft.Authorization/roleAssignments/{id}   — Get
//	DELETE /{scope}/providers/Microsoft.Authorization/roleAssignments/{id}   — Delete
//	GET    /{scope}/providers/Microsoft.Authorization/roleAssignments        — List at scope
//
// Scope can be subscription, resource-group, resource, or
// management-group — anything that appears before /providers/Microsoft.Authorization
// in the URL. The handler treats it as an opaque string.
//
// RoleDefinitions back through the shared iamdriver.IAM (each Azure role
// definition is stored as a driver Role with AssumeRolePolicyDoc holding the
// ARM properties JSON). RoleAssignments live in an in-handler store —
// Azure's RoleAssignment shape (principal + role + scope) does not map onto
// the AWS-shaped driver interface.
package iam

import (
	"net/http"
	"strings"

	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	// providerSegment is the lower-case marker we search for in URLs.
	// Real SDKs vary in case, so URL matching always lower-cases the path.
	providerSegment       = "/providers/microsoft.authorization/"
	roleDefinitionsSuffix = "roledefinitions"
	roleAssignmentsSuffix = "roleassignments"

	// providerSegmentCanonical is the cased segment we embed in returned
	// resource IDs so SDK consumers see Microsoft's canonical capitalization
	// (some downstream tooling does case-sensitive checks on the id field).
	providerSegmentCanonical = "/providers/Microsoft.Authorization/"
	roleDefinitionsCanonical = "roleDefinitions"
	roleAssignmentsCanonical = "roleAssignments"
)

// Handler serves Microsoft.Authorization ARM RBAC requests.
type Handler struct {
	iam         iamdriver.IAM
	assignments *assignmentStore
}

// New returns a handler backed by drv for role definitions, with an empty
// in-memory store for role assignments.
func New(drv iamdriver.IAM) *Handler {
	return &Handler{
		iam:         drv,
		assignments: newAssignmentStore(),
	}
}

// Matches claims any path containing /providers/Microsoft.Authorization/
// followed by roleDefinitions or roleAssignments. Comparisons are
// case-insensitive because Azure SDK URL templates sometimes lower-case the
// resource type.
func (*Handler) Matches(r *http.Request) bool {
	lower := strings.ToLower(r.URL.Path)

	idx := strings.Index(lower, providerSegment)
	if idx < 0 {
		return false
	}

	tail := lower[idx+len(providerSegment):]

	return strings.HasPrefix(tail, roleDefinitionsSuffix) ||
		strings.HasPrefix(tail, roleAssignmentsSuffix)
}

// ServeHTTP routes by resource type (definitions vs assignments) and HTTP verb.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	scope, kind, id, ok := parseAuthPath(r.URL.Path)
	if !ok {
		writeARMError(w, http.StatusBadRequest, "InvalidPath",
			"malformed Microsoft.Authorization path")
		return
	}

	switch kind {
	case roleDefinitionsSuffix:
		h.serveRoleDefinitions(w, r, scope, id)
	case roleAssignmentsSuffix:
		h.serveRoleAssignments(w, r, scope, id)
	default:
		writeARMError(w, http.StatusNotFound, "ResourceNotFound",
			"unknown Microsoft.Authorization resource: "+kind)
	}
}

// parseAuthPath splits a Microsoft.Authorization URL into (scope, kind, id).
//
//   - scope is everything before "/providers/Microsoft.Authorization/" — the
//     RBAC scope (subscription, resource group, resource, management group, …),
//     normalized to start with a leading "/" and with any trailing slash trimmed.
//   - kind is "roledefinitions" or "roleassignments" (lower-case).
//   - id is the {id} segment, or "" for a collection (list) request.
//
// Returns ok=false if the path doesn't match the expected shape.
func parseAuthPath(urlPath string) (scope, kind, id string, ok bool) {
	lower := strings.ToLower(urlPath)

	idx := strings.Index(lower, providerSegment)
	if idx < 0 {
		return "", "", "", false
	}

	scope = normalizeScope(urlPath[:idx])

	tail := strings.TrimRight(urlPath[idx+len(providerSegment):], "/")
	parts := strings.SplitN(tail, "/", 2) //nolint:mnd // splitting "kind/id" at most once
	kind = strings.ToLower(parts[0])

	if len(parts) > 1 {
		id = parts[1]
	}

	return scope, kind, id, true
}

// normalizeScope canonicalizes the chunk of an ARM URL that precedes the
// /providers/Microsoft.Authorization/ segment. Trailing slashes are trimmed,
// a leading slash is added so callers always see a path-rooted scope, and
// the empty path becomes "/" (subscription-less root for tenant-scoped ops).
func normalizeScope(raw string) string {
	trimmed := strings.TrimRight(raw, "/")
	if trimmed == "" {
		return "/"
	}

	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}

	return trimmed
}
