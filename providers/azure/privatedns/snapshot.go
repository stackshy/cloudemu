package privatedns

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// Compile-time check that Mock is persistable.
var _ snapshot.Snapshottable = (*Mock)(nil)

// pdnsSnapshot is the full serialized state of the Private DNS mock. All three
// stores hold fully-exported driver value types (generic-JSON maps and pointer
// scalars included) and round-trip through the generic memstore helper; the
// mutex and *config.Options are intentionally not captured.
type pdnsSnapshot struct {
	Zones   json.RawMessage `json:"zones,omitempty"`
	Links   json.RawMessage `json:"links,omitempty"`
	Records json.RawMessage `json:"records,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// the Private DNS mock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	zones, err := m.zones.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("private dns: snapshot zones: %w", err)
	}

	links, err := m.links.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("private dns: snapshot links: %w", err)
	}

	records, err := m.records.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("private dns: snapshot records: %w", err)
	}

	return json.Marshal(pdnsSnapshot{Zones: zones, Links: links, Records: records})
}

// Restore rebuilds the mock's state under the original identities: every zone,
// link and record key round-trips unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap pdnsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("private dns: parse snapshot: %w", err)
	}

	if len(snap.Zones) > 0 {
		if err := m.zones.LoadSnapshot(snap.Zones); err != nil {
			return fmt.Errorf("private dns: restore zones: %w", err)
		}
	}

	if len(snap.Links) > 0 {
		if err := m.links.LoadSnapshot(snap.Links); err != nil {
			return fmt.Errorf("private dns: restore links: %w", err)
		}
	}

	if len(snap.Records) > 0 {
		if err := m.records.LoadSnapshot(snap.Records); err != nil {
			return fmt.Errorf("private dns: restore records: %w", err)
		}
	}

	return nil
}
