package bedrockagent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Enum values of the agent members the mock validates (AgentCollaboration,
// OrchestrationType), copied from the service model.
//
//nolint:gochecknoglobals // fixed enum tables from the service model.
var (
	agentCollaborationValues = []string{"SUPERVISOR", "SUPERVISOR_ROUTER", driver.AgentCollaborationDisabled}
	orchestrationTypeValues  = []string{driver.OrchestrationTypeDefault, "CUSTOM_ORCHESTRATION"}
)

// CreateAgent creates an agent in the NOT_PREPARED state. agentResourceRoleArn
// is optional, as in the service model.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateAgent(_ context.Context, cfg driver.AgentConfig) (*driver.Agent, error) {
	if err := validateAgent(cfg, false); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	id, err := newID(m.agents)
	if err != nil {
		return nil, err
	}

	now := m.now()

	ttl := cfg.IdleSessionTTLInSeconds
	if ttl == 0 {
		ttl = defaultIdleSessionTTL
	}

	agent := &driver.Agent{
		ID:                          id,
		ARN:                         idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "agent/"+id),
		Name:                        cfg.Name,
		ResourceRoleArn:             cfg.ResourceRoleArn,
		FoundationModel:             cfg.FoundationModel,
		Instruction:                 cfg.Instruction,
		Description:                 cfg.Description,
		Status:                      driver.AgentNotPrepared,
		Version:                     driver.DraftVersion,
		IdleSessionTTLInSeconds:     ttl,
		CreatedAt:                   now,
		UpdatedAt:                   now,
		AgentCollaboration:          orDefault(cfg.AgentCollaboration, driver.AgentCollaborationDisabled),
		OrchestrationType:           orDefault(cfg.OrchestrationType, driver.OrchestrationTypeDefault),
		CustomerEncryptionKeyArn:    cfg.CustomerEncryptionKeyArn,
		PromptOverrideConfiguration: copyRaw(cfg.PromptOverrideConfiguration),
		GuardrailConfiguration:      copyRaw(cfg.GuardrailConfiguration),
		MemoryConfiguration:         copyRaw(cfg.MemoryConfiguration),
		CustomOrchestration:         copyRaw(cfg.CustomOrchestration),
	}
	m.agents.Set(id, agent)
	m.putTags(agent.ARN, cfg.Tags)

	result := cloneAgent(agent)

	return &result, nil
}

// GetAgent returns an agent by ID.
func (m *Mock) GetAgent(_ context.Context, agentID string) (*driver.Agent, error) {
	agent, ok := m.agents.Get(agentID)
	if !ok {
		return nil, agentNotFound(agentID)
	}

	result := cloneAgent(agent)

	return &result, nil
}

// ListAgents lists one page of agents.
func (m *Mock) ListAgents(_ context.Context, page driver.Page) ([]driver.Agent, string, error) {
	all := m.agents.SortedValues()
	out := make([]driver.Agent, 0, len(all))

	for _, a := range all {
		out = append(out, cloneAgent(a))
	}

	return paginate(out, page)
}

// UpdateAgent updates an agent's mutable fields. Configuration blocks the
// caller omits keep their stored value.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateAgent(_ context.Context, agentID string, cfg driver.AgentConfig) (*driver.Agent, error) {
	if err := validateAgent(cfg, true); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents.Get(agentID)
	if !ok {
		return nil, agentNotFound(agentID)
	}

	updated := cloneAgent(agent)
	updated.Name = cfg.Name
	updated.ResourceRoleArn = cfg.ResourceRoleArn
	updated.FoundationModel = cfg.FoundationModel
	updated.Instruction = orDefault(cfg.Instruction, agent.Instruction)
	updated.Description = cfg.Description
	updated.Status = driver.AgentNotPrepared
	updated.UpdatedAt = m.now()
	updated.AgentCollaboration = orDefault(cfg.AgentCollaboration, agent.AgentCollaboration)
	updated.OrchestrationType = orDefault(cfg.OrchestrationType, agent.OrchestrationType)
	updated.CustomerEncryptionKeyArn = orDefault(cfg.CustomerEncryptionKeyArn, agent.CustomerEncryptionKeyArn)
	updated.PromptOverrideConfiguration = rawOr(cfg.PromptOverrideConfiguration, agent.PromptOverrideConfiguration)
	updated.GuardrailConfiguration = rawOr(cfg.GuardrailConfiguration, agent.GuardrailConfiguration)
	updated.MemoryConfiguration = rawOr(cfg.MemoryConfiguration, agent.MemoryConfiguration)
	updated.CustomOrchestration = rawOr(cfg.CustomOrchestration, agent.CustomOrchestration)

	if cfg.IdleSessionTTLInSeconds != 0 {
		updated.IdleSessionTTLInSeconds = cfg.IdleSessionTTLInSeconds
	}

	m.agents.Set(agentID, &updated)

	result := cloneAgent(&updated)

	return &result, nil
}

