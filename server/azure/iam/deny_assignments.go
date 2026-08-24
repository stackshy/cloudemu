package iam

import (
	"net/http"
	"strings"
	"sync"
)

// DenyAssignments are a read-only RBAC surface in real Azure: they are created
// only by Azure itself (Blueprints, Managed Applications, system-protected
// assignments) — there is no customer-facing create/update/delete API, only
// Get and List. We therefore back them with an in-handler store that starts
// empty and expose only the read verbs. Listing an account with no deny
// assignments returns an empty (but correctly enveloped) collection, and Get
// on any id returns 404 — exactly what a real subscription with no deny
// assignments returns.
const (
	denyAssignmentsSuffix    = "denyassignments"
	denyAssignmentsCanonical = "denyAssignments"
)

// denyPrincipal is the {type, id} principal shape used by both principals and
// excludePrincipals in a deny assignment.
type denyPrincipal struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type,omitempty"`
}

// denyPermission is the {actions, notActions, dataActions, notDataActions} bag
// inside a DenyAssignment.properties.permissions[] entry.
type denyPermission struct {
	Actions        []string `json:"actions,omitempty"`
	NotActions     []string `json:"notActions,omitempty"`
	DataActions    []string `json:"dataActions,omitempty"`
	NotDataActions []string `json:"notDataActions,omitempty"`
}

// denyAssignmentProperties is the ARM properties block for a DenyAssignment.
type denyAssignmentProperties struct {
	DenyAssignmentName      string           `json:"denyAssignmentName,omitempty"`
	Description             string           `json:"description,omitempty"`
	Permissions             []denyPermission `json:"permissions,omitempty"`
	Scope                   string           `json:"scope,omitempty"`
	DoNotApplyToChildScopes bool             `json:"doNotApplyToChildScopes"`
	Principals              []denyPrincipal  `json:"principals,omitempty"`
	ExcludePrincipals       []denyPrincipal  `json:"excludePrincipals,omitempty"`
	IsSystemProtected       bool             `json:"isSystemProtected"`
	CreatedOn               string           `json:"createdOn,omitempty"`
	UpdatedOn               string           `json:"updatedOn,omitempty"`
}

// denyAssignmentEnvelope is the full ARM envelope returned on GET.
type denyAssignmentEnvelope struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	Type       string                   `json:"type"`
	Properties denyAssignmentProperties `json:"properties"`
}

// denyAssignmentList is the ARM paged-list shape returned on a collection GET.
type denyAssignmentList struct {
	Value []denyAssignmentEnvelope `json:"value"`
}

// denyAssignmentStore is a thread-safe store for deny assignments. It starts
// empty; there is no wire path that mutates it (matching Azure, where deny
// assignments are created only by the platform).
type denyAssignmentStore struct {
	mu    sync.RWMutex
	items map[string]denyAssignmentEnvelope
}

func newDenyAssignmentStore() *denyAssignmentStore {
	return &denyAssignmentStore{items: map[string]denyAssignmentEnvelope{}}
}

func (s *denyAssignmentStore) get(id string) (denyAssignmentEnvelope, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	env, ok := s.items[id]

	return env, ok
}

// listAtScope returns deny assignments visible from the query scope, mirroring
// RoleAssignments.listAtScope: assignments AT the queried scope and at all
// ANCESTOR scopes. With an empty store this is always an empty slice.
func (s *denyAssignmentStore) listAtScope(scope string) []denyAssignmentEnvelope {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]denyAssignmentEnvelope, 0, len(s.items))

	for id := range s.items {
		env := s.items[id]
		if scope == "" || scope == "/" ||
			env.Properties.Scope == scope ||
			strings.HasPrefix(scope, env.Properties.Scope+"/") {
			out = append(out, env)
		}
	}

	return out
}

// serveDenyAssignments dispatches GET (Get or List). All other verbs are
// rejected — deny assignments are read-only over the wire.
func (h *Handler) serveDenyAssignments(w http.ResponseWriter, r *http.Request, scope, id string) {
	if r.Method != http.MethodGet {
		writeARMError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"denyAssignments is a read-only surface: "+r.Method+" is not supported")
		return
	}

	if id == "" {
		items := h.denyAssignments.listAtScope(scope)
		writeARMJSON(w, http.StatusOK, denyAssignmentList{Value: items})

		return
	}

	env, ok := h.denyAssignments.get(id)
	if !ok {
		writeARMError(w, http.StatusNotFound, "DenyAssignmentNotFound",
			"deny assignment "+id+" not found")
		return
	}

	env.ID = scope + providerSegmentCanonical + denyAssignmentsCanonical + "/" + id
	writeARMJSON(w, http.StatusOK, env)
}
