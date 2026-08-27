package elasticache

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// persistSnapshot is the full serialized state of the ElastiCache mock (named to
// avoid colliding with the domain CreateSnapshot backup type). The caches store
// holds an unexported *cacheData whose nested items store (key→value entries)
// must be captured, so it is promoted; the other stores hold fully-exported
// types (driver.SubnetGroup/ReplicationGroup/Snapshot and the exported
// ParameterGroup) and round-trip through the generic memstore helper. tagsByARN
// carries the resource tags kept beside the stores. The mutexes and the wired
// deps (opts, monitoring, subnetResolver) are not serialized.
type persistSnapshot struct {
	Caches            map[string]*cacheDataSnapshot `json:"caches,omitempty"`
	SubnetGroups      json.RawMessage               `json:"subnetGroups,omitempty"`
	ReplicationGroups json.RawMessage               `json:"replicationGroups,omitempty"`
	ParameterGroups   json.RawMessage               `json:"parameterGroups,omitempty"`
	Snapshots         json.RawMessage               `json:"snapshots,omitempty"`
	TagsByARN         map[string]map[string]string  `json:"tagsByArn,omitempty"`
}

// cacheDataSnapshot mirrors cacheData: its info is exported, and its items store
// (whose value type cacheItem has only exported fields) round-trips through the
// generic memstore helper.
type cacheDataSnapshot struct {
	Info  cachedriver.CacheInfo `json:"info"`
	Items json.RawMessage       `json:"items,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// cache entry values are the payload and are always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap persistSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	caches, err := m.snapshotCaches()
	if err != nil {
		return nil, err
	}

	snap.Caches = caches

	m.tagMu.Lock()
	snap.TagsByARN = m.tagsByARN
	m.tagMu.Unlock()

	return json.Marshal(snap)
}

func (m *Mock) snapshotCaches() (map[string]*cacheDataSnapshot, error) {
	if m.caches.Len() == 0 {
		return nil, nil
	}

	out := make(map[string]*cacheDataSnapshot, m.caches.Len())

	for id, cd := range m.caches.All() {
		items, err := cd.items.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("elasticache: snapshot cache items: %w", err)
		}

		out[id] = &cacheDataSnapshot{Info: cd.info, Items: items}
	}

	return out, nil
}

func (m *Mock) snapshotStores(snap *persistSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.SubnetGroups, m.subnetGroups.Snapshot},
		{&snap.ReplicationGroups, m.replicationGroups.Snapshot},
		{&snap.ParameterGroups, m.parameterGroups.Snapshot},
		{&snap.Snapshots, m.snapshots.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("elasticache: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every cluster,
// replication group, snapshot and cache entry key is preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap persistSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("elasticache: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	if err := m.restoreCaches(snap.Caches); err != nil {
		return err
	}

	if snap.TagsByARN != nil {
		m.tagMu.Lock()
		m.tagsByARN = snap.TagsByARN
		m.tagMu.Unlock()
	}

	return nil
}

func (m *Mock) restoreCaches(caches map[string]*cacheDataSnapshot) error {
	for id, cs := range caches {
		cd := &cacheData{info: cs.Info, items: memstore.New[cacheItem]()}
		if len(cs.Items) > 0 {
			if err := cd.items.LoadSnapshot(cs.Items); err != nil {
				return fmt.Errorf("elasticache: restore cache items: %w", err)
			}
		}

		m.caches.Set(id, cd)
	}

	return nil
}

func (m *Mock) restoreStores(snap *persistSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.SubnetGroups, m.subnetGroups.LoadSnapshot},
		{snap.ReplicationGroups, m.replicationGroups.LoadSnapshot},
		{snap.ParameterGroups, m.parameterGroups.LoadSnapshot},
		{snap.Snapshots, m.snapshots.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("elasticache: restore store: %w", err)
		}
	}

	return nil
}
