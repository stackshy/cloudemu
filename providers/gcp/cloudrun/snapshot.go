package cloudrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// crSnapshot is the full serialized state of the Cloud Run mock. Every store
// holds a fully-exported driver value type (Job, Execution, Service, Revision) —
// or, for engineHandles, a plain []string — keyed by resource id, so each
// round-trips through the generic memstore helper; no promotion is needed. The
// container template a job/service/revision carries (its image reference and
// command, the deployable "code") lives in those exported fields and survives
// unchanged. The wired ContainerEngine (m.opts) and the mutex are intentionally
// not serialized; a restored engineHandles entry records that a job's executions
// were engine-backed, but the real containers behind those handles do not survive
// a process restart.
type crSnapshot struct {
	Jobs          json.RawMessage `json:"jobs,omitempty"`
	Executions    json.RawMessage `json:"executions,omitempty"`
	Services      json.RawMessage `json:"services,omitempty"`
	Revisions     json.RawMessage `json:"revisions,omitempty"`
	EngineHandles json.RawMessage `json:"engineHandles,omitempty"`
}

// Snapshot captures every job, execution, service, and revision as JSON.
// includeAssets is unused — Cloud Run holds container references, not object
// bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var snap crSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Jobs, m.jobs.Snapshot},
		{&snap.Executions, m.executions.Snapshot},
		{&snap.Services, m.services.Snapshot},
		{&snap.Revisions, m.revisions.Snapshot},
		{&snap.EngineHandles, m.engineHandles.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("cloudrun: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds every job, execution, service, and revision under its original
// id, so parent references (execution->job, revision->service) survive unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap crSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudrun: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Jobs, m.jobs.LoadSnapshot},
		{snap.Executions, m.executions.LoadSnapshot},
		{snap.Services, m.services.LoadSnapshot},
		{snap.Revisions, m.revisions.LoadSnapshot},
		{snap.EngineHandles, m.engineHandles.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("cloudrun: restore store: %w", err)
		}
	}

	return nil
}
