package elasticsan

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// Snapshot captures every Elastic SAN keyed by its (lowercased) resource id.
// includeAssets is unused — Elastic SANs hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := m.store.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("elasticsan: snapshot store: %w", err)
	}

	return data, nil
}

// Restore rebuilds every Elastic SAN under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	if err := m.store.LoadSnapshot(data); err != nil {
		return fmt.Errorf("elasticsan: restore store: %w", err)
	}

	return nil
}
