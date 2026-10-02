// Package tagsatscope holds the Azure Tags resource-provider tag sets
// (Microsoft.Resources/tags/default), one per scope, in a persisted store so
// serve --persist keeps them across a restart. The ARM wire handler in
// server/azure/tags owns the Merge/Replace/Delete semantics.
package tagsatscope

import (
	"context"
	"maps"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/scope"
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

// PurgeResourceGroup drops the tag set of the resource group and of every
// resource inside it, so a resource-group delete leaves no tags behind for a
// later group or resource of the same name.
func (m *Mock) PurgeResourceGroup(_ context.Context, subscription, resourceGroup string) error {
	for _, k := range m.store.Keys() {
		if scope.IDInResourceGroup(k, subscription, resourceGroup) {
			m.store.Delete(k)
		}
	}

	return nil
}

// EvictTree clears the tag set at the ARM id and at every scope nested under
// it, compared case-insensitively and bounded by a slash, so a deleted
// resource or resource group leaves no stale tags for a same-named successor.
func (m *Mock) EvictTree(id string) {
	target := strings.ToLower(strings.Trim(id, "/"))
	if target == "" {
		return
	}

	prefix := target + "/"

	for _, key := range m.store.Keys() {
		lk := strings.ToLower(strings.Trim(key, "/"))
		if lk == target || strings.HasPrefix(lk, prefix) {
			m.store.Delete(key)
		}
	}
}
