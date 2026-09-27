package bedrockagent

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// CreateAgent creates an agent in the NOT_PREPARED state.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateAgent(_ context.Context, cfg driver.AgentConfig) (*driver.Agent, error) {
	var v violations

	v.required("agentName", cfg.Name == "")

	if err := v.err(); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	id := newID(m.agents)
	now := m.now()

	ttl := cfg.IdleSessionTTLInSeconds
	if ttl == 0 {
		ttl = defaultIdleSessionTTL
	}

	agent := &driver.Agent{
		ID:                      id,
		ARN:                     idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "agent/"+id),
		Name:                    cfg.Name,
		ResourceRoleArn:         cfg.ResourceRoleArn,
		FoundationModel:         cfg.FoundationModel,
		Instruction:             cfg.Instruction,
		Description:             cfg.Description,
		Status:                  driver.AgentNotPrepared,
		Version:                 driver.DraftVersion,
		IdleSessionTTLInSeconds: ttl,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	m.agents.Set(id, agent)
	m.putTags(agent.ARN, cfg.Tags)

	result := *agent

	return &result, nil
}

// GetAgent returns an agent by ID.
func (m *Mock) GetAgent(_ context.Context, agentID string) (*driver.Agent, error) {
	agent, ok := m.agents.Get(agentID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "agent %q not found", agentID)
	}

	result := *agent

	return &result, nil
}

// ListAgents lists one page of agents.
func (m *Mock) ListAgents(_ context.Context, page driver.Page) ([]driver.Agent, string, error) {
	all := m.agents.SortedValues()
	out := make([]driver.Agent, 0, len(all))

	for _, a := range all {
		out = append(out, *a)
	}

	return paginate(out, page)
}

// UpdateAgent updates an agent's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateAgent(_ context.Context, agentID string, cfg driver.AgentConfig) (*driver.Agent, error) {
	var v violations

	v.required("agentName", cfg.Name == "")
	v.required("foundationModel", cfg.FoundationModel == "")
	v.required("agentResourceRoleArn", cfg.ResourceRoleArn == "")

	if err := v.err(); err != nil {
		return nil, err
	}

	agent, ok := m.agents.Get(agentID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "agent %q not found", agentID)
	}

	updated := *agent
	updated.Name = cfg.Name
	updated.ResourceRoleArn = cfg.ResourceRoleArn
	updated.FoundationModel = cfg.FoundationModel
	updated.Instruction = orDefault(cfg.Instruction, agent.Instruction)
	updated.Description = cfg.Description
	updated.Status = driver.AgentNotPrepared
	updated.UpdatedAt = m.now()

	if cfg.IdleSessionTTLInSeconds != 0 {
		updated.IdleSessionTTLInSeconds = cfg.IdleSessionTTLInSeconds
	}

	m.agents.Set(agentID, &updated)

	result := updated

	return &result, nil
}

// DeleteAgent deletes an agent and, cascading like real AWS, every alias that
// belongs to it, along with their tags.
func (m *Mock) DeleteAgent(_ context.Context, agentID string) (string, error) {
	agent, ok := m.agents.Get(agentID)
	if !ok {
		return "", errors.Newf(errors.NotFound, "agent %q not found", agentID)
	}

	m.agents.Delete(agentID)
	m.dropTags(agent.ARN)
	m.deleteAliasesForAgent(agentID)

	return statusDeleting, nil
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

// PrepareAgent prepares an agent, transitioning it to PREPARED.
func (m *Mock) PrepareAgent(_ context.Context, agentID string) (*driver.Agent, error) {
	agent, ok := m.agents.Get(agentID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "agent %q not found", agentID)
	}

	updated := *agent
	updated.Status = driver.AgentPrepared
	updated.PreparedAt = m.now()
	updated.UpdatedAt = updated.PreparedAt
	m.agents.Set(agentID, &updated)

	result := updated

	return &result, nil
}

// CreateAgentAlias creates an alias of an agent in the PREPARED state.
func (m *Mock) CreateAgentAlias(_ context.Context, cfg driver.AgentAliasConfig) (*driver.AgentAlias, error) {
	var v violations

	v.required("agentId", cfg.AgentID == "")
	v.required("agentAliasName", cfg.Name == "")

	if err := v.err(); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	if !m.agents.Has(cfg.AgentID) {
		return nil, errors.Newf(errors.NotFound, "agent %q not found", cfg.AgentID)
	}

	id := newID(m.aliases)
	now := m.now()
	alias := &driver.AgentAlias{
		ID:          id,
		ARN:         idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "agent-alias/"+cfg.AgentID+"/"+id),
		AgentID:     cfg.AgentID,
		Name:        cfg.Name,
		Description: cfg.Description,
		Status:      driver.AgentAliasPrepared,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.aliases.Set(id, alias)
	m.putTags(alias.ARN, cfg.Tags)

	result := *alias

	return &result, nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}
