package compute

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// computeSnapshot is the full serialized state of the OCI Compute and Block
// Volume mock. Every memstore store is dumped keyed by its resource OCID so
// cross-references (an attachment's InstanceID and VolumeID, an instance's
// SubnetID and VCNID, a pool's ConfigurationID and InstanceIDs) still resolve
// after a restore. The scopes and created side-stores are captured too, so a
// restored resource keeps its compartment and creation time, and IPCounter
// carries the synthetic private-address counter so a restore does not hand out
// addresses already in use.
type computeSnapshot struct {
	Instances   json.RawMessage `json:"instances,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
	Images      json.RawMessage `json:"images,omitempty"`
	Volumes     json.RawMessage `json:"volumes,omitempty"`
	VolAttach   json.RawMessage `json:"volAttach,omitempty"`
	BootVolumes json.RawMessage `json:"bootVolumes,omitempty"`
	BootAttach  json.RawMessage `json:"bootAttach,omitempty"`
	Backups     json.RawMessage `json:"backups,omitempty"`
	VolGroups   json.RawMessage `json:"volGroups,omitempty"`
	VNICAttach  json.RawMessage `json:"vnicAttach,omitempty"`
	Pools       json.RawMessage `json:"pools,omitempty"`
	Configs     json.RawMessage `json:"configs,omitempty"`
	Spot        json.RawMessage `json:"spot,omitempty"`
	Shapes      json.RawMessage `json:"shapes,omitempty"`
	Scopes      json.RawMessage `json:"scopes,omitempty"`
	Created     json.RawMessage `json:"created,omitempty"`
	IPCounter   int64           `json:"ipCounter,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Compute holds no bulk object bodies; a volume is metadata only.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := computeSnapshot{IPCounter: m.ipCounter.Load()}

	for _, d := range m.snapshotDumps(&snap) {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("compute: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every OCID
// and the id-string cross-references between resources are preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap computeSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("compute: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range m.snapshotDumps(&snap) {
		if len(*d.dst) == 0 {
			continue
		}

		if err := d.load(*d.dst); err != nil {
			return fmt.Errorf("compute: restore store: %w", err)
		}
	}

	// The counter only ever moves forward, so a restore onto a mock that has
	// already handed addresses out keeps the higher of the two.
	if cur := m.ipCounter.Load(); snap.IPCounter > cur {
		m.ipCounter.Store(snap.IPCounter)
	}

	return nil
}

// storeDump pairs a snapshot field with its store's dump and load functions, so
// Snapshot and Restore share one table and cannot drift apart.
type storeDump struct {
	dst  *json.RawMessage
	fn   func() ([]byte, error)
	load func([]byte) error
}

// snapshotDumps lists every store alongside the snapshot field it maps to.
func (m *Mock) snapshotDumps(snap *computeSnapshot) []storeDump {
	return []storeDump{
		{&snap.Instances, m.instances.Snapshot, m.instances.LoadSnapshot},
		{&snap.Details, m.details.Snapshot, m.details.LoadSnapshot},
		{&snap.Images, m.images.Snapshot, m.images.LoadSnapshot},
		{&snap.Volumes, m.volumes.Snapshot, m.volumes.LoadSnapshot},
		{&snap.VolAttach, m.volAttach.Snapshot, m.volAttach.LoadSnapshot},
		{&snap.BootVolumes, m.bootVolumes.Snapshot, m.bootVolumes.LoadSnapshot},
		{&snap.BootAttach, m.bootAttach.Snapshot, m.bootAttach.LoadSnapshot},
		{&snap.Backups, m.backups.Snapshot, m.backups.LoadSnapshot},
		{&snap.VolGroups, m.volGroups.Snapshot, m.volGroups.LoadSnapshot},
		{&snap.VNICAttach, m.vnicAttach.Snapshot, m.vnicAttach.LoadSnapshot},
		{&snap.Pools, m.pools.Snapshot, m.pools.LoadSnapshot},
		{&snap.Configs, m.configs.Snapshot, m.configs.LoadSnapshot},
		{&snap.Spot, m.spot.Snapshot, m.spot.LoadSnapshot},
		{&snap.Shapes, m.shapes.Snapshot, m.shapes.LoadSnapshot},
		{&snap.Scopes, m.scopes.Snapshot, m.scopes.LoadSnapshot},
		{&snap.Created, m.created.Snapshot, m.created.LoadSnapshot},
	}
}
