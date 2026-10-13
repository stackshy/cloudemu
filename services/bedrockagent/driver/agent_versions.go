package driver

import "context"

// The interfaces in this package's feature files (AgentVersions, ActionGroups,
// AgentKnowledgeBases, AgentCollaborators, IngestionJobs, FlowVersions,
// PromptVersions, KnowledgeBaseDocuments, FlowValidator, ResourcePolicies) are
// optional capabilities of a Bedrock Agent provider: the wire handler and the
// portable wrapper discover them by type assertion, so they extend the
// BedrockAgent interface without widening it.

// AgentTestAliasID is the id of the test alias every agent has. It routes to
// the DRAFT version and cannot be updated or deleted.
const AgentTestAliasID = "TSTALIASID"

// AgentTestAliasName is the name of the test alias.
const AgentTestAliasName = "AgentTestAlias"

// AgentVersions manages agent versions and the rest of the alias lifecycle.
//
// Versions are cut by CreateAgentAlias
// and UpdateAgentAlias when no routingConfiguration is given; they are
// immutable snapshots of DRAFT, its action groups, knowledge-base associations
// and collaborators. ListAgentVersions includes DRAFT.
type AgentVersions interface {
	GetAgentVersion(ctx context.Context, agentID, version string) (*Agent, error)
	ListAgentVersions(ctx context.Context, agentID string, page Page) ([]Agent, string, error)
	DeleteAgentVersion(ctx context.Context, agentID, version string, skipResourceInUseCheck bool) (string, error)
	GetAgentAlias(ctx context.Context, agentID, aliasID string) (*AgentAlias, error)
	ListAgentAliases(ctx context.Context, agentID string, page Page) ([]AgentAlias, string, error)
	UpdateAgentAlias(ctx context.Context, aliasID string, cfg AgentAliasConfig) (*AgentAlias, error)
	DeleteAgentAlias(ctx context.Context, agentID, aliasID string) (string, error)
}
