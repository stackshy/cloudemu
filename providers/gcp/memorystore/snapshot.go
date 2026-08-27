package memorystore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// memorystoreSnapshot is the full serialized state of the GCP Memorystore mock:
// every cache instance keyed by name, each carrying its metadata and its stored
// key/value items. The stored *cacheData has an unexported layout (a nested item
// store), so it is promoted to cacheSnapshot; the wired *config.Options and
// monitoring backend are intentionally not serialized.
type memorystoreSnapshot struct {
	Caches map[string]*cacheSnapshot `json:"caches,omitempty"`
}

// cacheSnapshot mirrors cacheData, promoting its unexported item store to an
// exported dump. cacheItem is fully exported, so its store round-trips through
// the generic memstore helper.
type cacheSnapshot struct {
	Info  driver.CacheInfo `json:"info"`
	Items json.RawMessage  `json:"items,omitempty"`
}

// Snapshot captures every cache and its items as JSON. Cache values are the
// service's whole state, so they are always captured regardless of includeAssets.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := memorystoreSnapshot{Caches: make(map[string]*cacheSnapshot, m.caches.Len())}

	for name, cd := range m.caches.All() {
		items, err := cd.items.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("memorystore: snapshot items: %w", err)
		}

		snap.Caches[name] = &cacheSnapshot{Info: cd.info, Items: items}
	}

	return json.Marshal(snap)
}

// Restore rebuilds every cache under its original name with its items intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap memorystoreSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("memorystore: parse snapshot: %w", err)
	}

	for name, cs := range snap.Caches {
		cd := &cacheData{info: cs.Info, items: memstore.New[cacheItem]()}
		if len(cs.Items) > 0 {
			if err := cd.items.LoadSnapshot(cs.Items); err != nil {
				return fmt.Errorf("memorystore: restore items: %w", err)
			}
		}

		m.caches.Set(name, cd)
	}

	return nil
}
