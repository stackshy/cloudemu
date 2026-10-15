package insightscomponents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// Snapshot captures every component keyed by its scope. includeAssets is unused.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	data, err := m.store.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("insightscomponents: snapshot store: %w", err)
	}

	return data, nil
}

// Restore rebuilds every component under its original scope.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}

	if err := m.store.LoadSnapshot(data); err != nil {
		return fmt.Errorf("insightscomponents: restore store: %w", err)
	}

	return nil
}
