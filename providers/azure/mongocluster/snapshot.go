package mongocluster

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotState is the on-disk shape: the cluster store keyed by its (lowercased)
// resource id.
type snapshotState struct {
	Clusters json.RawMessage `json:"clusters,omitempty"`
}

// Snapshot captures every mongo cluster. includeAssets is unused — these
// resources hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clusters, err := m.clusters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("mongocluster: snapshot clusters: %w", err)
	}

	data, err := json.Marshal(snapshotState{Clusters: clusters})
	if err != nil {
		return nil, fmt.Errorf("mongocluster: marshal snapshot: %w", err)
	}

	return data, nil
}

// Restore rebuilds every mongo cluster under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("mongocluster: unmarshal snapshot: %w", err)
	}

	if len(state.Clusters) > 0 {
		if err := m.clusters.LoadSnapshot(state.Clusters); err != nil {
			return fmt.Errorf("mongocluster: restore clusters: %w", err)
		}
	}

	return nil
}
