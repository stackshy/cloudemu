package globalaccelerator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// gaSnapshot is the full serialized state of the Global Accelerator mock. The
// stores hold exported driver types, so they serialize directly: accelerators,
// listeners and endpoint groups keyed by their ARN, and attributes keyed by
// accelerator ARN. The wired opts are not serialized.
type gaSnapshot struct {
	Accelerators   map[string]driver.Accelerator           `json:"accelerators,omitempty"`
	Listeners      map[string]driver.Listener              `json:"listeners,omitempty"`
	EndpointGroups map[string]driver.EndpointGroup         `json:"endpointGroups,omitempty"`
	Attributes     map[string]driver.AcceleratorAttributes `json:"attributes,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Global Accelerator is control-plane only and holds no bulk assets.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := gaSnapshot{}

	if m.accelerators.Len() > 0 {
		snap.Accelerators = m.accelerators.All()
	}

	if m.listeners.Len() > 0 {
		snap.Listeners = m.listeners.All()
	}

	if m.endpointGroups.Len() > 0 {
		snap.EndpointGroups = m.endpointGroups.All()
	}

	if m.attributes.Len() > 0 {
		snap.Attributes = m.attributes.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("globalaccelerator: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every ARN (and
// the computed fields derived from it) is preserved, so a restore is transparent
// to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap gaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("globalaccelerator: parse snapshot: %w", err)
	}

	for k := range snap.Accelerators {
		m.accelerators.Set(k, snap.Accelerators[k])
	}

	for k := range snap.Listeners {
		m.listeners.Set(k, snap.Listeners[k])
	}

	for k := range snap.EndpointGroups {
		m.endpointGroups.Set(k, snap.EndpointGroups[k])
	}

	for k := range snap.Attributes {
		m.attributes.Set(k, snap.Attributes[k])
	}

	return nil
}
