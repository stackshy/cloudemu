package cosmospostgresql

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cpgSnapshot is the full serialized state of the Cosmos DB for PostgreSQL mock.
// Every store holds a fully-exported cpgdriver value type, so each round-trips
// through the generic memstore helper keyed by its composite ARM key. The
// mu-guarded coordinatorHosts map (clusterKey -> reachable coordinator host when
// a real DatabaseEngine backs the cluster) is captured beside them. The wired
// opts are not serialized.
type cpgSnapshot struct {
	Clusters      json.RawMessage   `json:"clusters,omitempty"`
	FirewallRules json.RawMessage   `json:"firewallRules,omitempty"`
	Roles         json.RawMessage   `json:"roles,omitempty"`
	PrivateEPs    json.RawMessage   `json:"privateEps,omitempty"`
	ServerConfigs json.RawMessage   `json:"serverConfigs,omitempty"`
	Coordinators  map[string]string `json:"coordinators,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Cosmos PostgreSQL holds resource metadata, not bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap cpgSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.FirewallRules, m.firewallRules.Snapshot},
		{&snap.Roles, m.roles.Snapshot},
		{&snap.PrivateEPs, m.privateEPs.Snapshot},
		{&snap.ServerConfigs, m.serverConfigs.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("cosmospostgresql: snapshot store: %w", err)
		}

		*d.dst = b
	}

	m.mu.RLock()
	if len(m.coordinatorHosts) > 0 {
		snap.Coordinators = m.coordinatorHosts
	}
	m.mu.RUnlock()

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every cluster,
// firewall rule, role, private-endpoint, and server-configuration composite key
// is preserved so a client's identifiers still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cpgSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cosmospostgresql: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.FirewallRules, m.firewallRules.LoadSnapshot},
		{snap.Roles, m.roles.LoadSnapshot},
		{snap.PrivateEPs, m.privateEPs.LoadSnapshot},
		{snap.ServerConfigs, m.serverConfigs.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("cosmospostgresql: restore store: %w", err)
		}
	}

	if snap.Coordinators != nil {
		m.mu.Lock()
		m.coordinatorHosts = snap.Coordinators
		m.mu.Unlock()
	}

	return nil
}
