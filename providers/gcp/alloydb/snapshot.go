package alloydb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// alloydbSnapshot is the full serialized state of the GCP AlloyDB mock. The
// portable rdsdriver stores round-trip through the generic memstore helper keyed
// by resource id; the AlloyDB-native side-stores (clusterExtra/instanceExtra/
// backupExtra) and initial passwords are fully-exported value maps captured
// directly. The wired *config.Options and monitoring backend are intentionally
// not serialized.
type alloydbSnapshot struct {
	Clusters         json.RawMessage          `json:"clusters,omitempty"`
	Instances        json.RawMessage          `json:"instances,omitempty"`
	Backups          json.RawMessage          `json:"backups,omitempty"`
	Databases        json.RawMessage          `json:"databases,omitempty"`
	Users            json.RawMessage          `json:"users,omitempty"`
	ClusterExtra     map[string]clusterExtra  `json:"clusterExtra,omitempty"`
	InstanceExtra    map[string]instanceExtra `json:"instanceExtra,omitempty"`
	BackupExtra      map[string]backupExtra   `json:"backupExtra,omitempty"`
	InitialPasswords map[string]string        `json:"initialPasswords,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// AlloyDB holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap alloydbSnapshot

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.mu.RLock()
	snap.ClusterExtra = m.clusterExtra
	snap.InstanceExtra = m.instanceExtra
	snap.BackupExtra = m.backupExtra
	snap.InitialPasswords = m.initialPasswords
	m.mu.RUnlock()

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *alloydbSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.Instances, m.instances.Snapshot},
		{&snap.Backups, m.backups.Snapshot},
		{&snap.Databases, m.databases.Snapshot},
		{&snap.Users, m.users.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("alloydb: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: cluster,
// instance, and backup ids are preserved so member/child cross-references still
// resolve after a restore.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap alloydbSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("alloydb: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.restoreExtra(&snap)

	return nil
}

func (m *Mock) restoreExtra(snap *alloydbSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if snap.ClusterExtra != nil {
		m.clusterExtra = snap.ClusterExtra
	}

	if snap.InstanceExtra != nil {
		m.instanceExtra = snap.InstanceExtra
	}

	if snap.BackupExtra != nil {
		m.backupExtra = snap.BackupExtra
	}

	if snap.InitialPasswords != nil {
		m.initialPasswords = snap.InitialPasswords
	}
}

func (m *Mock) restoreStores(snap *alloydbSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.Instances, m.instances.LoadSnapshot},
		{snap.Backups, m.backups.LoadSnapshot},
		{snap.Databases, m.databases.LoadSnapshot},
		{snap.Users, m.users.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("alloydb: restore store: %w", err)
		}
	}

	return nil
}
