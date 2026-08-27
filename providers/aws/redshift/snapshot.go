package redshift

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// redshiftSnapshot is the full serialized state of the AWS Redshift mock. Every
// memstore store holds a fully-exported value type — rdbdriver.Cluster /
// rdbdriver.ClusterSnapshot for the shared resources, and the redshift-package
// ParameterGroup / SubnetGroup (all fields exported) for the redshift-specific
// ones — so each round-trips through the generic memstore helper keyed by its
// resource id/name. The mu-guarded tagsByARN map (ARN -> tag map) lives beside
// the stores and is captured with them. The wired deps (opts, monitoring,
// subnetResolver) and the real DatabaseEngine backing are intentionally not
// serialized: a restored cluster reports its stored endpoint, not a re-provisioned
// engine host.
type redshiftSnapshot struct {
	Clusters         json.RawMessage `json:"clusters,omitempty"`
	ClusterSnapshots json.RawMessage `json:"clusterSnapshots,omitempty"`
	ParameterGroups  json.RawMessage `json:"parameterGroups,omitempty"`
	SubnetGroups     json.RawMessage `json:"subnetGroups,omitempty"`

	TagsByARN map[string]map[string]string `json:"tagsByArn,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Redshift is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap redshiftSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.mu.RLock()
	snap.TagsByARN = m.tagsByARN
	m.mu.RUnlock()

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *redshiftSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.ClusterSnapshots, m.clusterSnapshots.Snapshot},
		{&snap.ParameterGroups, m.parameterGroups.Snapshot},
		{&snap.SubnetGroups, m.subnetGroups.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("redshift: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every cluster
// id, snapshot id, and group name (and the id/name cross-references records hold)
// is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap redshiftSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("redshift: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.mu.Lock()
	if snap.TagsByARN != nil {
		m.tagsByARN = snap.TagsByARN
	}
	m.mu.Unlock()

	return nil
}

func (m *Mock) restoreStores(snap *redshiftSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.ClusterSnapshots, m.clusterSnapshots.LoadSnapshot},
		{snap.ParameterGroups, m.parameterGroups.LoadSnapshot},
		{snap.SubnetGroups, m.subnetGroups.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("redshift: restore store: %w", err)
		}
	}

	return nil
}
