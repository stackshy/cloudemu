package route53

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// route53Snapshot is the full serialized state of the Route 53 mock: every
// store keyed by its resource identity (hosted zone ids, record keys, health
// check ids), plus the ChangeResourceTags tag map keyed by resource id. Zone
// values carry their nested VPC associations, so private-zone associations
// round-trip with the zone. The driver-typed stores dump through the generic
// memstore helper, which preserves each key unchanged.
type route53Snapshot struct {
	Zones        json.RawMessage              `json:"zones,omitempty"`
	Records      json.RawMessage              `json:"records,omitempty"`
	HealthChecks json.RawMessage              `json:"healthChecks,omitempty"`
	TagsByID     map[string]map[string]string `json:"tagsById,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Route 53 holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap route53Snapshot

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
			return nil, fmt.Errorf("route53: snapshot store: %w", err)
		}

		*d.dst = b
	}

	snap.TagsByID = m.snapshotTags()

	return json.Marshal(snap)
}

// snapshotTags copies the ChangeResourceTags map under its lock so a restore
// reinstates each resource's tags under the same resource id.
func (m *Mock) snapshotTags() map[string]map[string]string {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	if len(m.tagsByID) == 0 {
		return nil
	}

	out := make(map[string]map[string]string, len(m.tagsByID))

	for id, tags := range m.tagsByID {
		cp := make(map[string]string, len(tags))
		for k, v := range tags {
			cp[k] = v
		}

		out[id] = cp
	}

	return out
}

// Restore rebuilds the mock's state under the original identities: hosted zone
// ids, record keys, health check ids, and their nested cross-references (VPC
// associations, health-check ids on records) survive unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap route53Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("route53: parse snapshot: %w", err)
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
			return fmt.Errorf("route53: restore store: %w", err)
		}
	}

	m.restoreTags(snap.TagsByID)

	return nil
}

// restoreTags reinstates the ChangeResourceTags map under its lock.
func (m *Mock) restoreTags(tagsByID map[string]map[string]string) {
	if tagsByID == nil {
		return
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	m.tagsByID = tagsByID
}
