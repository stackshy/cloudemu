package locks

import (
	"sort"
	"strings"
	"sync"
)

// storedLock is one management lock. scope and name preserve their original
// casing so a lock's ARM id round-trips exactly as the caller addressed it.
type storedLock struct {
	scope string
	name  string
	level string
	notes string
}

// store is the in-memory management-lock backend, keyed case-insensitively by
// (scope, lockName) so a lock created via one SDK scope-level variant is found
// by another that differs only in path casing (e.g. resourceGroups vs
// resourcegroups).
type store struct {
	mu    sync.RWMutex
	locks map[string]storedLock
}

func newStore() *store {
	return &store{locks: map[string]storedLock{}}
}

// key builds the case-insensitive map key for a (scope, name) pair. The NUL
// separator cannot appear in an ARM path, so distinct pairs never collide.
func key(scope, name string) string {
	return strings.ToLower(scope) + "\x00" + strings.ToLower(name)
}

// put creates or replaces a lock, returning the stored value and whether it was
// newly created (true) versus updated in place (false).
func (s *store) put(scope, name, level, notes string) (storedLock, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(scope, name)
	_, existed := s.locks[k]

	l := storedLock{scope: scope, name: name, level: level, notes: notes}
	s.locks[k] = l

	return l, !existed
}

// get returns the lock at (scope, name), if present.
func (s *store) get(scope, name string) (storedLock, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	l, ok := s.locks[key(scope, name)]

	return l, ok
}

// delete removes the lock at (scope, name), reporting whether it existed.
func (s *store) delete(scope, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(scope, name)
	if _, ok := s.locks[k]; !ok {
		return false
	}

	delete(s.locks, k)

	return true
}

// covering returns every lock at path or at any ancestor scope above it — the
// upward complement of list. A stored lock at scope L covers path P iff
// P == L or P starts with L+"/" (a segment-boundary prefix), which yields
// subscription→resource-group→resource inheritance and extension-resource
// inheritance without enumerating ancestors. The trailing-slash boundary keeps
// /subscriptions/S1 from matching /subscriptions/S10. Comparison is
// case-insensitive, matching how scopes are keyed.
func (s *store) covering(path string) []storedLock {
	s.mu.RLock()
	defer s.mu.RUnlock()

	want := strings.ToLower(path)

	var out []storedLock

	for _, l := range s.locks {
		got := strings.ToLower(l.scope)
		if want == got || strings.HasPrefix(want, got+"/") {
			out = append(out, l)
		}
	}

	return out
}

// list returns every lock at scope and — mirroring real Azure's inheritance —
// at any child scope beneath it, ordered deterministically by (scope, name). A
// subscription-scope list therefore surfaces resource-group and resource locks;
// a resource-group-scope list surfaces its resources' locks.
func (s *store) list(scope string) []storedLock {
	s.mu.RLock()
	defer s.mu.RUnlock()

	want := strings.ToLower(scope)

	var out []storedLock

	for _, l := range s.locks {
		got := strings.ToLower(l.scope)
		if got == want || strings.HasPrefix(got, want+"/") {
			out = append(out, l)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].scope != out[j].scope {
			return out[i].scope < out[j].scope
		}

		return out[i].name < out[j].name
	})

	return out
}
