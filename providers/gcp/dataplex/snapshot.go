package dataplex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// dataplexSnapshot is the full serialized state of the Dataplex mock. Every store
// holds fully-exported dpdriver value types keyed by their full GCP resource
// name, so each round-trips through the generic memstore helper — no field
// promotion is needed. opSeq is the operation-name counter, captured beside the
// stores so restored operation ids do not collide with fresh ones. The wired deps
// (m.opts) and the RWMutex are intentionally not serialized.
type dataplexSnapshot struct {
	Lakes      json.RawMessage `json:"lakes,omitempty"`
	Zones      json.RawMessage `json:"zones,omitempty"`
	Assets     json.RawMessage `json:"assets,omitempty"`
	Operations json.RawMessage `json:"operations,omitempty"`
	OpSeq      uint64          `json:"opSeq,omitempty"`
}

// Snapshot captures every lake, zone, asset, and operation as JSON. includeAssets
// is unused — Dataplex admin is control-plane only and holds no bulk object
// bodies (the storage/BigQuery resources an asset references live in their own
// services).
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap dataplexSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Lakes, m.lakes.Snapshot},
		{&snap.Zones, m.zones.Snapshot},
		{&snap.Assets, m.assets.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("dataplex: snapshot store: %w", err)
		}

		*d.dst = b
	}

	snap.OpSeq = m.opSeq.Load()

	return json.Marshal(snap)
}

// Restore rebuilds every lake, zone, asset, and operation under its original
// resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap dataplexSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("dataplex: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Lakes, m.lakes.LoadSnapshot},
		{snap.Zones, m.zones.LoadSnapshot},
		{snap.Assets, m.assets.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("dataplex: restore store: %w", err)
		}
	}

	m.opSeq.Store(snap.OpSeq)

	return nil
}
