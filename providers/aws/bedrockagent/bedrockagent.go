// Package bedrockagent provides an in-memory mock implementation of the AWS
// Bedrock Agent authoring control plane: agents (and aliases), knowledge bases,
// data sources, ingestion jobs, flows, and prompts, with ARN-keyed tags.
// Resources are created directly in a terminal/ready state.
package bedrockagent

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// defaultIdleSessionTTL is the idle-session timeout assigned to agents created
// without an explicit value, matching the Bedrock default.
const defaultIdleSessionTTL int32 = 600

// statusDeleting is the transitional status returned by delete operations.
const statusDeleting = "DELETING"

// Compile-time checks that Mock implements driver.BedrockAgent and every
// optional capability.
var (
	_ driver.BedrockAgent           = (*Mock)(nil)
	_ driver.AgentVersions          = (*Mock)(nil)
	_ driver.ActionGroups           = (*Mock)(nil)
	_ driver.AgentKnowledgeBases    = (*Mock)(nil)
	_ driver.AgentCollaborators     = (*Mock)(nil)
	_ driver.IngestionJobs          = (*Mock)(nil)
	_ driver.FlowVersions           = (*Mock)(nil)
	_ driver.PromptVersions         = (*Mock)(nil)
	_ driver.KnowledgeBaseDocuments = (*Mock)(nil)
	_ driver.FlowValidator          = (*Mock)(nil)
	_ driver.ResourcePolicies       = (*Mock)(nil)
)

// Mock is an in-memory mock implementation of the AWS Bedrock Agent service.
type Mock struct {
	agents     *memstore.Store[*driver.Agent]
	aliases    *memstore.Store[*driver.AgentAlias]
	knowledge  *memstore.Store[*driver.KnowledgeBase]
	dataSource *memstore.Store[*driver.DataSource]
	jobs       *memstore.Store[*driver.IngestionJob]
	flows      *memstore.Store[*driver.Flow]
	prompts    *memstore.Store[*driver.Prompt]
	// tags holds each resource's tags keyed by its ARN; tagMu serializes the
	// read-modify-write of Tag/UntagResource.
	tags  *memstore.Store[map[string]string]
	tagMu sync.Mutex

	// Versions are keyed "<parentID>/<version>"; agent sub-resources are keyed
	// "<agentID>/<version>/<childID>"; documents "<kbID>/<dsID>/<docKey>".
	agentVersions  *memstore.Store[*driver.Agent]
	actionGroups   *memstore.Store[*driver.ActionGroup]
	agentKBs       *memstore.Store[*driver.AgentKnowledgeBase]
	collaborators  *memstore.Store[*driver.AgentCollaborator]
	flowVersions   *memstore.Store[*driver.Flow]
	flowAliases    *memstore.Store[*driver.FlowAlias]
	promptVersions *memstore.Store[*driver.Prompt]
	documents      *memstore.Store[*storedDocument]
	policies       *memstore.Store[*driver.ResourcePolicy]
	// versionSeq holds the last version number cut per "<kind>/<id>".
	versionSeq *memstore.Store[int]
	// jobSettle overlays IN_PROGRESS on ingestion jobs while they run under
	// async settling. It is not persisted, like every settle window.
	jobSettle *settle.Set

	// mu serializes every compound write: name-uniqueness scans plus inserts,
	// version cuts (DRAFT and its children copied as one snapshot), alias
	// routing checks, and cascading deletes. It is taken before tagMu, never
	// after.
	mu sync.Mutex

	// objects is S3, read by ingestion jobs over S3 data sources; nil when the
	// mock is used alone.
	objects ObjectReader

	opts *config.Options
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
		tags:       memstore.New[map[string]string](),

		agentVersions:  memstore.New[*driver.Agent](),
		actionGroups:   memstore.New[*driver.ActionGroup](),
		agentKBs:       memstore.New[*driver.AgentKnowledgeBase](),
		collaborators:  memstore.New[*driver.AgentCollaborator](),
		flowVersions:   memstore.New[*driver.Flow](),
		flowAliases:    memstore.New[*driver.FlowAlias](),
		promptVersions: memstore.New[*driver.Prompt](),
		documents:      memstore.New[*storedDocument](),
		policies:       memstore.New[*driver.ResourcePolicy](),
		versionSeq:     memstore.New[int](),
		jobSettle:      settle.NewSet(),

		opts: opts,
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
