package binaryauthorization

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// binaryAuthorizationSnapshot is the full serialized state of the Binary
// Authorization mock. Both stores hold fully-exported driver value types (a
// Policy per project, an Attestor — including any stored IAM policy — per full
// resource name), so they round-trip through the generic memstore helper
// unchanged; the mutex and wired options are intentionally not serialized.
type binaryAuthorizationSnapshot struct {
	Policies  json.RawMessage `json:"policies,omitempty"`
	Attestors json.RawMessage `json:"attestors,omitempty"`
}

// Snapshot captures every policy and attestor as JSON. includeAssets is unused —
// Binary Authorization holds no object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	policies, err := m.policies.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("binaryauthorization: snapshot policies: %w", err)
	}

	attestors, err := m.attestors.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("binaryauthorization: snapshot attestors: %w", err)
	}

	return json.Marshal(binaryAuthorizationSnapshot{Policies: policies, Attestors: attestors})
}

// Restore rebuilds every policy and attestor under its original key.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap binaryAuthorizationSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("binaryauthorization: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(snap.Policies) > 0 {
		if err := m.policies.LoadSnapshot(snap.Policies); err != nil {
			return fmt.Errorf("binaryauthorization: restore policies: %w", err)
		}
	}

	if len(snap.Attestors) > 0 {
		if err := m.attestors.LoadSnapshot(snap.Attestors); err != nil {
			return fmt.Errorf("binaryauthorization: restore attestors: %w", err)
		}
	}

	return nil
}
