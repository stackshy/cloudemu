package cloudtasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cloudtasksSnapshot is the full serialized state of the Cloud Tasks mock. The
// queues store holds fully-exported driver.Queue value types keyed by resource
// name (including any stored IAM policy), so it round-trips through the generic
// memstore helper unchanged; the mutex and wired options are intentionally not
// serialized.
type cloudtasksSnapshot struct {
	Queues json.RawMessage `json:"queues,omitempty"`
}

// Snapshot captures every queue as JSON. includeAssets is unused — Cloud Tasks
// holds no object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	queues, err := m.queues.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudtasks: snapshot queues: %w", err)
	}

	return json.Marshal(cloudtasksSnapshot{Queues: queues})
}

// Restore rebuilds every queue under its original resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cloudtasksSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudtasks: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(snap.Queues) == 0 {
		return nil
	}

	if err := m.queues.LoadSnapshot(snap.Queues); err != nil {
		return fmt.Errorf("cloudtasks: restore queues: %w", err)
	}

	return nil
}
