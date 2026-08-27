package efs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// efsSnapshot is the full serialized state of the EFS mock. File systems carry
// an exported form (the stored fsData has a mutex that json.Marshal cannot see);
// the id-index stores round-trip through the generic memstore helper so a mount
// target or access-point id still resolves to its owning file system after a
// restore. The mutexes, the subnet resolver, and the wired *config.Options are
// intentionally not serialized.
type efsSnapshot struct {
	FileSystems  map[string]*fsDataSnapshot `json:"fileSystems,omitempty"`
	MTIndex      json.RawMessage            `json:"mtIndex,omitempty"`
	APIndex      json.RawMessage            `json:"apIndex,omitempty"`
	TokenIndex   json.RawMessage            `json:"tokenIndex,omitempty"`
	APTokenIndex json.RawMessage            `json:"apTokenIndex,omitempty"`
	AccountPref  string                     `json:"accountPref,omitempty"`
}

// fsDataSnapshot mirrors fsData, promoting its fields to exported ones so they
// survive JSON. The per-file-system mutex is deliberately excluded.
type fsDataSnapshot struct {
	FS          driver.FileSystem                `json:"fs"`
	Policy      string                           `json:"policy,omitempty"`
	Backup      string                           `json:"backup,omitempty"`
	MountTgts   map[string]*driver.MountTarget   `json:"mountTgts,omitempty"`
	AccessPts   map[string]*driver.AccessPoint   `json:"accessPts,omitempty"`
	Lifecycle   []driver.LifecyclePolicy         `json:"lifecycle,omitempty"`
	Replication *driver.ReplicationConfiguration `json:"replication,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// EFS holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := efsSnapshot{FileSystems: m.snapshotFileSystems()}

	if err := m.snapshotIndexes(&snap); err != nil {
		return nil, err
	}

	m.prefMu.RLock()
	snap.AccountPref = m.accountPref
	m.prefMu.RUnlock()

	return json.Marshal(snap)
}

// snapshotFileSystems promotes each stored fsData to its exported snapshot form.
func (m *Mock) snapshotFileSystems() map[string]*fsDataSnapshot {
	if m.fileSystems.Len() == 0 {
		return nil
	}

	out := make(map[string]*fsDataSnapshot, m.fileSystems.Len())

	for id, d := range m.fileSystems.All() {
		d.mu.RLock()
		out[id] = &fsDataSnapshot{
			FS: d.fs, Policy: d.policy, Backup: d.backup, MountTgts: d.mountTgts,
			AccessPts: d.accessPts, Lifecycle: d.lifecycle, Replication: d.replication,
		}
		d.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotIndexes(snap *efsSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.MTIndex, m.mtIndex.Snapshot},
		{&snap.APIndex, m.apIndex.Snapshot},
		{&snap.TokenIndex, m.tokenIndex.Snapshot},
		{&snap.APTokenIndex, m.apTokenIndex.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("efs: snapshot index: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every file
// system, mount target, and access point is reinstated under its stored id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap efsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("efs: parse snapshot: %w", err)
	}

	for id, s := range snap.FileSystems {
		m.fileSystems.Set(id, &fsData{
			fs: s.FS, policy: s.Policy, backup: s.Backup, mountTgts: s.MountTgts,
			accessPts: s.AccessPts, lifecycle: s.Lifecycle, replication: s.Replication,
		})
	}

	if err := m.restoreIndexes(&snap); err != nil {
		return err
	}

	if snap.AccountPref != "" {
		m.prefMu.Lock()
		m.accountPref = snap.AccountPref
		m.prefMu.Unlock()
	}

	return nil
}

func (m *Mock) restoreIndexes(snap *efsSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.MTIndex, m.mtIndex.LoadSnapshot},
		{snap.APIndex, m.apIndex.LoadSnapshot},
		{snap.TokenIndex, m.tokenIndex.LoadSnapshot},
		{snap.APTokenIndex, m.apTokenIndex.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("efs: restore index: %w", err)
		}
	}

	return nil
}
