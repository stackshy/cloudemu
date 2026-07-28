// Package bedrockagent provides an in-memory mock implementation of the AWS
// Bedrock Agent authoring control plane: agents (and aliases), knowledge bases,
// data sources, ingestion jobs, flows, and prompts. Resources are created
// directly in a terminal/ready state.
package bedrockagent

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// defaultIdleSessionTTL is the idle-session timeout assigned to agents created
// without an explicit value, matching the Bedrock default.
const defaultIdleSessionTTL int32 = 600

// statusDeleting is the transitional status returned by delete operations.
const statusDeleting = "DELETING"

// Compile-time check that Mock implements driver.BedrockAgent.
var _ driver.BedrockAgent = (*Mock)(nil)

// Mock is an in-memory mock implementation of the AWS Bedrock Agent service.
type Mock struct {
	agents     *memstore.Store[*driver.Agent]
	aliases    *memstore.Store[*driver.AgentAlias]
	knowledge  *memstore.Store[*driver.KnowledgeBase]
	dataSource *memstore.Store[*driver.DataSource]
	jobs       *memstore.Store[*driver.IngestionJob]
	flows      *memstore.Store[*driver.Flow]
	prompts    *memstore.Store[*driver.Prompt]
	opts       *config.Options
}

// New creates a new Bedrock Agent mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		agents:     memstore.New[*driver.Agent](),
		aliases:    memstore.New[*driver.AgentAlias](),
		knowledge:  memstore.New[*driver.KnowledgeBase](),
		dataSource: memstore.New[*driver.DataSource](),
		jobs:       memstore.New[*driver.IngestionJob](),
		flows:      memstore.New[*driver.Flow](),
		prompts:    memstore.New[*driver.Prompt](),
		opts:       opts,
	}
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// copyRaw returns a defensive copy of a json.RawMessage so stored resources do
// not alias caller-owned buffers. nil maps to nil.
func copyRaw(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}

	out := make(json.RawMessage, len(b))
	copy(out, b)

	return out
}
