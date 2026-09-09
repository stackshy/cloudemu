package frontdoor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// Compile-time check that Mock is persistable.
var _ snapshot.Snapshottable = (*Mock)(nil)

// fdSnapshot is the full serialized state of the Front Door mock. All three
// stores hold fully-exported driver value types (generic-JSON maps included) and
// round-trip through the generic memstore helper; the mutex and *config.Options
// are intentionally not captured.
type fdSnapshot struct {
	Profiles     json.RawMessage `json:"profiles,omitempty"`
	Endpoints    json.RawMessage `json:"endpoints,omitempty"`
	OriginGroups json.RawMessage `json:"originGroups,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// the Front Door mock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	profiles, err := m.profiles.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("azure front door: snapshot profiles: %w", err)
	}

	endpoints, err := m.endpoints.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("azure front door: snapshot endpoints: %w", err)
	}

	originGroups, err := m.originGroups.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("azure front door: snapshot origin groups: %w", err)
	}

	return json.Marshal(fdSnapshot{Profiles: profiles, Endpoints: endpoints, OriginGroups: originGroups})
}

// Restore rebuilds the mock's state under the original identities: every
// resource-group/profile/name key round-trips unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap fdSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("azure front door: parse snapshot: %w", err)
	}

	if len(snap.Profiles) > 0 {
		if err := m.profiles.LoadSnapshot(snap.Profiles); err != nil {
			return fmt.Errorf("azure front door: restore profiles: %w", err)
		}
	}

	if len(snap.Endpoints) > 0 {
		if err := m.endpoints.LoadSnapshot(snap.Endpoints); err != nil {
			return fmt.Errorf("azure front door: restore endpoints: %w", err)
		}
	}

	if len(snap.OriginGroups) > 0 {
		if err := m.originGroups.LoadSnapshot(snap.OriginGroups); err != nil {
			return fmt.Errorf("azure front door: restore origin groups: %w", err)
		}
	}

	return nil
}
