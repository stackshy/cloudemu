package iam

import (
	"strings"
	"sync"
)

// assignmentStore is a thread-safe in-memory store for Azure RoleAssignments.
// We cannot back this through the shared iamdriver.IAM because the AWS-shaped
// driver has no equivalent concept (it carries Users + Roles + Policies,
// whereas an Azure RoleAssignment is a (principal, roleDefinition, scope)
// triple). Keeping a small dedicated store here is the simplest path that
// matches real Azure semantics.
type assignmentStore struct {
	mu sync.RWMutex
	// items is keyed by assignment id (the {id} segment of the URL — a GUID
	// in real Azure, but we treat it as an opaque string).
	items map[string]roleAssignmentEnvelope
}

func newAssignmentStore() *assignmentStore {
	return &assignmentStore{items: map[string]roleAssignmentEnvelope{}}
}

// put inserts or updates an assignment. Returns the stored envelope.
// env is passed by pointer because the envelope is wider than the gocritic
// hugeParam threshold; the function only stores a copy.
func (s *assignmentStore) put(env *roleAssignmentEnvelope) roleAssignmentEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items[env.Name] = *env

	return *env
}

// get returns the assignment with the given id, or (_, false) if absent.
func (s *assignmentStore) get(id string) (roleAssignmentEnvelope, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	env, ok := s.items[id]

	return env, ok
}

// delete removes an assignment. Returns the deleted envelope so the caller
// can echo it back per Azure semantics (DELETE returns 200 with the prior
// resource), and ok=false if it wasn't there.
func (s *assignmentStore) delete(id string) (roleAssignmentEnvelope, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	env, ok := s.items[id]
	if !ok {
		return roleAssignmentEnvelope{}, false
	}

	delete(s.items, id)

	return env, true
}

// listAtScope returns every assignment whose stored scope starts with the
// query scope. Real Azure list-at-scope returns assignments AT the scope and
// at all ancestor scopes; we implement a simpler "prefix or exact match"
// that's correct for the typical narrow scope-equality case and a reasonable
// approximation otherwise.
func (s *assignmentStore) listAtScope(scope string) []roleAssignmentEnvelope {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]roleAssignmentEnvelope, 0, len(s.items))

	for id := range s.items {
		env := s.items[id]
		if scope == "" || scope == "/" ||
			env.Properties.Scope == scope ||
			strings.HasPrefix(env.Properties.Scope, scope+"/") ||
			strings.HasPrefix(scope, env.Properties.Scope+"/") {
			out = append(out, env)
		}
	}

	return out
}
