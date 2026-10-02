// Package rgstore provides the in-memory store behind the Azure
// Resource Manager resource-group API (Microsoft.Resources/resourceGroups).
//
// Every ARM resource lives in a resource group, so the store sits on the
// provider next to the services whose resources it gates. That keeps resource
// groups in snapshots, so a persisted server comes back with its groups as well
// as the resources inside them.
//
// A group is keyed by its lowercased subscription and name, because ARM
// resolves both case-insensitively. The stored body keeps the original casing.
package rgstore

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// Mock stores resource-group bodies, the ARM JSON the handler serves.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[map[string]any]
}

// New returns an empty resource-group store.
func New(_ *config.Options) *Mock {
	return &Mock{store: memstore.New[map[string]any]()}
}

func key(sub, name string) string {
	return strings.ToLower(sub) + "/" + strings.ToLower(name)
}

// Put stores body as the group sub/name and reports whether a group of that
// name already existed.
func (m *Mock) Put(sub, name string, body map[string]any) (existed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, name)
	existed = m.store.Has(k)
	m.store.Set(k, clone(body))

	return existed
}

// Get returns a copy of the group sub/name.
func (m *Mock) Get(sub, name string) (map[string]any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	body, ok := m.store.Get(key(sub, name))
	if !ok {
		return nil, false
	}

	return clone(body), true
}

// Exists reports whether the group sub/name exists.
func (m *Mock) Exists(sub, name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.store.Has(key(sub, name))
}

// Delete removes the group sub/name and reports whether it existed.
func (m *Mock) Delete(sub, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, name))
}

// List returns copies of every group in sub, ordered by lowercased name.
func (m *Mock) List(sub string) []map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := strings.ToLower(sub) + "/"
	matched := m.store.Filter(func(k string, _ map[string]any) bool { return strings.HasPrefix(k, prefix) })

	keys := make([]string, 0, len(matched))
	for k := range matched {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, clone(matched[k]))
	}

	return out
}

// clone deep-copies a JSON-shaped body so callers never share the stored map.
func clone(body map[string]any) map[string]any {
	data, err := json.Marshal(body)
	if err != nil {
		return body
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return body
	}

	return out
}
