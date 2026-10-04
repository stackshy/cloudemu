// Package managementlocks holds Azure management locks (Microsoft.Authorization/locks) in
// a persisted store, so serve --persist keeps them, and the delete protection
// they give, across a restart. The ARM wire handler and the enforcement gate in
// server/azure/locks own the request semantics.
package managementlocks

import (
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// Lock is one management lock. Scope and Name preserve their original casing so
// a lock's ARM id round-trips exactly as the caller addressed it.
type Lock struct {
	Scope string
	Name  string
	Level string
	Notes string
}

// Mock is the management-lock store, keyed case-insensitively by (scope,
// lockName) so a lock created via one SDK scope variant is found by another that
// differs only in path casing (e.g. resourceGroups vs resourcegroups).
type Mock struct {
	mu    sync.Mutex // makes Put's create-or-replace report atomic
	store *memstore.Store[Lock]
}

// New returns an empty lock store.
func New() *Mock {
	return &Mock{store: memstore.New[Lock]()}
}

// key builds the case-insensitive key for a (scope, name) pair. The NUL
// separator cannot appear in an ARM path, so distinct pairs never collide.
func key(scope, name string) string {
	return strings.ToLower(scope) + "\x00" + strings.ToLower(name)
}

// Put creates or replaces a lock, returning the stored value and whether it was
// newly created (true) versus updated in place (false).
func (m *Mock) Put(scope, name, level, notes string) (Lock, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(scope, name)
	existed := m.store.Has(k)

	l := Lock{Scope: scope, Name: name, Level: level, Notes: notes}
	m.store.Set(k, l)

	return l, !existed
}

// Get returns the lock at (scope, name), if present.
func (m *Mock) Get(scope, name string) (Lock, bool) {
	return m.store.Get(key(scope, name))
}

// Delete removes the lock at (scope, name), reporting whether it existed.
func (m *Mock) Delete(scope, name string) bool {
	return m.store.Delete(key(scope, name))
}

// Covering returns every lock at path or at any ancestor scope above it: the
// upward complement of List. A stored lock at scope L covers path P iff P == L
// or P starts with L+"/" (a segment-boundary prefix), which yields
// subscription, resource-group and resource inheritance without enumerating
// ancestors. The trailing-slash boundary keeps /subscriptions/S1 from matching
// /subscriptions/S10. Comparison is case-insensitive.
func (m *Mock) Covering(path string) []Lock {
	want := strings.ToLower(path)

	var out []Lock

	for _, l := range m.store.All() {
		got := strings.ToLower(l.Scope)
		if want == got || strings.HasPrefix(want, got+"/") {
			out = append(out, l)
		}
	}

	return out
}

// List returns every lock at scope and, mirroring real Azure's inheritance, at
// any child scope beneath it, ordered by (scope, name).
func (m *Mock) List(scope string) []Lock {
	want := strings.ToLower(scope)

	var out []Lock

	for _, l := range m.store.All() {
		got := strings.ToLower(l.Scope)
		if got == want || strings.HasPrefix(got, want+"/") {
			out = append(out, l)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}

		return out[i].Name < out[j].Name
	})

	return out
}
