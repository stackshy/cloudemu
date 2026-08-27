package databricks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// databricksSnapshot is the full serialized state of the Databricks mock. Every
// store's value type is fully exported (driver.*), so each round-trips through
// the generic memstore helper keyed by its resource id. The job/run sequence
// counters are captured so ids minted after a restore do not collide with
// existing ones. The wired *config.Options is intentionally not serialized.
type databricksSnapshot struct {
	Workspaces       json.RawMessage `json:"workspaces,omitempty"`
	Pools            json.RawMessage `json:"pools,omitempty"`
	Clusters         json.RawMessage `json:"clusters,omitempty"`
	Jobs             json.RawMessage `json:"jobs,omitempty"`
	Runs             json.RawMessage `json:"runs,omitempty"`
	Policies         json.RawMessage `json:"policies,omitempty"`
	Libraries        json.RawMessage `json:"libraries,omitempty"`
	Permissions      json.RawMessage `json:"permissions,omitempty"`
	AccessConnectors json.RawMessage `json:"accessConnectors,omitempty"`
	PrivateEndpoints json.RawMessage `json:"privateEndpoints,omitempty"`
	VNetPeerings     json.RawMessage `json:"vnetPeerings,omitempty"`
	JobSeq           int64           `json:"jobSeq,omitempty"`
	RunSeq           int64           `json:"runSeq,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Databricks holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap databricksSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	snap.JobSeq = m.jobSeq.Load()
	snap.RunSeq = m.runSeq.Load()

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *databricksSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Workspaces, m.workspaces.Snapshot},
		{&snap.Pools, m.pools.Snapshot},
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.Jobs, m.jobs.Snapshot},
		{&snap.Runs, m.runs.Snapshot},
		{&snap.Policies, m.policies.Snapshot},
		{&snap.Libraries, m.libraries.Snapshot},
		{&snap.Permissions, m.permissions.Snapshot},
		{&snap.AccessConnectors, m.accessConnectors.Snapshot},
		{&snap.PrivateEndpoints, m.privateEndpoints.Snapshot},
		{&snap.VNetPeerings, m.vnetPeerings.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("databricks: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every
// workspace/cluster/job id is preserved so cross-references still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap databricksSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("databricks: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.jobSeq.Store(snap.JobSeq)
	m.runSeq.Store(snap.RunSeq)

	return nil
}

func (m *Mock) restoreStores(snap *databricksSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Workspaces, m.workspaces.LoadSnapshot},
		{snap.Pools, m.pools.LoadSnapshot},
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.Jobs, m.jobs.LoadSnapshot},
		{snap.Runs, m.runs.LoadSnapshot},
		{snap.Policies, m.policies.LoadSnapshot},
		{snap.Libraries, m.libraries.LoadSnapshot},
		{snap.Permissions, m.permissions.LoadSnapshot},
		{snap.AccessConnectors, m.accessConnectors.LoadSnapshot},
		{snap.PrivateEndpoints, m.privateEndpoints.LoadSnapshot},
		{snap.VNetPeerings, m.vnetPeerings.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("databricks: restore store: %w", err)
		}
	}

	return nil
}
