package driver

import "context"

// Knowledge-base association state values (KnowledgeBaseState).
const (
	AgentKnowledgeBaseEnabled  = "ENABLED"
	AgentKnowledgeBaseDisabled = "DISABLED"
)

// AgentKnowledgeBaseConfig describes a knowledge-base association. On update an
// empty Description or State keeps the stored value.
type AgentKnowledgeBaseConfig struct {
	AgentID         string
	AgentVersion    string
	KnowledgeBaseID string
	Description     string
	State           string
}

// AgentKnowledgeBase is a knowledge base associated with an agent version.
type AgentKnowledgeBase struct {
	AgentID         string
	AgentVersion    string
	KnowledgeBaseID string
	Description     string
	State           string
	CreatedAt       string
	UpdatedAt       string
}

// AgentKnowledgeBases associates knowledge bases with an agent version.
//
// Writes are allowed only on DRAFT; reads work on any version.
type AgentKnowledgeBases interface {
	AssociateAgentKnowledgeBase(ctx context.Context, cfg AgentKnowledgeBaseConfig) (*AgentKnowledgeBase, error)
	GetAgentKnowledgeBase(ctx context.Context, agentID, version, knowledgeBaseID string) (*AgentKnowledgeBase, error)
	UpdateAgentKnowledgeBase(ctx context.Context, cfg AgentKnowledgeBaseConfig) (*AgentKnowledgeBase, error)
	DisassociateAgentKnowledgeBase(ctx context.Context, agentID, version, knowledgeBaseID string) error
	ListAgentKnowledgeBases(ctx context.Context, agentID, version string, page Page) ([]AgentKnowledgeBase, string, error)
}
