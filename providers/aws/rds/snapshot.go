package rds

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// rdsSnapshot is the full serialized state of the AWS RDS mock. Every memstore
// store holds a fully-exported rdsdriver type, so each round-trips through the
// generic memstore helper keyed by its resource id. The mu-guarded maps carry
// state that lives beside the stores: clusterCreds (unexported value type, so
// promoted), groupTags, and rootPasswords. The in-flight settle overlays
// (instSettle/snapSettle) and the wired deps (opts, subnetResolver, monitoring)
// are intentionally not serialized — a restored record reports its stored state.
type rdsSnapshot struct {
	Instances          json.RawMessage `json:"instances,omitempty"`
	Clusters           json.RawMessage `json:"clusters,omitempty"`
	Snapshots          json.RawMessage `json:"snapshots,omitempty"`
	ClusterSnapshots   json.RawMessage `json:"clusterSnapshots,omitempty"`
	SubnetGroups       json.RawMessage `json:"subnetGroups,omitempty"`
	ParamGroups        json.RawMessage `json:"paramGroups,omitempty"`
	ClusterParamGroups json.RawMessage `json:"clusterParamGroups,omitempty"`
	OptionGroups       json.RawMessage `json:"optionGroups,omitempty"`
	Proxies            json.RawMessage `json:"proxies,omitempty"`
	EventSubs          json.RawMessage `json:"eventSubs,omitempty"`
	ClusterEndpoints   json.RawMessage `json:"clusterEndpoints,omitempty"`
	GlobalClusters     json.RawMessage `json:"globalClusters,omitempty"`

	ClusterCreds  map[string]clusterCredSnapshot `json:"clusterCreds,omitempty"`
	GroupTags     map[string]map[string]string   `json:"groupTags,omitempty"`
	RootPasswords map[string]string              `json:"rootPasswords,omitempty"`
}

// clusterCredSnapshot is the exported form of clusterCred (whose fields are
// unexported and invisible to json.Marshal).
type clusterCredSnapshot struct {
	User string `json:"user,omitempty"`
	Pass string `json:"pass,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// RDS holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap rdsSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.snapshotScalarState(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *rdsSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Instances, m.instances.Snapshot},
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.Snapshots, m.snapshots.Snapshot},
		{&snap.ClusterSnapshots, m.clusterSnapshots.Snapshot},
		{&snap.SubnetGroups, m.subnetGroups.Snapshot},
		{&snap.ParamGroups, m.paramGroups.Snapshot},
		{&snap.ClusterParamGroups, m.clusterParamGroups.Snapshot},
		{&snap.OptionGroups, m.optionGroups.Snapshot},
		{&snap.Proxies, m.proxies.Snapshot},
		{&snap.EventSubs, m.eventSubs.Snapshot},
		{&snap.ClusterEndpoints, m.clusterEndpoints.Snapshot},
		{&snap.GlobalClusters, m.globalClusters.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("rds: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// snapshotScalarState captures the mu-guarded maps that live beside the stores.
func (m *Mock) snapshotScalarState(snap *rdsSnapshot) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.clusterCreds) > 0 {
		creds := make(map[string]clusterCredSnapshot, len(m.clusterCreds))
		for id, c := range m.clusterCreds {
			creds[id] = clusterCredSnapshot{User: c.user, Pass: c.pass}
		}

		snap.ClusterCreds = creds
	}

	snap.GroupTags = m.groupTags
	snap.RootPasswords = m.rootPasswords
}

// Restore rebuilds the mock's state under the original identities: every
// instance/cluster/snapshot id (and the id-string cross-references records hold)
// is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap rdsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("rds: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.restoreScalarState(&snap)

	return nil
}

func (m *Mock) restoreStores(snap *rdsSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Instances, m.instances.LoadSnapshot},
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.Snapshots, m.snapshots.LoadSnapshot},
		{snap.ClusterSnapshots, m.clusterSnapshots.LoadSnapshot},
		{snap.SubnetGroups, m.subnetGroups.LoadSnapshot},
		{snap.ParamGroups, m.paramGroups.LoadSnapshot},
		{snap.ClusterParamGroups, m.clusterParamGroups.LoadSnapshot},
		{snap.OptionGroups, m.optionGroups.LoadSnapshot},
		{snap.Proxies, m.proxies.LoadSnapshot},
		{snap.EventSubs, m.eventSubs.LoadSnapshot},
		{snap.ClusterEndpoints, m.clusterEndpoints.LoadSnapshot},
		{snap.GlobalClusters, m.globalClusters.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("rds: restore store: %w", err)
		}
	}

	return nil
}

// restoreScalarState reinstates the mu-guarded maps, leaving unset ones at their
// New() defaults.
func (m *Mock) restoreScalarState(snap *rdsSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(snap.ClusterCreds) > 0 {
		creds := make(map[string]clusterCred, len(snap.ClusterCreds))
		for id, c := range snap.ClusterCreds {
			creds[id] = clusterCred{user: c.User, pass: c.Pass}
		}

		m.clusterCreds = creds
	}

	if snap.GroupTags != nil {
		m.groupTags = snap.GroupTags
	}

	if snap.RootPasswords != nil {
		m.rootPasswords = snap.RootPasswords
	}
}
