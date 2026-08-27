package managedcassandra

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// mcSnapshot is the full serialized state of the Azure Managed Cassandra mock.
// Both stores hold fully-exported mcdriver value types, so each round-trips
// through the generic memstore helper keyed by its composite ARM key ("rg/name"
// for clusters, "rg/cluster/name" for data centers). The wired opts are not
// serialized.
type mcSnapshot struct {
	Clusters    json.RawMessage `json:"clusters,omitempty"`
	DataCenters json.RawMessage `json:"dataCenters,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Managed Cassandra holds resource metadata, not bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap mcSnapshot

	clusters, err := m.clusters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("managedcassandra: snapshot clusters: %w", err)
	}

	dcs, err := m.dataCenters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("managedcassandra: snapshot dataCenters: %w", err)
	}

	snap.Clusters = clusters
	snap.DataCenters = dcs

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every cluster
// and data-center composite key is preserved so a client's identifiers still
// resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap mcSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("managedcassandra: parse snapshot: %w", err)
	}

	if len(snap.Clusters) > 0 {
		if err := m.clusters.LoadSnapshot(snap.Clusters); err != nil {
			return fmt.Errorf("managedcassandra: restore clusters: %w", err)
		}
	}

	if len(snap.DataCenters) > 0 {
		if err := m.dataCenters.LoadSnapshot(snap.DataCenters); err != nil {
			return fmt.Errorf("managedcassandra: restore dataCenters: %w", err)
		}
	}

	return nil
}
