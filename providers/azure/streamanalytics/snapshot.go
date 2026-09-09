package streamanalytics

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotState is the on-disk shape: both the job and child stores keyed by
// their (lowercased) resource ids.
type snapshotState struct {
	Jobs     json.RawMessage `json:"jobs,omitempty"`
	Children json.RawMessage `json:"children,omitempty"`
}

// Snapshot captures every streaming job and its child resources. includeAssets
// is unused — these resources hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	jobs, err := m.jobs.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("streamanalytics: snapshot jobs: %w", err)
	}

	children, err := m.children.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("streamanalytics: snapshot children: %w", err)
	}

	data, err := json.Marshal(snapshotState{Jobs: jobs, Children: children})
	if err != nil {
		return nil, fmt.Errorf("streamanalytics: marshal snapshot: %w", err)
	}

	return data, nil
}

// Restore rebuilds every streaming job and child resource under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("streamanalytics: unmarshal snapshot: %w", err)
	}

	if len(state.Jobs) > 0 {
		if err := m.jobs.LoadSnapshot(state.Jobs); err != nil {
			return fmt.Errorf("streamanalytics: restore jobs: %w", err)
		}
	}

	if len(state.Children) > 0 {
		if err := m.children.LoadSnapshot(state.Children); err != nil {
			return fmt.Errorf("streamanalytics: restore children: %w", err)
		}
	}

	return nil
}
