package mwaa

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// mwaaSnapshot is the full serialized state of the MWAA mock. The store holds
// exported driver types, so it serializes directly, keyed by environment name.
// The wired opts are not serialized.
type mwaaSnapshot struct {
	Environments map[string]driver.Environment `json:"environments,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// MWAA is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := mwaaSnapshot{}

	if m.envs.Len() > 0 {
		snap.Environments = m.envs.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("mwaa: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every
// environment name (and the ARN and computed fields derived from it) is
// preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap mwaaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("mwaa: parse snapshot: %w", err)
	}

	for name := range snap.Environments {
		m.envs.Set(name, snap.Environments[name])
	}

	return nil
}
