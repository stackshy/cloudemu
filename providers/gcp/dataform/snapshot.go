package dataform

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// dataformSnapshot is the full serialized state of the Dataform mock. The store
// holds fully-exported driver value types keyed by their full GCP resource name,
// so it round-trips through the generic memstore helper — no field promotion is
// needed. The wired deps (m.opts) and the RWMutex are intentionally not
// serialized.
type dataformSnapshot struct {
	Repositories json.RawMessage `json:"repositories,omitempty"`
}

// Snapshot captures every repository as JSON. includeAssets is unused — Dataform
// is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	b, err := m.repositories.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("dataform: snapshot store: %w", err)
	}

	return json.Marshal(dataformSnapshot{Repositories: b})
}

// Restore rebuilds every repository under its original resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap dataformSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("dataform: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(snap.Repositories) == 0 {
		return nil
	}

	if err := m.repositories.LoadSnapshot(snap.Repositories); err != nil {
		return fmt.Errorf("dataform: restore store: %w", err)
	}

	return nil
}
