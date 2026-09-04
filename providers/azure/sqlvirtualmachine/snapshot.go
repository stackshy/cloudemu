package sqlvirtualmachine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// SQL virtual machines hold no bulk object bodies. Record is fully exported and
// round-trips through the generic memstore helper.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := m.store.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("sqlvirtualmachine: snapshot store: %w", err)
	}

	return data, nil
}

// Restore rebuilds every record under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.LoadSnapshot(data); err != nil {
		return fmt.Errorf("sqlvirtualmachine: restore store: %w", err)
	}

	return nil
}
