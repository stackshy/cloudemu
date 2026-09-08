package accesscontextmanager

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// acmSnapshot is the full serialized state of the Access Context Manager mock.
// Every store holds fully-exported acmdriver value types keyed by their full
// GCP resource name, so each round-trips through the generic memstore helper —
// no field promotion is needed. opSeq and etagSeq are captured beside the stores
// so restored operation ids and etags do not collide with fresh ones. The wired
// deps (m.opts) and the RWMutex are intentionally not serialized.
type acmSnapshot struct {
	Policies   json.RawMessage `json:"policies,omitempty"`
	Levels     json.RawMessage `json:"levels,omitempty"`
	Perimeters json.RawMessage `json:"perimeters,omitempty"`
	Operations json.RawMessage `json:"operations,omitempty"`
	OpSeq      uint64          `json:"opSeq,omitempty"`
	EtagSeq    uint64          `json:"etagSeq,omitempty"`
}

// Snapshot captures every policy, access level, service perimeter, and operation
// as JSON. includeAssets is unused — the control plane holds no bulk bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap acmSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Policies, m.policies.Snapshot},
		{&snap.Levels, m.levels.Snapshot},
		{&snap.Perimeters, m.perimeters.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("accesscontextmanager: snapshot store: %w", err)
		}

		*d.dst = b
	}

	snap.OpSeq = m.opSeq.Load()
	snap.EtagSeq = m.etagSeq.Load()

	return json.Marshal(snap)
}

// Restore rebuilds every policy, access level, service perimeter, and operation
// under its original resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap acmSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("accesscontextmanager: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Policies, m.policies.LoadSnapshot},
		{snap.Levels, m.levels.LoadSnapshot},
		{snap.Perimeters, m.perimeters.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("accesscontextmanager: restore store: %w", err)
		}
	}

	m.opSeq.Store(snap.OpSeq)
	m.etagSeq.Store(snap.EtagSeq)

	return nil
}
