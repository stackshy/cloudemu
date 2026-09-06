package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// schedulerSnapshot is the full serialized state of the Cloud Scheduler mock.
// The jobs store holds fully-exported driver.Job value types keyed by resource
// name, so it round-trips through the generic memstore helper unchanged; the
// mutex and wired options are intentionally not serialized.
type schedulerSnapshot struct {
	Jobs json.RawMessage `json:"jobs,omitempty"`
}

// Snapshot captures every job as JSON. includeAssets is unused — Cloud
// Scheduler holds no object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs, err := m.jobs.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("scheduler: snapshot jobs: %w", err)
	}

	return json.Marshal(schedulerSnapshot{Jobs: jobs})
}

// Restore rebuilds every job under its original resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap schedulerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("scheduler: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(snap.Jobs) == 0 {
		return nil
	}

	if err := m.jobs.LoadSnapshot(snap.Jobs); err != nil {
		return fmt.Errorf("scheduler: restore jobs: %w", err)
	}

	return nil
}
