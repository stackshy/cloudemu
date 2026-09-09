package managedgrafana

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// Snapshot captures every grafana resource keyed by its (lowercased) resource
// id. The estate tenant id is deterministic (minted from a fixed seed in New),
// so it needs no persistence. includeAssets is unused — grafana resources hold
// no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := m.store.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("managedgrafana: snapshot store: %w", err)
	}

	return data, nil
}

// Restore rebuilds every grafana resource under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	if err := m.store.LoadSnapshot(data); err != nil {
		return fmt.Errorf("managedgrafana: restore store: %w", err)
	}

	return nil
}
