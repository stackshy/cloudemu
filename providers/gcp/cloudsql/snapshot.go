package cloudsql

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cloudsqlSnapshot is the full serialized state of the GCP Cloud SQL mock. Every
// stored value type is fully exported, so the stores round-trip through the
// generic memstore helper keyed by resource id; the mu-guarded backup sequence
// and per-instance root passwords are captured alongside. The wired
// *config.Options and monitoring backend are intentionally not serialized.
type cloudsqlSnapshot struct {
	Instances     json.RawMessage   `json:"instances,omitempty"`
	Snapshots     json.RawMessage   `json:"snapshots,omitempty"`
	Databases     json.RawMessage   `json:"databases,omitempty"`
	Users         json.RawMessage   `json:"users,omitempty"`
	SSLCerts      json.RawMessage   `json:"sslCerts,omitempty"`
	BackupSeq     int64             `json:"backupSeq,omitempty"`
	RootPasswords map[string]string `json:"rootPasswords,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Cloud SQL holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap cloudsqlSnapshot

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.mu.RLock()
	snap.BackupSeq = m.backupSeq
	snap.RootPasswords = m.rootPasswords
	m.mu.RUnlock()

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *cloudsqlSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Instances, m.instances.Snapshot},
		{&snap.Snapshots, m.snapshots.Snapshot},
		{&snap.Databases, m.databases.Snapshot},
		{&snap.Users, m.users.Snapshot},
		{&snap.SSLCerts, m.sslCerts.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("cloudsql: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every
// instance/database/user/snapshot id is preserved so cross-references still
// resolve after a restore.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cloudsqlSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudsql: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.mu.Lock()
	m.backupSeq = snap.BackupSeq

	if snap.RootPasswords != nil {
		m.rootPasswords = snap.RootPasswords
	}
	m.mu.Unlock()

	return nil
}

func (m *Mock) restoreStores(snap *cloudsqlSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Instances, m.instances.LoadSnapshot},
		{snap.Snapshots, m.snapshots.LoadSnapshot},
		{snap.Databases, m.databases.LoadSnapshot},
		{snap.Users, m.users.LoadSnapshot},
		{snap.SSLCerts, m.sslCerts.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("cloudsql: restore store: %w", err)
		}
	}

	return nil
}
