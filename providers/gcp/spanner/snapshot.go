package spanner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// spannerSnapshot is the full serialized state of the Spanner admin mock. Every
// store holds a fully-exported spdriver value type keyed by its full GCP resource
// name (projects/{p}/instances/{i}[/databases/{d}]), so each round-trips through
// the generic memstore helper — no field promotion is needed. opSeq is the
// operation-name counter, captured beside the stores so restored operation ids do
// not collide with fresh ones. The wired deps (m.opts) and the RWMutex are
// intentionally not serialized.
type spannerSnapshot struct {
	Instances  json.RawMessage `json:"instances,omitempty"`
	Databases  json.RawMessage `json:"databases,omitempty"`
	Operations json.RawMessage `json:"operations,omitempty"`
	OpSeq      uint64          `json:"opSeq,omitempty"`
}

// Snapshot captures every instance, database, and operation as JSON.
// includeAssets is unused — Spanner admin is control-plane only and holds no bulk
// object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap spannerSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Instances, m.instances.Snapshot},
		{&snap.Databases, m.databases.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("spanner: snapshot store: %w", err)
		}

		*d.dst = b
	}

	snap.OpSeq = m.opSeq.Load()

	return json.Marshal(snap)
}

// Restore rebuilds every instance and database under its original resource name,
// so database->instance cross-references survive a restore.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap spannerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("spanner: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Instances, m.instances.LoadSnapshot},
		{snap.Databases, m.databases.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("spanner: restore store: %w", err)
		}
	}

	m.opSeq.Store(snap.OpSeq)

	return nil
}
