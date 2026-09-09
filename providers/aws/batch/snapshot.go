package batch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// batchSnapshot is the full serialized state of the Batch mock. Every store
// holds a plain JSON-serializable value, so each round-trips through the generic
// memstore dump helper under its original keys (compute-environment/job-queue
// name, job-definition "name:revision"). The registration mutex and the wired
// *config.Options are intentionally not serialized.
type batchSnapshot struct {
	ComputeEnvs  json.RawMessage `json:"computeEnvs,omitempty"`
	JobQueues    json.RawMessage `json:"jobQueues,omitempty"`
	JobDefs      json.RawMessage `json:"jobDefs,omitempty"`
	JobDefMaxRev json.RawMessage `json:"jobDefMaxRev,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Batch holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap batchSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.ComputeEnvs, m.computeEnvs.Snapshot},
		{&snap.JobQueues, m.jobQueues.Snapshot},
		{&snap.JobDefs, m.jobDefs.Snapshot},
		{&snap.JobDefMaxRev, m.jobDefMaxRev.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("batch: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap batchSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("batch: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.ComputeEnvs, m.computeEnvs.LoadSnapshot},
		{snap.JobQueues, m.jobQueues.LoadSnapshot},
		{snap.JobDefs, m.jobDefs.LoadSnapshot},
		{snap.JobDefMaxRev, m.jobDefMaxRev.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("batch: restore store: %w", err)
		}
	}

	return nil
}
