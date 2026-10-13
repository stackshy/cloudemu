// Package driver defines the interface for Bedrock Agent-style services: the
// authoring control plane for agents, knowledge bases, data sources, ingestion
// jobs, flows, and prompts.
package driver

import (
	"context"
	"encoding/json"
)

// Agent lifecycle status values (UPPER_SNAKE, per AgentStatus).
const (
	AgentNotPrepared = "NOT_PREPARED"
	AgentPrepared    = "PREPARED"
)

// AgentAlias status values.
const (
	AgentAliasPrepared = "PREPARED"
)

// AgentCollaborationDisabled is the agentCollaboration value of an agent that
// is not a supervisor; it is the default.
const AgentCollaborationDisabled = "DISABLED"

// OrchestrationTypeDefault is the default orchestrationType of an agent.
const OrchestrationTypeDefault = "DEFAULT"

// KnowledgeBase status values.
const (
	KnowledgeBaseActive = "ACTIVE"
)

// DataSource status values.
const (
	DataSourceAvailable = "AVAILABLE"
)

// IngestionJob status values.
const (
	IngestionJobComplete   = "COMPLETE"
	IngestionJobInProgress = "IN_PROGRESS"
	IngestionJobStopped    = "STOPPED"
)

// Flow status values (PascalCase, per FlowStatus).
const (
	FlowNotPrepared = "NotPrepared"
	FlowPrepared    = "Prepared"
)

// DraftVersion is the working version assigned to freshly created resources.
const DraftVersion = "DRAFT"

// AgentConfig describes an agent to create. The RawMessage members are kept
// verbatim; an Update that omits one keeps the stored value.
type AgentConfig struct {
	Name                        string
	ResourceRoleArn             string
	FoundationModel             string
	Instruction                 string
	Description                 string
	IdleSessionTTLInSeconds     int32
	ClientToken                 string
	Tags                        map[string]string
	AgentCollaboration          string
	OrchestrationType           string
	CustomerEncryptionKeyArn    string
	PromptOverrideConfiguration json.RawMessage
	GuardrailConfiguration      json.RawMessage
	MemoryConfiguration         json.RawMessage
	CustomOrchestration         json.RawMessage
}

// Agent describes a Bedrock agent. Version is DRAFT for the working draft and
// a number for a version cut from it (see AgentVersions).
type Agent struct {
	ID                          string
	ARN                         string
	Name                        string
	ResourceRoleArn             string
	FoundationModel             string
	Instruction                 string
	Description                 string
	Status                      string
	Version                     string
	IdleSessionTTLInSeconds     int32
	CreatedAt                   string
	UpdatedAt                   string
	PreparedAt                  string
	AgentCollaboration          string
	OrchestrationType           string
	CustomerEncryptionKeyArn    string
	PromptOverrideConfiguration json.RawMessage
	GuardrailConfiguration      json.RawMessage
	MemoryConfiguration         json.RawMessage
	CustomOrchestration         json.RawMessage
}

// AgentAliasRoute is one routingConfiguration entry of an agent alias.
type AgentAliasRoute struct {
	AgentVersion          string
	ProvisionedThroughput string
}

// AgentAliasHistoryEvent records a routing an alias used between two instants.
// EndDate is empty for the routing in effect.
type AgentAliasHistoryEvent struct {
	RoutingConfiguration []AgentAliasRoute
	StartDate            string
	EndDate              string
}

// AgentAliasConfig describes an agent alias to create or update. An empty
// RoutingConfiguration cuts a new numbered version from the agent's DRAFT and
// points the alias at it, as real Bedrock does.
type AgentAliasConfig struct {
	AgentID              string
	Name                 string
	Description          string
	ClientToken          string
	RoutingConfiguration []AgentAliasRoute
	InvocationState      string
	Tags                 map[string]string
}

// AgentAlias describes an alias of an agent.
type AgentAlias struct {
	ID                   string
	ARN                  string
	AgentID              string
	Name                 string
	Description          string
	Status               string
	ClientToken          string
	RoutingConfiguration []AgentAliasRoute
	HistoryEvents        []AgentAliasHistoryEvent
	InvocationState      string
	CreatedAt            string
	UpdatedAt            string
}

// KnowledgeBaseConfig describes a knowledge base to create.
type KnowledgeBaseConfig struct {
	Name                       string
	RoleArn                    string
	Description                string
	KnowledgeBaseConfiguration json.RawMessage
	StorageConfiguration       json.RawMessage
	Tags                       map[string]string
}

// KnowledgeBase describes a Bedrock knowledge base.
type KnowledgeBase struct {
	ID                         string
	ARN                        string
	Name                       string
	RoleArn                    string
	Description                string
	Status                     string
	KnowledgeBaseConfiguration json.RawMessage
	StorageConfiguration       json.RawMessage
	CreatedAt                  string
	UpdatedAt                  string
}

