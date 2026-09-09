package grafana

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// grafanaSnapshot is the full serialized state of the Grafana mock. The store
// holds exported driver types, so it serializes directly, keyed by workspace id.
// The wired opts are not serialized.
type grafanaSnapshot struct {
	Workspaces map[string]driver.Workspace `json:"workspaces,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Grafana is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := grafanaSnapshot{}

	if m.workspaces.Len() > 0 {
		snap.Workspaces = m.workspaces.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("grafana: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every
// workspace id (and the arn and endpoint derived from it) is preserved, so a
// restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap grafanaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("grafana: parse snapshot: %w", err)
	}

	for id := range snap.Workspaces {
		m.workspaces.Set(id, snap.Workspaces[id])
	}

	return nil
}
