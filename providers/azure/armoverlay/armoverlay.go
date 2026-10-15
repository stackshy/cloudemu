// Package armoverlay stores the ARM request properties the Azure server echoes
// back for a resource although no handler models them, keyed by resource id.
//
// The store sits on the provider so it is part of snapshots: a persisted server
// comes back still echoing the properties a client set, so an IaC re-plan after
// a restart sees no drift.
package armoverlay

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// Mock maps a resource id to its unmodeled properties.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[map[string]any]
}

// New returns an empty overlay store.
func New(_ *config.Options) *Mock {
	return &Mock{store: memstore.New[map[string]any]()}
}

// Capture records props for id, replacing any previous entry. An empty set
// clears the entry.
func (m *Mock) Capture(id string, props map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(props) == 0 {
		m.store.Delete(id)
		return
	}

	m.store.Set(id, clone(props))
}

// Lookup returns a copy of the properties recorded for id, or nil.
func (m *Mock) Lookup(id string) map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	props, ok := m.store.Get(id)
	if !ok {
		return nil
	}

	return clone(props)
}

// EvictTree drops the entry for id and every entry nested under it, compared
// case-insensitively and bounded by a slash, so evicting rg1 never touches
// rg10.
func (m *Mock) EvictTree(id string) {
	target := strings.ToLower(strings.TrimRight(id, "/"))
	if target == "" {
		return
	}

	prefix := target + "/"

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range m.store.Keys() {
		lk := strings.ToLower(key)
		if lk == target || strings.HasPrefix(lk, prefix) {
			m.store.Delete(key)
		}
	}
}

// clone deep-copies a JSON-shaped property set so callers never share the
// stored map.
func clone(props map[string]any) map[string]any {
	data, err := json.Marshal(props)
	if err != nil {
		return props
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return props
	}

	return out
}
