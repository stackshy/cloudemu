package bedrockagent

import (
	"context"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Collaborator constraints from the service model.
const (
	maxCollaborationInstructionLen = 4000
	agentAliasARNPattern           = `arn:aws:bedrock:[a-z0-9-]{1,20}:[0-9]{12}:agent-alias/[0-9a-zA-Z]{10}/[0-9a-zA-Z]{10}`
)

// agentAliasARN is the AgentAliasArn pattern, anchored.
var agentAliasARN = regexp.MustCompile(`^` + agentAliasARNPattern + `$`)

// AssociateAgentCollaborator adds a collaborating agent alias to a supervisor
// agent's DRAFT. The agent's agentCollaboration must be SUPERVISOR or
// SUPERVISOR_ROUTER, and the alias must exist. Names are unique per agent.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) AssociateAgentCollaborator(_ context.Context, cfg driver.AgentCollaboratorConfig) (*driver.AgentCollaborator, error) {
	if err := validateCollaborator(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkSupervisor(cfg); err != nil {
		return nil, err
	}

	if err := m.checkCollaboratorName(cfg.AgentID, "", cfg.Name); err != nil {
		return nil, err
	}

	prefix := versionKey(cfg.AgentID, driver.DraftVersion)

	id, err := uniqueID(func(id string) bool { return m.collaborators.Has(versionKey(prefix, id)) })
	if err != nil {
		return nil, err
	}

	now := m.now()
	col := &driver.AgentCollaborator{
		ID:                       id,
		AgentID:                  cfg.AgentID,
		AgentVersion:             driver.DraftVersion,
		AliasARN:                 cfg.AliasARN,
		Name:                     cfg.Name,
		Instruction:              cfg.Instruction,
		RelayConversationHistory: orDefault(cfg.RelayConversationHistory, driver.RelayDisabled),
		ClientToken:              cfg.ClientToken,
		CreatedAt:                now,
		LastUpdatedAt:            now,
	}
	m.collaborators.Set(versionKey(prefix, id), col)
	m.markDraftChanged(cfg.AgentID)

	result := *col

	return &result, nil
}

// GetAgentCollaborator returns a collaborator of any agent version.
func (m *Mock) GetAgentCollaborator(_ context.Context, agentID, version, collaboratorID string) (*driver.AgentCollaborator, error) {
	if err := m.checkAgentChild(agentID, version, "collaboratorId", collaboratorID); err != nil {
		return nil, err
	}

	col, ok := m.collaborators.Get(versionKey(agentID, version, collaboratorID))
	if !ok {
		return nil, collaboratorNotFound(collaboratorID)
	}

	result := *col

	return &result, nil
}

// UpdateAgentCollaborator replaces a DRAFT collaborator's alias, name,
// instruction and relay setting.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateAgentCollaborator(_ context.Context, collaboratorID string, cfg driver.AgentCollaboratorConfig) (
	*driver.AgentCollaborator, error,
) {
	if err := validateCollaborator(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkSupervisor(cfg); err != nil {
		return nil, err
	}

	key := versionKey(cfg.AgentID, driver.DraftVersion, collaboratorID)

	col, ok := m.collaborators.Get(key)
	if !ok {
		return nil, collaboratorNotFound(collaboratorID)
	}

	if err := m.checkCollaboratorName(cfg.AgentID, collaboratorID, cfg.Name); err != nil {
		return nil, err
	}

	updated := *col
	updated.AliasARN = cfg.AliasARN
	updated.Name = cfg.Name
	updated.Instruction = cfg.Instruction
	updated.RelayConversationHistory = orDefault(cfg.RelayConversationHistory, col.RelayConversationHistory)
	updated.LastUpdatedAt = m.now()
	m.collaborators.Set(key, &updated)
	m.markDraftChanged(cfg.AgentID)

	result := updated

	return &result, nil
}

// DisassociateAgentCollaborator removes a collaborator from the DRAFT.
func (m *Mock) DisassociateAgentCollaborator(_ context.Context, agentID, version, collaboratorID string) error {
	return deleteDraftChild(m, m.collaborators, agentID, version, "collaboratorId", collaboratorID, collaboratorNotFound)
}

// ListAgentCollaborators lists one page of an agent version's collaborators.
func (m *Mock) ListAgentCollaborators(_ context.Context, agentID, version string, page driver.Page) (
	[]driver.AgentCollaborator, string, error,
) {
	if err := m.checkAgentVersion(agentID, version); err != nil {
		return nil, "", err
	}

	items := childrenOf(m.collaborators, versionKey(agentID, version),
		func(c *driver.AgentCollaborator) driver.AgentCollaborator { return *c })

	return paginate(items, page)
}

// checkSupervisor resolves the supervisor agent and the collaborating alias.
// Callers hold m.mu.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) checkSupervisor(cfg driver.AgentCollaboratorConfig) error {
	agent, ok := m.agents.Get(cfg.AgentID)
	if !ok {
		return agentNotFound(cfg.AgentID)
	}

	if agent.AgentCollaboration == "" || agent.AgentCollaboration == driver.AgentCollaborationDisabled {
		return errors.Newf(errors.InvalidArgument,
			"Agent %s has agentCollaboration DISABLED. Set it to SUPERVISOR or SUPERVISOR_ROUTER to add collaborators.",
			cfg.AgentID)
	}

	if !m.arnExists(cfg.AliasARN) || strings.HasSuffix(cfg.AliasARN, "/"+driver.AgentTestAliasID) {
		return errors.Newf(errors.NotFound, "agent alias %q not found", cfg.AliasARN)
	}

	return nil
}

// checkCollaboratorName rejects a name another DRAFT collaborator of the agent
// already uses. Callers hold m.mu.
func (m *Mock) checkCollaboratorName(agentID, selfID, name string) error {
	prefix := versionKey(agentID, driver.DraftVersion) + "/"

	for key, c := range m.collaborators.All() {
		if strings.HasPrefix(key, prefix) && c.ID != selfID && c.Name == name {
			return errors.Newf(errors.AlreadyExists,
				"A collaborator named %s already exists for agent %s.", name, agentID)
		}
	}

	return nil
}

// validateCollaborator checks the members Associate- and
// UpdateAgentCollaborator share.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateCollaborator(cfg driver.AgentCollaboratorConfig) error {
	var v violations

	v.id("agentId", cfg.AgentID)
	v.draft(cfg.AgentVersion)
	v.required("agentDescriptor.aliasArn", cfg.AliasARN == "")
	v.pattern("agentDescriptor.aliasArn", cfg.AliasARN, agentAliasARN, agentAliasARNPattern)
	v.name("collaboratorName", cfg.Name)
	v.required("collaborationInstruction", cfg.Instruction == "")
	v.maxLen("collaborationInstruction", cfg.Instruction, maxCollaborationInstructionLen)
	v.enum("relayConversationHistory", cfg.RelayConversationHistory, driver.RelayToCollaborator, driver.RelayDisabled)
	v.clientToken(cfg.ClientToken)

	return v.err()
}

func collaboratorNotFound(id string) error {
	return errors.Newf(errors.NotFound, "agent collaborator %q not found", id)
}