// DataSourceConfig describes a data source to create.
type DataSourceConfig struct {
	KnowledgeBaseID              string
	Name                         string
	Description                  string
	DataDeletionPolicy           string
	DataSourceConfiguration      json.RawMessage
	VectorIngestionConfiguration json.RawMessage
}

// DataSource describes a knowledge-base data source.
type DataSource struct {
	ID                      string
	KnowledgeBaseID         string
	Name                    string
	Description             string
	Status                  string
	DataDeletionPolicy      string
	DataSourceConfiguration json.RawMessage
	CreatedAt               string
	UpdatedAt               string
}

// IngestionJob describes a data-source ingestion job.
type IngestionJob struct {
	ID              string
	KnowledgeBaseID string
	DataSourceID    string
	Description     string
	Status          string
	Statistics      IngestionJobStatistics
	StartedAt       string
	UpdatedAt       string
}

// FlowConfig describes a flow to create.
type FlowConfig struct {
	Name                     string
	ExecutionRoleArn         string
	Description              string
	CustomerEncryptionKeyArn string
	Definition               json.RawMessage
	Tags                     map[string]string
}

// Flow describes a Bedrock flow.
type Flow struct {
	ID                       string
	ARN                      string
	Name                     string
	ExecutionRoleArn         string
	Description              string
	Status                   string
	Version                  string
	CustomerEncryptionKeyArn string
	Definition               json.RawMessage
	CreatedAt                string
	UpdatedAt                string
}

// PromptConfig describes a prompt to create.
type PromptConfig struct {
	Name                     string
	Description              string
	DefaultVariant           string
	CustomerEncryptionKeyArn string
	Variants                 json.RawMessage
	Tags                     map[string]string
}

// Prompt describes a Bedrock prompt.
type Prompt struct {
	ID                       string
	ARN                      string
	Name                     string
	Description              string
	Version                  string
	DefaultVariant           string
	CustomerEncryptionKeyArn string
	Variants                 json.RawMessage
	CreatedAt                string
	UpdatedAt                string
}

// Page is the maxResults/nextToken cursor every List operation takes. A nil
// MaxResults means the caller sent none; a set value must be within 1..1000.
type Page struct {
	MaxResults *int32
	NextToken  string
}

// BedrockAgent is the interface that Bedrock Agent authoring implementations
// must satisfy: agents (and aliases), knowledge bases, data sources, ingestion
// jobs, flows, prompts, and ARN-keyed tagging.
type BedrockAgent interface {
	CreateAgent(ctx context.Context, cfg AgentConfig) (*Agent, error)
	GetAgent(ctx context.Context, agentID string) (*Agent, error)
	ListAgents(ctx context.Context, page Page) ([]Agent, string, error)
	UpdateAgent(ctx context.Context, agentID string, cfg AgentConfig) (*Agent, error)
	DeleteAgent(ctx context.Context, agentID string) (string, error)
	PrepareAgent(ctx context.Context, agentID string) (*Agent, error)
	CreateAgentAlias(ctx context.Context, cfg AgentAliasConfig) (*AgentAlias, error)

	CreateKnowledgeBase(ctx context.Context, cfg KnowledgeBaseConfig) (*KnowledgeBase, error)
	GetKnowledgeBase(ctx context.Context, id string) (*KnowledgeBase, error)
	ListKnowledgeBases(ctx context.Context, page Page) ([]KnowledgeBase, string, error)
	UpdateKnowledgeBase(ctx context.Context, id string, cfg KnowledgeBaseConfig) (*KnowledgeBase, error)
	DeleteKnowledgeBase(ctx context.Context, id string) (string, error)

	CreateDataSource(ctx context.Context, cfg DataSourceConfig) (*DataSource, error)
	GetDataSource(ctx context.Context, kbID, dsID string) (*DataSource, error)
	ListDataSources(ctx context.Context, kbID string, page Page) ([]DataSource, string, error)
	UpdateDataSource(ctx context.Context, cfg DataSourceConfig, dsID string) (*DataSource, error)
	DeleteDataSource(ctx context.Context, kbID, dsID string) (string, error)
	StartIngestionJob(ctx context.Context, kbID, dsID, description string) (*IngestionJob, error)

	CreateFlow(ctx context.Context, cfg FlowConfig) (*Flow, error)
	GetFlow(ctx context.Context, id string) (*Flow, error)
	ListFlows(ctx context.Context, page Page) ([]Flow, string, error)
	UpdateFlow(ctx context.Context, id string, cfg FlowConfig) (*Flow, error)
	DeleteFlow(ctx context.Context, id string) (string, error)
	PrepareFlow(ctx context.Context, id string) (*Flow, error)

	CreatePrompt(ctx context.Context, cfg PromptConfig) (*Prompt, error)
	GetPrompt(ctx context.Context, id string) (*Prompt, error)
	ListPrompts(ctx context.Context, page Page) ([]Prompt, string, error)
	UpdatePrompt(ctx context.Context, id string, cfg PromptConfig) (*Prompt, error)
	DeletePrompt(ctx context.Context, id string) (string, error)

	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) (map[string]string, error)
}
