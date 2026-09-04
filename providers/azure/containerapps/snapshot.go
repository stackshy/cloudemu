package containerapps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// caSnapshot is the full serialized state of the Container Apps mock: every
// managed environment and container app keyed by its (lowercased) ARM id. Both
// record types are fully exported and round-trip through the generic memstore
// helper, so a stop/start preserves the minted domain/fqdn/revision values.
type caSnapshot struct {
	Environments json.RawMessage `json:"environments,omitempty"`
	Apps         json.RawMessage `json:"apps,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Container Apps hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	envs, err := m.envs.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("containerapps: snapshot environments: %w", err)
	}

	apps, err := m.apps.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("containerapps: snapshot apps: %w", err)
	}

	return json.Marshal(caSnapshot{Environments: envs, Apps: apps})
}

// Restore rebuilds every environment and container app under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap caSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("containerapps: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(snap.Environments) > 0 {
		if err := m.envs.LoadSnapshot(snap.Environments); err != nil {
			return fmt.Errorf("containerapps: restore environments: %w", err)
		}
	}

	if len(snap.Apps) > 0 {
		if err := m.apps.LoadSnapshot(snap.Apps); err != nil {
			return fmt.Errorf("containerapps: restore apps: %w", err)
		}
	}

	return nil
}
