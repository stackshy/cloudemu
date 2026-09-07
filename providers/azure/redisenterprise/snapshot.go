package redisenterprise

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotState is the on-disk shape: both the cluster and database stores keyed
// by their (lowercased) resource ids. The estate tenant id is deterministic
// (minted from a fixed seed in New), so it needs no persistence.
type snapshotState struct {
	Clusters  json.RawMessage `json:"clusters,omitempty"`
	Databases json.RawMessage `json:"databases,omitempty"`
}

// Snapshot captures every redisEnterprise cluster and database. includeAssets is
// unused — these resources hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clusters, err := m.clusters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("redisenterprise: snapshot clusters: %w", err)
	}

	databases, err := m.databases.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("redisenterprise: snapshot databases: %w", err)
	}

	data, err := json.Marshal(snapshotState{Clusters: clusters, Databases: databases})
	if err != nil {
		return nil, fmt.Errorf("redisenterprise: marshal snapshot: %w", err)
	}

	return data, nil
}

// Restore rebuilds every redisEnterprise cluster and database under its original
// id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("redisenterprise: unmarshal snapshot: %w", err)
	}

	if len(state.Clusters) > 0 {
		if err := m.clusters.LoadSnapshot(state.Clusters); err != nil {
			return fmt.Errorf("redisenterprise: restore clusters: %w", err)
		}
	}

	if len(state.Databases) > 0 {
		if err := m.databases.LoadSnapshot(state.Databases); err != nil {
			return fmt.Errorf("redisenterprise: restore databases: %w", err)
		}
	}

	return nil
}
