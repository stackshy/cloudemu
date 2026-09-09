package loadtesting

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// Snapshot captures every load-test resource keyed by its (lowercased) resource
// id. The estate tenant id is deterministic (minted from a fixed seed in New),
// so it needs no persistence. includeAssets is unused — load tests hold no bulk
// object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := m.store.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("loadtesting: snapshot store: %w", err)
	}

	return data, nil
}

// Restore rebuilds every load-test resource under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	if err := m.store.LoadSnapshot(data); err != nil {
		return fmt.Errorf("loadtesting: restore store: %w", err)
	}

	return nil
}