// DeleteAgent deletes an agent and, cascading like real AWS, its aliases,
// versions, action groups, knowledge-base associations and collaborators,
// along with their tags.
func (m *Mock) DeleteAgent(ctx context.Context, agentID string) (string, error) {
	return m.DeleteAgentChecked(ctx, agentID, true)
}

// DeleteAgentChecked deletes an agent as DeleteAgent does, but first refuses
// (ConflictException) when another agent's collaborator uses one of its
// aliases, unless skipResourceInUseCheck is set.
func (m *Mock) DeleteAgentChecked(_ context.Context, agentID string, skipResourceInUseCheck bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents.Get(agentID)
	if !ok {
		return "", agentNotFound(agentID)
	}

	if !skipResourceInUseCheck {
		if user := m.collaboratorUsing(agentID); user != nil {
			return "", errors.Newf(errors.FailedPrecondition,
				"Agent %s is in use: agent %s uses its alias %s as collaborator %s. "+
					"Disassociate the collaborator or set skipResourceInUseCheck.", agentID, user.AgentID, user.AliasARN, user.Name)
		}
	}

	m.agents.Delete(agentID)
	m.dropTags(agent.ARN)
	m.deleteAliasesForAgent(agentID)
	m.deleteAgentChildren(agentID, "")
	deleteByPrefix(m.agentVersions, agentID+"/")
	m.versionSeq.Delete(agentSeqKey(agentID))

	return statusDeleting, nil
}

// collaboratorUsing returns a collaborator of another agent whose alias ARN
// belongs to agentID, or nil. Callers hold m.mu.
func (m *Mock) collaboratorUsing(agentID string) *driver.AgentCollaborator {
	marker := ":agent-alias/" + agentID + "/"

	for _, c := range m.collaborators.SortedValues() {
		if c.AgentID != agentID && strings.Contains(c.AliasARN, marker) {
			found := *c

			return &found
		}
	}

	return nil
}

// deleteAliasesForAgent removes every alias belonging to agentID. All() returns
// a snapshot, so deleting while ranging is safe.
func (m *Mock) deleteAliasesForAgent(agentID string) {
	for id, alias := range m.aliases.All() {
		if alias.AgentID == agentID {
			m.aliases.Delete(id)
			m.dropTags(alias.ARN)
		}
	}
}

// PrepareAgent prepares an agent, transitioning it to PREPARED. Like real
// Bedrock it does not cut a version: aliases do (see CreateAgentAlias).
func (m *Mock) PrepareAgent(_ context.Context, agentID string) (*driver.Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents.Get(agentID)
	if !ok {
		return nil, agentNotFound(agentID)
	}

	updated := cloneAgent(agent)
	updated.Status = driver.AgentPrepared
	updated.PreparedAt = m.now()
	updated.UpdatedAt = updated.PreparedAt
	m.agents.Set(agentID, &updated)

	result := cloneAgent(&updated)

	return &result, nil
}

// validateAgent checks the members CreateAgent requires and, for an update
// (which the model requires to carry them), foundationModel and
// agentResourceRoleArn too.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateAgent(cfg driver.AgentConfig, update bool) error {
	var v violations

	v.required("agentName", cfg.Name == "")

	if update {
		v.required("foundationModel", cfg.FoundationModel == "")
		v.required("agentResourceRoleArn", cfg.ResourceRoleArn == "")
	}

	v.enum("agentCollaboration", cfg.AgentCollaboration, agentCollaborationValues...)
	v.enum("orchestrationType", cfg.OrchestrationType, orchestrationTypeValues...)

	return v.err()
}

// markDraftChanged returns an agent's DRAFT to NOT_PREPARED after one of its
// action groups, knowledge-base associations or collaborators changed, as
// real Bedrock does. Callers hold m.mu.
func (m *Mock) markDraftChanged(agentID string) {
	agent, ok := m.agents.Get(agentID)
	if !ok {
		return
	}

	updated := cloneAgent(agent)
	updated.Status = driver.AgentNotPrepared
	updated.UpdatedAt = m.now()
	m.agents.Set(agentID, &updated)
}

// cloneAgent returns a value copy whose RawMessage members do not alias the
// stored agent. It reports the defaults for members an agent persisted before
// they existed lacks.
func cloneAgent(a *driver.Agent) driver.Agent {
	out := *a
	out.AgentCollaboration = orDefault(a.AgentCollaboration, driver.AgentCollaborationDisabled)
	out.OrchestrationType = orDefault(a.OrchestrationType, driver.OrchestrationTypeDefault)
	out.PromptOverrideConfiguration = copyRaw(a.PromptOverrideConfiguration)
	out.GuardrailConfiguration = copyRaw(a.GuardrailConfiguration)
	out.MemoryConfiguration = copyRaw(a.MemoryConfiguration)
	out.CustomOrchestration = copyRaw(a.CustomOrchestration)

	return out
}

func agentNotFound(agentID string) error {
	return errors.Newf(errors.NotFound, "agent %q not found", agentID)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

// rawOr returns a copy of v, or of fallback when v is absent.
func rawOr(v, fallback json.RawMessage) json.RawMessage {
	if isNull(v) {
		return copyRaw(fallback)
	}

	return copyRaw(v)
}
