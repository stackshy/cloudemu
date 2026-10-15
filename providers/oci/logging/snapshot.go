package logging

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// loggingSnapshot is the full serialized state of the OCI Logging mock. Groups
// are keyed by log group OCID and log records by log OCID, so a log's
// LogGroupID still resolves to its group after a restore. Every value type is
// exported, so both stores round-trip through the generic memstore helper; the
// mutex, *config.Options and the monitoring driver are not serialized.
type loggingSnapshot struct {
	Groups json.RawMessage `json:"groups,omitempty"`
	Logs   json.RawMessage `json:"logs,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// an ingested log entry is part of the state, not a bulk object body.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap loggingSnapshot

	for _, d := range m.snapshotDumps(&snap) {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("logging: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every OCID,
// each log's group cross-reference and every ingested entry are preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap loggingSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("logging: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range m.snapshotDumps(&snap) {
		if len(*d.dst) == 0 {
			continue
		}

		if err := d.load(*d.dst); err != nil {
			return fmt.Errorf("logging: restore store: %w", err)
		}
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
func (m *Mock) snapshotDumps(snap *loggingSnapshot) []storeDump {
	return []storeDump{
		{&snap.Groups, m.groups.Snapshot, m.groups.LoadSnapshot},
		{&snap.Logs, m.logs.Snapshot, m.logs.LoadSnapshot},
	}
}
