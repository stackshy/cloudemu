package driver

import "context"

// RelayConversationHistory values.
const (
	RelayToCollaborator = "TO_COLLABORATOR"
	RelayDisabled       = "DISABLED"
)

// AgentCollaboratorConfig describes a collaborator of a supervisor agent.
// AliasARN is the agentDescriptor.aliasArn of the collaborating agent alias.
type AgentCollaboratorConfig struct {
	AgentID                  string
	AgentVersion             string
	AliasARN                 string
	Name                     string
	Instruction              string
	RelayConversationHistory string
	ClientToken              string
}

// AgentCollaborator is a collaborator associated with a supervisor agent
// version.
type AgentCollaborator struct {
	ID                       string
	AgentID                  string
	AgentVersion             string
	AliasARN                 string
	Name                     string
	Instruction              string
	RelayConversationHistory string
	ClientToken              string
	CreatedAt                string
	LastUpdatedAt            string
}

// AgentCollaborators manages the collaborator agents of a supervisor agent.
//
// The supervisor's agentCollaboration must be SUPERVISOR or SUPERVISOR_ROUTER.
// Writes are allowed only on DRAFT; reads work on any version.
//
//nolint:dupl // same CRUD shape as the other agent-version children, different resource.
type AgentCollaborators interface {
	AssociateAgentCollaborator(ctx context.Context, cfg AgentCollaboratorConfig) (*AgentCollaborator, error)
	GetAgentCollaborator(ctx context.Context, agentID, version, collaboratorID string) (*AgentCollaborator, error)
	UpdateAgentCollaborator(ctx context.Context, collaboratorID string, cfg AgentCollaboratorConfig) (*AgentCollaborator, error)
	DisassociateAgentCollaborator(ctx context.Context, agentID, version, collaboratorID string) error
	ListAgentCollaborators(ctx context.Context, agentID, version string, page Page) ([]AgentCollaborator, string, error)
}

// AgentInUseDeleter deletes an agent the way DeleteAgent's
// skipResourceInUseCheck asks: an agent whose alias another agent's
// collaborator uses is in use, and is kept (ConflictException) unless
// skipResourceInUseCheck is set. Plain DeleteAgent deletes without the check.
type AgentInUseDeleter interface {
	DeleteAgentChecked(ctx context.Context, agentID string, skipResourceInUseCheck bool) (string, error)
}
