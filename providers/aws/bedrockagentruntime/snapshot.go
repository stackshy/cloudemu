package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// runtimeSnapshot is the serialized state of the runtime mock. Document-content
// handles are kept until they expire, so a presigned URL survives a restart.
// Settle windows are not persisted, like every settle window, so a restored
// Running execution reads as finished.
type runtimeSnapshot struct {
	Sessions      json.RawMessage `json:"sessions,omitempty"`
	SessionTags   json.RawMessage `json:"sessionTags,omitempty"`
	Invocations   json.RawMessage `json:"invocations,omitempty"`
	Steps         json.RawMessage `json:"invocationSteps,omitempty"`
	Conversations json.RawMessage `json:"conversations,omitempty"`
	Memories      json.RawMessage `json:"memories,omitempty"`
	Executions    json.RawMessage `json:"flowExecutions,omitempty"`
	DocTokens     json.RawMessage `json:"documentContentTokens,omitempty"`
}

// Snapshot captures the mock's state as JSON. includeAssets is unused.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap runtimeSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Sessions, m.sessions.Snapshot},
		{&snap.SessionTags, m.sessionTags.Snapshot},
		{&snap.Invocations, m.invocations.Snapshot},
		{&snap.Steps, m.steps.Snapshot},
		{&snap.Conversations, m.conversations.Snapshot},
		{&snap.Memories, m.memories.Snapshot},
		{&snap.Executions, m.executions.Snapshot},
		{&snap.DocTokens, m.docTokens.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("bedrockagentruntime: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state from a Snapshot.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap runtimeSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("bedrockagentruntime: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Sessions, m.sessions.LoadSnapshot},
		{snap.SessionTags, m.sessionTags.LoadSnapshot},
		{snap.Invocations, m.invocations.LoadSnapshot},
		{snap.Steps, m.steps.LoadSnapshot},
		{snap.Conversations, m.conversations.LoadSnapshot},
		{snap.Memories, m.memories.LoadSnapshot},
		{snap.Executions, m.executions.LoadSnapshot},
		{snap.DocTokens, m.docTokens.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("bedrockagentruntime: restore store: %w", err)
		}
	}

	return nil
}
