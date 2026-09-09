package batch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotState is the on-disk shape: both the account and pool stores keyed by
// their (lowercased) resource ids.
type snapshotState struct {
	Accounts json.RawMessage `json:"accounts,omitempty"`
	Pools    json.RawMessage `json:"pools,omitempty"`
}

// Snapshot captures every batch account and pool. includeAssets is unused —
// these resources hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	accounts, err := m.accounts.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("batch: snapshot accounts: %w", err)
	}

	pools, err := m.pools.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("batch: snapshot pools: %w", err)
	}

	data, err := json.Marshal(snapshotState{Accounts: accounts, Pools: pools})
	if err != nil {
		return nil, fmt.Errorf("batch: marshal snapshot: %w", err)
	}

	return data, nil
}

// Restore rebuilds every batch account and pool under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("batch: unmarshal snapshot: %w", err)
	}

	if len(state.Accounts) > 0 {
		if err := m.accounts.LoadSnapshot(state.Accounts); err != nil {
			return fmt.Errorf("batch: restore accounts: %w", err)
		}
	}

	if len(state.Pools) > 0 {
		if err := m.pools.LoadSnapshot(state.Pools); err != nil {
			return fmt.Errorf("batch: restore pools: %w", err)
		}
	}

	return nil
}
