package frontdoor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// Compile-time check that Mock is persistable.
var _ snapshot.Snapshottable = (*Mock)(nil)

// fdSnapshot is the full serialized state of the Front Door mock. All five
// stores hold fully-exported driver value types (generic-JSON maps included) and
// round-trip through the generic memstore helper; the mutex and *config.Options
// are intentionally not captured.
type fdSnapshot struct {
	Profiles     json.RawMessage `json:"profiles,omitempty"`
	Endpoints    json.RawMessage `json:"endpoints,omitempty"`
	OriginGroups json.RawMessage `json:"originGroups,omitempty"`
	Origins      json.RawMessage `json:"origins,omitempty"`
	Routes       json.RawMessage `json:"routes,omitempty"`
}

// snapshotter is the memstore surface Snapshot/Restore need, so every store can
// be walked in one table regardless of its value type.
type snapshotter interface {
	Snapshot() ([]byte, error)
	LoadSnapshot(data []byte) error
}

// storeSlot pairs a store with its label and its field in fdSnapshot.
type storeSlot struct {
	label string
	store snapshotter
	field *json.RawMessage
}

func (m *Mock) slots(snap *fdSnapshot) []storeSlot {
	return []storeSlot{
		{"profiles", m.profiles, &snap.Profiles},
		{"endpoints", m.endpoints, &snap.Endpoints},
		{"origin groups", m.originGroups, &snap.OriginGroups},
		{"origins", m.origins, &snap.Origins},
		{"routes", m.routes, &snap.Routes},
	}
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused:
// the Front Door mock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap fdSnapshot

	for _, s := range m.slots(&snap) {
		raw, err := s.store.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("azure front door: snapshot %s: %w", s.label, err)
		}

		*s.field = raw
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every
// resource-group/profile/parent/name key round-trips unchanged. A snapshot taken
// before origins and routes existed simply has no entries for them.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap fdSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("azure front door: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.slots(&snap) {
		if len(*s.field) == 0 {
			continue
		}

		if err := s.store.LoadSnapshot(*s.field); err != nil {
			return fmt.Errorf("azure front door: restore %s: %w", s.label, err)
		}
	}

	return nil
}
