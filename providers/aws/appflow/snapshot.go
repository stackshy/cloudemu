package appflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// appflowSnapshot is the full serialized state of the AppFlow mock. Both stores
// hold exported driver types, so they serialize directly, keyed by flow name and
// connector-profile name respectively. The wired opts are not serialized.
type appflowSnapshot struct {
	Flows    map[string]driver.Flow             `json:"flows,omitempty"`
	Profiles map[string]driver.ConnectorProfile `json:"connectorProfiles,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// AppFlow is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := appflowSnapshot{}

	if m.flows.Len() > 0 {
		snap.Flows = m.flows.All()
	}

	if m.profiles.Len() > 0 {
		snap.Profiles = m.profiles.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("appflow: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every flow
// name and connector-profile name (and the ARNs derived from them) is preserved,
// so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap appflowSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("appflow: parse snapshot: %w", err)
	}

	for name := range snap.Flows {
		m.flows.Set(name, snap.Flows[name])
	}

	for name := range snap.Profiles {
		m.profiles.Set(name, snap.Profiles[name])
	}

	return nil
}
