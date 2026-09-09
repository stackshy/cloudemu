package bastion

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// Compile-time check that Mock is persistable.
var _ snapshot.Snapshottable = (*Mock)(nil)

// bastionSnapshot is the full serialized state of the Azure Bastion mock. The
// store holds fully-exported driver value types (generic-JSON maps included) and
// round-trips through the generic memstore helper; the mutex and *config.Options
// are intentionally not captured.
type bastionSnapshot struct {
	Hosts json.RawMessage `json:"hosts,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// the bastion mock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	hosts, err := m.hosts.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("azure bastion: snapshot hosts: %w", err)
	}

	return json.Marshal(bastionSnapshot{Hosts: hosts})
}

// Restore rebuilds the mock's state under the original identities: every
// resource-group/name key round-trips unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap bastionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("azure bastion: parse snapshot: %w", err)
	}

	if len(snap.Hosts) > 0 {
		if err := m.hosts.LoadSnapshot(snap.Hosts); err != nil {
			return fmt.Errorf("azure bastion: restore hosts: %w", err)
		}
	}

	return nil
}
