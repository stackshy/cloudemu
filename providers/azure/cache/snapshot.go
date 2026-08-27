package cache

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cacheSnapshot is the full serialized state of the Azure Cache mock: every
// cache instance keyed by name, each carrying its info and its key/value items.
// cacheData is unexported, so it is promoted to an exported form; the items store
// round-trips through the generic memstore helper (cacheItem has only exported
// fields). The wired options/monitoring are intentionally not serialized.
type cacheSnapshot struct {
	Caches map[string]*cacheDataSnapshot `json:"caches,omitempty"`
}

// cacheDataSnapshot is the exported form of cacheData.
type cacheDataSnapshot struct {
	Info  driver.CacheInfo `json:"info"`
	Items json.RawMessage  `json:"items,omitempty"`
}

// Snapshot captures every cache instance's state as JSON. includeAssets is unused
// — the cached items are the resource, so they are always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := cacheSnapshot{Caches: make(map[string]*cacheDataSnapshot, m.caches.Len())}

	for name, cd := range m.caches.All() {
		items, err := cd.items.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("cache: snapshot items: %w", err)
		}

		snap.Caches[name] = &cacheDataSnapshot{Info: cd.info, Items: items}
	}

	return json.Marshal(snap)
}

// Restore rebuilds every cache instance under its original name with its items
// intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cacheSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cache: parse snapshot: %w", err)
	}

	for name, cs := range snap.Caches {
		cd := &cacheData{info: cs.Info, items: memstore.New[cacheItem]()}
		if len(cs.Items) > 0 {
			if err := cd.items.LoadSnapshot(cs.Items); err != nil {
				return fmt.Errorf("cache: restore items: %w", err)
			}
		}

		m.caches.Set(name, cd)
	}

	return nil
}
