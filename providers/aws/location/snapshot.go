package location

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// locationSnapshot is the full serialized state of the Location mock. The stores
// hold exported driver types, so they serialize directly, keyed by resource
// name. The wired opts are not serialized.
type locationSnapshot struct {
	Maps        map[string]driver.MapInfo                `json:"maps,omitempty"`
	Indexes     map[string]driver.PlaceIndexInfo         `json:"indexes,omitempty"`
	Calculators map[string]driver.RouteCalculatorInfo    `json:"calculators,omitempty"`
	Collections map[string]driver.GeofenceCollectionInfo `json:"collections,omitempty"`
	Trackers    map[string]driver.TrackerInfo            `json:"trackers,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Location is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := locationSnapshot{}

	if m.maps.Len() > 0 {
		snap.Maps = m.maps.All()
	}

	if m.indexes.Len() > 0 {
		snap.Indexes = m.indexes.All()
	}

	if m.calculators.Len() > 0 {
		snap.Calculators = m.calculators.All()
	}

	if m.collections.Len() > 0 {
		snap.Collections = m.collections.All()
	}

	if m.trackers.Len() > 0 {
		snap.Trackers = m.trackers.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("location: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every
// resource name (and the ARN, timestamps and tags stored with it) is preserved,
// so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap locationSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("location: parse snapshot: %w", err)
	}

	for name := range snap.Maps {
		m.maps.Set(name, snap.Maps[name])
	}

	for name := range snap.Indexes {
		m.indexes.Set(name, snap.Indexes[name])
	}

	for name := range snap.Calculators {
		m.calculators.Set(name, snap.Calculators[name])
	}

	for name := range snap.Collections {
		m.collections.Set(name, snap.Collections[name])
	}

	for name := range snap.Trackers {
		m.trackers.Set(name, snap.Trackers[name])
	}

	return nil
}
