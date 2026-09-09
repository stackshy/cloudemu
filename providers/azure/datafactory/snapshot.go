package datafactory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// Compile-time check that Mock is persistable.
var _ snapshot.Snapshottable = (*Mock)(nil)

// dfSnapshot is the full serialized state of the Data Factory mock. The store
// holds fully-exported driver value types (identity, generic-JSON maps included)
// and round-trips through the generic memstore helper; the *config.Options is
// intentionally not captured.
type dfSnapshot struct {
	Factories json.RawMessage `json:"factories,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// the factory mock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	f, err := m.factories.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("azure data factory: snapshot factories: %w", err)
	}

	return json.Marshal(dfSnapshot{Factories: f})
}

// Restore rebuilds the mock's state under the original identities: every
// resource-group/name key round-trips unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap dfSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("azure data factory: parse snapshot: %w", err)
	}

	if len(snap.Factories) > 0 {
		if err := m.factories.LoadSnapshot(snap.Factories); err != nil {
			return fmt.Errorf("azure data factory: restore factories: %w", err)
		}
	}

	return nil
}
