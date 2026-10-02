// Package tagsatscope holds the Azure Tags resource-provider tag sets
// (Microsoft.Resources/tags/default), one per scope, in a persisted store so
// serve --persist keeps them across a restart. The ARM wire handler in
// server/azure/tags owns the Merge/Replace/Delete semantics.
package tagsatscope

import (
	"maps"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// Mock is the tag-set store keyed by the normalized scope.
type Mock struct {
	store *memstore.Store[map[string]string]
}

// New returns an empty tag-set store.
func New() *Mock {
	return &Mock{store: memstore.New[map[string]string]()}
}

// Get returns a copy of the tag set at scope. An unknown scope has an empty set.
func (m *Mock) Get(scope string) map[string]string {
	tags, _ := m.store.Get(scope)

	out := make(map[string]string, len(tags))
	maps.Copy(out, tags)

	return out
}

// Set replaces the tag set at scope with a copy of tags.
func (m *Mock) Set(scope string, tags map[string]string) {
	stored := make(map[string]string, len(tags))
	maps.Copy(stored, tags)
	m.store.Set(scope, stored)
}

// Delete clears the tag set at scope.
func (m *Mock) Delete(scope string) {
	m.store.Delete(scope)
}
