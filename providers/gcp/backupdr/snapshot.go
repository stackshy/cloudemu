package backupdr

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// backupdrSnapshot is the full serialized state of the Backup and DR mock. Every
// store holds fully-exported bdrdriver value types keyed by their full GCP
// resource name, so each round-trips through the generic memstore helper: no
// field promotion is needed. opSeq is the operation-name counter, captured beside
// the stores so restored operation ids do not collide with fresh ones. The wired
// deps (m.opts) and the RWMutex are intentionally not serialized.
type backupdrSnapshot struct {
	Vaults     json.RawMessage `json:"backupVaults,omitempty"`
	Operations json.RawMessage `json:"operations,omitempty"`
	OpSeq      uint64          `json:"opSeq,omitempty"`
}

// Snapshot captures every backup vault and operation as JSON. includeAssets is
// unused: Backup and DR admin is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap backupdrSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Vaults, m.vaults.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("backupdr: snapshot store: %w", err)
		}

		*d.dst = b
	}

	snap.OpSeq = m.opSeq.Load()

	return json.Marshal(snap)
}

// Restore rebuilds every backup vault and operation under its original resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap backupdrSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("backupdr: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Vaults, m.vaults.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("backupdr: restore store: %w", err)
		}
	}

	m.opSeq.Store(snap.OpSeq)

	return nil
}
