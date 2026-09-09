package recoveryservices

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotState is the on-disk shape: every store keyed by its (lowercased)
// resource id.
type snapshotState struct {
	Vaults   json.RawMessage `json:"vaults,omitempty"`
	Policies json.RawMessage `json:"policies,omitempty"`
	Configs  json.RawMessage `json:"configs,omitempty"`
}

// Snapshot captures every vault, backup policy and backup vault/storage config.
// includeAssets is unused — these resources hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vaults, err := m.vaults.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("recoveryservices: snapshot vaults: %w", err)
	}

	policies, err := m.policies.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("recoveryservices: snapshot policies: %w", err)
	}

	configs, err := m.configs.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("recoveryservices: snapshot configs: %w", err)
	}

	data, err := json.Marshal(snapshotState{Vaults: vaults, Policies: policies, Configs: configs})
	if err != nil {
		return nil, fmt.Errorf("recoveryservices: marshal snapshot: %w", err)
	}

	return data, nil
}

// Restore rebuilds every vault, policy and config under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("recoveryservices: unmarshal snapshot: %w", err)
	}

	for _, step := range []struct {
		name string
		data json.RawMessage
		load func([]byte) error
	}{
		{"vaults", state.Vaults, m.vaults.LoadSnapshot},
		{"policies", state.Policies, m.policies.LoadSnapshot},
		{"configs", state.Configs, m.configs.LoadSnapshot},
	} {
		if len(step.data) == 0 {
			continue
		}

		if err := step.load(step.data); err != nil {
			return fmt.Errorf("recoveryservices: restore %s: %w", step.name, err)
		}
	}

	return nil
}
