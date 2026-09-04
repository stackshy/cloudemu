package iam

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// RoleAssignmentConfig is the input to CreateRoleAssignment: an Azure RBAC
// (principal, roleDefinition, scope) binding. It has no AWS-shaped equivalent,
// so it lives on the concrete Mock rather than the driver.IAM interface — the
// Azure wire server (server/azure/iam) calls these methods directly.
type RoleAssignmentConfig struct {
	ID               string // the assignment GUID (the ARM {id} path segment)
	RoleDefinitionID string // relative or fully scope-qualified roleDefinitions id
	PrincipalID      string
	PrincipalType    string
	Scope            string
	Description      string
}

// RoleAssignmentInfo is the stored state of an Azure role assignment,
// returned by every RoleAssignment* method.
type RoleAssignmentInfo struct {
	ID               string
	RoleDefinitionID string
	PrincipalID      string
	PrincipalType    string
	Scope            string
	Description      string
	CreatedOn        string
	UpdatedOn        string
}

// CreateRoleAssignment stores a new role assignment. It enforces, atomically
// under m.mu, the two uniqueness invariants real Azure applies: the
// assignment id (GUID) must be unused, and the (principalId, roleDefinition,
// scope) triple must not already be bound under a different id.
func (m *Mock) CreateRoleAssignment(_ context.Context, cfg RoleAssignmentConfig) (*RoleAssignmentInfo, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "role assignment id is required")
	}

	if cfg.RoleDefinitionID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "roleDefinitionId is required")
	}

	if cfg.PrincipalID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "principalId is required")
	}

	wantGUID := roleAssignmentGUID(cfg.RoleDefinitionID)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.roleAssignments[cfg.ID]; exists {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "a role assignment with id %q already exists", cfg.ID)
	}

	for _, a := range m.roleAssignments {
		if a.PrincipalID == cfg.PrincipalID && a.Scope == cfg.Scope && roleAssignmentGUID(a.RoleDefinitionID) == wantGUID {
			return nil, cerrors.New(cerrors.AlreadyExists,
				"the role assignment already exists for this principal, role and scope")
		}
	}

	now := m.opts.Clock.Now().UTC().Format(timeFormat)

	info := &RoleAssignmentInfo{
		ID:               cfg.ID,
		RoleDefinitionID: cfg.RoleDefinitionID,
		PrincipalID:      cfg.PrincipalID,
		PrincipalType:    cfg.PrincipalType,
		Scope:            cfg.Scope,
		Description:      cfg.Description,
		CreatedOn:        now,
		UpdatedOn:        now,
	}

	m.roleAssignments[cfg.ID] = info

	out := *info

	return &out, nil
}

// GetRoleAssignment returns the role assignment with the given id.
func (m *Mock) GetRoleAssignment(_ context.Context, id string) (*RoleAssignmentInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.roleAssignments[id]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "role assignment %q not found", id)
	}

	out := *a

	return &out, nil
}

// DeleteRoleAssignment removes a role assignment and returns the deleted
// state, so the caller can echo it back per Azure's DELETE semantics.
func (m *Mock) DeleteRoleAssignment(_ context.Context, id string) (*RoleAssignmentInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.roleAssignments[id]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "role assignment %q not found", id)
	}

	delete(m.roleAssignments, id)

	out := *a

	return &out, nil
}

// ListRoleAssignments returns every stored role assignment, ordered by id for
// deterministic listing. Scope narrowing and $filter evaluation are ARM/REST
// query concerns handled by the wire layer (server/azure/iam).
func (m *Mock) ListRoleAssignments(_ context.Context) ([]RoleAssignmentInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]RoleAssignmentInfo, 0, len(m.roleAssignments))
	for _, a := range m.roleAssignments {
		out = append(out, *a)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

// RoleAssignmentsForRoleDefinition returns every stored role assignment whose
// roleDefinitionId resolves (by trailing GUID) to roleDefinitionGUID. The
// wire layer's RoleDefinition delete guard uses this reverse lookup to reject
// deleting a role definition that active assignments still reference,
// matching real Azure's RoleDefinitionHasAssignments error.
func (m *Mock) RoleAssignmentsForRoleDefinition(_ context.Context, roleDefinitionGUID string) ([]RoleAssignmentInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []RoleAssignmentInfo

	for _, a := range m.roleAssignments {
		if roleAssignmentGUID(a.RoleDefinitionID) == roleDefinitionGUID {
			out = append(out, *a)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

// roleAssignmentGUID returns the trailing GUID segment of a roleDefinitionId,
// i.e. everything after the final "/". A bare id with no slash is returned
// unchanged. This mirrors server/azure/iam's roleDefinitionGUID helper so a
// relative reference and a fully scope-qualified one to the same role compare
// equal; the two packages don't share the helper to avoid a needless coupling
// over a two-line string operation.
func roleAssignmentGUID(roleDefinitionID string) string {
	trimmed := strings.TrimRight(roleDefinitionID, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:]
	}

	return trimmed
}
