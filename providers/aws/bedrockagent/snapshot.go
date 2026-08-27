package bedrockagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// bedrockAgentSnapshot is the full serialized state of the AWS Bedrock Agent
// mock. Every store holds a fully-exported *driver type, so each round-trips
// through the generic memstore helper keyed by its resource id. The wired opts
// are intentionally not serialized.
type bedrockAgentSnapshot struct {
	Agents      json.RawMessage `json:"agents,omitempty"`
	Aliases     json.RawMessage `json:"aliases,omitempty"`
	Knowledge   json.RawMessage `json:"knowledge,omitempty"`
	DataSources json.RawMessage `json:"dataSources,omitempty"`
	Jobs        json.RawMessage `json:"jobs,omitempty"`
	Flows       json.RawMessage `json:"flows,omitempty"`
	Prompts     json.RawMessage `json:"prompts,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Bedrock Agent holds resource metadata, not bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap bedrockAgentSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Agents, m.agents.Snapshot},
		{&snap.Aliases, m.aliases.Snapshot},
		{&snap.Knowledge, m.knowledge.Snapshot},
		{&snap.DataSources, m.dataSource.Snapshot},
		{&snap.Jobs, m.jobs.Snapshot},
		{&snap.Flows, m.flows.Snapshot},
		{&snap.Prompts, m.prompts.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("bedrockagent: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every agent,
// alias, knowledge base, data source, ingestion job, flow, and prompt id is
// preserved so cross-references (e.g. a data source's knowledge-base id) resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap bedrockAgentSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("bedrockagent: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Agents, m.agents.LoadSnapshot},
		{snap.Aliases, m.aliases.LoadSnapshot},
		{snap.Knowledge, m.knowledge.LoadSnapshot},
		{snap.DataSources, m.dataSource.LoadSnapshot},
		{snap.Jobs, m.jobs.LoadSnapshot},
		{snap.Flows, m.flows.LoadSnapshot},
		{snap.Prompts, m.prompts.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("bedrockagent: restore store: %w", err)
		}
	}

	return nil
}
