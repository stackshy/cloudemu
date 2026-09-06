package azurefirewall

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// Compile-time check that Mock is persistable.
var _ snapshot.Snapshottable = (*Mock)(nil)

// fwSnapshot is the full serialized state of the Azure Firewall mock. Both stores
// hold fully-exported driver value types (generic-JSON maps included) and
// round-trip through the generic memstore helper; the mutex and *config.Options
// are intentionally not captured.
type fwSnapshot struct {
	Firewalls json.RawMessage `json:"firewalls,omitempty"`
	Policies  json.RawMessage `json:"policies,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// the firewall mock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	fw, err := m.firewalls.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("azure firewall: snapshot firewalls: %w", err)
	}

	pol, err := m.policies.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("azure firewall: snapshot policies: %w", err)
	}

	return json.Marshal(fwSnapshot{Firewalls: fw, Policies: pol})
}

// Restore rebuilds the mock's state under the original identities: every
// resource-group/name key round-trips unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap fwSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("azure firewall: parse snapshot: %w", err)
	}

	if len(snap.Firewalls) > 0 {
		if err := m.firewalls.LoadSnapshot(snap.Firewalls); err != nil {
			return fmt.Errorf("azure firewall: restore firewalls: %w", err)
		}
	}

	if len(snap.Policies) > 0 {
		if err := m.policies.LoadSnapshot(snap.Policies); err != nil {
			return fmt.Errorf("azure firewall: restore policies: %w", err)
		}
	}

	return nil
}
