package sql

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// sqlSnapshot is the full serialized state of the Azure SQL mock. Every store
// holds a fully-exported rdsdriver value, so each round-trips through the
// generic memstore helper keyed by its resource id (server name, "server/db",
// snapshot id, …) — cross-references survive because the keys are preserved. The
// mutex and the wired options/monitoring are intentionally not serialized.
type sqlSnapshot struct {
	Clusters         json.RawMessage `json:"clusters,omitempty"`
	Instances        json.RawMessage `json:"instances,omitempty"`
	Snapshots        json.RawMessage `json:"snapshots,omitempty"`
	FirewallRules    json.RawMessage `json:"firewallRules,omitempty"`
	VNetRules        json.RawMessage `json:"vnetRules,omitempty"`
	ElasticPools     json.RawMessage `json:"elasticPools,omitempty"`
	FailoverGroups   json.RawMessage `json:"failoverGroups,omitempty"`
	AADAdmins        json.RawMessage `json:"aadAdmins,omitempty"`
	ManagedInstances json.RawMessage `json:"managedInstances,omitempty"`
	ManagedDatabases json.RawMessage `json:"managedDatabases,omitempty"`
	Databases        json.RawMessage `json:"databases,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Azure SQL holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap sqlSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *sqlSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.Instances, m.instances.Snapshot},
		{&snap.Snapshots, m.snapshots.Snapshot},
		{&snap.FirewallRules, m.firewallRules.Snapshot},
		{&snap.VNetRules, m.vnetRules.Snapshot},
		{&snap.ElasticPools, m.elasticPools.Snapshot},
		{&snap.FailoverGroups, m.failoverGroups.Snapshot},
		{&snap.AADAdmins, m.aadAdmins.Snapshot},
		{&snap.ManagedInstances, m.managedInstances.Snapshot},
		{&snap.ManagedDatabases, m.managedDatabases.Snapshot},
		{&snap.Databases, m.databases.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("sql: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds every store under its original keys, so a restored database's
// "server/name" identity and its server cross-reference still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap sqlSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("sql: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.Instances, m.instances.LoadSnapshot},
		{snap.Snapshots, m.snapshots.LoadSnapshot},
		{snap.FirewallRules, m.firewallRules.LoadSnapshot},
		{snap.VNetRules, m.vnetRules.LoadSnapshot},
		{snap.ElasticPools, m.elasticPools.LoadSnapshot},
		{snap.FailoverGroups, m.failoverGroups.LoadSnapshot},
		{snap.AADAdmins, m.aadAdmins.LoadSnapshot},
		{snap.ManagedInstances, m.managedInstances.LoadSnapshot},
		{snap.ManagedDatabases, m.managedDatabases.LoadSnapshot},
		{snap.Databases, m.databases.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("sql: restore store: %w", err)
		}
	}

	return nil
}
