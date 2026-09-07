package fis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// fisSnapshot is the full serialized state of the FIS mock. The stores hold
// exported driver types, so they serialize directly, keyed by template id and by
// experiment id. The wired opts are not serialized.
type fisSnapshot struct {
	Templates   map[string]driver.ExperimentTemplate `json:"templates,omitempty"`
	Experiments map[string]driver.Experiment         `json:"experiments,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// FIS is control-plane only and holds no bulk assets.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := fisSnapshot{}

	if m.templates.Len() > 0 {
		snap.Templates = m.templates.All()
	}

	if m.experiments.Len() > 0 {
		snap.Experiments = m.experiments.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("fis: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every template
// and experiment id (and the ARN and computed fields derived from them) is
// preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap fisSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("fis: parse snapshot: %w", err)
	}

	for id := range snap.Templates {
		m.templates.Set(id, snap.Templates[id])
	}

	for id := range snap.Experiments {
		m.experiments.Set(id, snap.Experiments[id])
	}

	return nil
}
