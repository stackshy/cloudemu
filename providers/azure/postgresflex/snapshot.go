package postgresflex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// pgFlexSnapshot is the full serialized state of the Postgres Flexible Server
// mock. Every store holds a fully-exported rdsdriver value, so each round-trips
// through the generic memstore helper keyed by its resource id — cross-references
// (a database/firewall rule's server key) survive. The mutex and the wired
// options/monitoring are intentionally not serialized.
type pgFlexSnapshot struct {
	Instances      json.RawMessage `json:"instances,omitempty"`
	Snapshots      json.RawMessage `json:"snapshots,omitempty"`
	Databases      json.RawMessage `json:"databases,omitempty"`
	FirewallRules  json.RawMessage `json:"firewallRules,omitempty"`
	Configurations json.RawMessage `json:"configurations,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Postgres Flex holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap pgFlexSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *pgFlexSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Instances, m.instances.Snapshot},
		{&snap.Snapshots, m.snapshots.Snapshot},
		{&snap.Databases, m.databases.Snapshot},
		{&snap.FirewallRules, m.firewallRules.Snapshot},
		{&snap.Configurations, m.configurations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("postgresflex: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds every store under its original keys, so a restored database's
// identity and its server cross-reference still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap pgFlexSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("postgresflex: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Instances, m.instances.LoadSnapshot},
		{snap.Snapshots, m.snapshots.LoadSnapshot},
		{snap.Databases, m.databases.LoadSnapshot},
		{snap.FirewallRules, m.firewallRules.LoadSnapshot},
		{snap.Configurations, m.configurations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("postgresflex: restore store: %w", err)
		}
	}

	return nil
}
