package composer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// composerSnapshot is the full serialized state of the Composer mock. Both
// stores hold fully-exported cdriver value types keyed by their full GCP
// resource name (projects/{p}/locations/{loc}/environments/{env} and
// .../operations/{op}), so each round-trips through the generic memstore helper
// — no field promotion is needed. opSeq is the operation-name counter, captured
// beside the stores so restored operation ids do not collide with fresh ones.
// The wired deps (m.opts) and the RWMutex are intentionally not serialized.
type composerSnapshot struct {
	Environments json.RawMessage `json:"environments,omitempty"`
	Operations   json.RawMessage `json:"operations,omitempty"`
	OpSeq        uint64          `json:"opSeq,omitempty"`
}

// Snapshot captures every environment and operation as JSON. includeAssets is
// unused — Composer admin is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap composerSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Environments, m.environments.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("composer: snapshot store: %w", err)
		}

		*d.dst = b
	}

	snap.OpSeq = m.opSeq.Load()

	return json.Marshal(snap)
}

// Restore rebuilds every environment and operation under its original resource
// name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap composerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("composer: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Environments, m.environments.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("composer: restore store: %w", err)
		}
	}

	m.opSeq.Store(snap.OpSeq)

	return nil
}
