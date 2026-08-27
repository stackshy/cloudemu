package memorydb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// memorydbSnapshot is the full serialized state of the AWS MemoryDB mock. Every
// memstore store holds a fully-exported mdbdriver type (Cluster, ACL, User,
// ParameterGroup, SubnetGroup, Snapshot, MultiRegionCluster, ReservedNode), so
// each round-trips through the generic memstore helper keyed by its resource
// name. The mu-guarded state that lives beside the stores — the parameter-group
// overrides, the ARN-keyed tag maps, and the append-only event log — is captured
// alongside. The wired deps (opts, monitoring) are intentionally not serialized:
// a restore reinstates records, not the CloudWatch backend they emit into.
type memorydbSnapshot struct {
	Clusters        json.RawMessage `json:"clusters,omitempty"`
	ACLs            json.RawMessage `json:"acls,omitempty"`
	Users           json.RawMessage `json:"users,omitempty"`
	ParameterGroups json.RawMessage `json:"parameterGroups,omitempty"`
	SubnetGroups    json.RawMessage `json:"subnetGroups,omitempty"`
	Snapshots       json.RawMessage `json:"snapshots,omitempty"`
	MultiRegion     json.RawMessage `json:"multiRegion,omitempty"`
	ReservedNodes   json.RawMessage `json:"reservedNodes,omitempty"`

	ParamOverrides map[string]map[string]string `json:"paramOverrides,omitempty"`
	Tags           map[string]map[string]string `json:"tags,omitempty"`
	Events         []mdbdriver.Event            `json:"events,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// MemoryDB is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap memorydbSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.snapshotScalarState(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *memorydbSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.ACLs, m.acls.Snapshot},
		{&snap.Users, m.users.Snapshot},
		{&snap.ParameterGroups, m.parameterGroups.Snapshot},
		{&snap.SubnetGroups, m.subnetGroups.Snapshot},
		{&snap.Snapshots, m.snapshots.Snapshot},
		{&snap.MultiRegion, m.multiRegion.Snapshot},
		{&snap.ReservedNodes, m.reservedNodes.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("memorydb: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// snapshotScalarState captures the mu-guarded maps and event log beside the
// stores.
func (m *Mock) snapshotScalarState(snap *memorydbSnapshot) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap.ParamOverrides = m.paramOverrides
	snap.Tags = m.tags

	if len(m.events) > 0 {
		snap.Events = append([]mdbdriver.Event(nil), m.events...)
	}
}

// Restore rebuilds the mock's state under the original identities: every cluster,
// ACL, user, and group name (and the name-string cross-references records hold)
// is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap memorydbSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("memorydb: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.restoreScalarState(&snap)

	return nil
}

func (m *Mock) restoreStores(snap *memorydbSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.ACLs, m.acls.LoadSnapshot},
		{snap.Users, m.users.LoadSnapshot},
		{snap.ParameterGroups, m.parameterGroups.LoadSnapshot},
		{snap.SubnetGroups, m.subnetGroups.LoadSnapshot},
		{snap.Snapshots, m.snapshots.LoadSnapshot},
		{snap.MultiRegion, m.multiRegion.LoadSnapshot},
		{snap.ReservedNodes, m.reservedNodes.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("memorydb: restore store: %w", err)
		}
	}

	return nil
}

// restoreScalarState reinstates the mu-guarded maps and event log, leaving unset
// ones at their New() defaults.
func (m *Mock) restoreScalarState(snap *memorydbSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if snap.ParamOverrides != nil {
		m.paramOverrides = snap.ParamOverrides
	}

	if snap.Tags != nil {
		m.tags = snap.Tags
	}

	if snap.Events != nil {
		m.events = snap.Events
	}
}
