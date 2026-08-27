package clouddns

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// dnsSnapshot is the full serialized state of the Cloud DNS mock: every store
// keyed by its resource identity (managed zone ids, record keys, health check
// ids). Zone values carry their nested state (DNSSEC config, visibility
// networks), so they round-trip with the zone. The driver-typed stores dump
// through the generic memstore helper, which preserves each key unchanged.
type dnsSnapshot struct {
	Zones        json.RawMessage `json:"zones,omitempty"`
	Records      json.RawMessage `json:"records,omitempty"`
	HealthChecks json.RawMessage `json:"healthChecks,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Cloud DNS holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap dnsSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Zones, m.zones.Snapshot},
		{&snap.Records, m.records.Snapshot},
		{&snap.HealthChecks, m.healthChecks.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("clouddns: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: managed zone
// ids, record keys, health check ids, and their nested cross-references
// (DNSSEC config, visibility networks) survive unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap dnsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("clouddns: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Zones, m.zones.LoadSnapshot},
		{snap.Records, m.records.LoadSnapshot},
		{snap.HealthChecks, m.healthChecks.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("clouddns: restore store: %w", err)
		}
	}

	return nil
}
