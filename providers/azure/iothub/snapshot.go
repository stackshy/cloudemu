package iothub

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotState is the on-disk shape: both the hub and consumer-group stores,
// keyed by their (lowercased) resource ids. A hub's shared-access-policy keys and
// event-hub endpoint are embedded in the hub record, so no separate key store is
// needed.
type snapshotState struct {
	Hubs           json.RawMessage `json:"hubs,omitempty"`
	ConsumerGroups json.RawMessage `json:"consumerGroups,omitempty"`
}

// Snapshot captures every IoT hub (with its keys and endpoint) and consumer
// group. includeAssets is unused — these resources hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hubs, err := m.hubs.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("iothub: snapshot hubs: %w", err)
	}

	groups, err := m.consumerGroups.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("iothub: snapshot consumer groups: %w", err)
	}

	data, err := json.Marshal(snapshotState{Hubs: hubs, ConsumerGroups: groups})
	if err != nil {
		return nil, fmt.Errorf("iothub: marshal snapshot: %w", err)
	}

	return data, nil
}

// Restore rebuilds every IoT hub and consumer group under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("iothub: unmarshal snapshot: %w", err)
	}

	if len(state.Hubs) > 0 {
		if err := m.hubs.LoadSnapshot(state.Hubs); err != nil {
			return fmt.Errorf("iothub: restore hubs: %w", err)
		}
	}

	if len(state.ConsumerGroups) > 0 {
		if err := m.consumerGroups.LoadSnapshot(state.ConsumerGroups); err != nil {
			return fmt.Errorf("iothub: restore consumer groups: %w", err)
		}
	}

	return nil
}
