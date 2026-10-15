package bedrockagent

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// AssociateAgentKnowledgeBase associates an existing knowledge base with an
// agent's DRAFT, ENABLED unless the caller says otherwise. A knowledge base is
// associated at most once.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) AssociateAgentKnowledgeBase(_ context.Context, cfg driver.AgentKnowledgeBaseConfig) (*driver.AgentKnowledgeBase, error) {
	if err := validateAgentKnowledgeBase(cfg, true); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.agents.Has(cfg.AgentID) {
		return nil, agentNotFound(cfg.AgentID)
	}

	if !m.knowledge.Has(cfg.KnowledgeBaseID) {
		return nil, errors.Newf(errors.NotFound, "knowledge base %q not found", cfg.KnowledgeBaseID)
	}

	key := versionKey(cfg.AgentID, driver.DraftVersion, cfg.KnowledgeBaseID)
	now := m.now()
	assoc := &driver.AgentKnowledgeBase{
		AgentID:         cfg.AgentID,
		AgentVersion:    driver.DraftVersion,
		KnowledgeBaseID: cfg.KnowledgeBaseID,
		Description:     cfg.Description,
		State:           orDefault(cfg.State, driver.AgentKnowledgeBaseEnabled),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if !m.agentKBs.SetIfAbsent(key, assoc) {
		return nil, errors.Newf(errors.AlreadyExists,
			"Knowledge base %s is already associated with agent %s.", cfg.KnowledgeBaseID, cfg.AgentID)
	}

	m.markDraftChanged(cfg.AgentID)

	result := *assoc

	return &result, nil
}

// GetAgentKnowledgeBase returns a knowledge-base association of any agent
// version.
func (m *Mock) GetAgentKnowledgeBase(_ context.Context, agentID, version, knowledgeBaseID string) (*driver.AgentKnowledgeBase, error) {
	if err := m.checkAgentChild(agentID, version, "knowledgeBaseId", knowledgeBaseID); err != nil {
		return nil, err
	}

	assoc, ok := m.agentKBs.Get(versionKey(agentID, version, knowledgeBaseID))
	if !ok {
		return nil, agentKBNotFound(knowledgeBaseID)
	}

	result := *assoc

	return &result, nil
}

// UpdateAgentKnowledgeBase changes the description or state of a DRAFT
// association; members the caller omits keep their value.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateAgentKnowledgeBase(_ context.Context, cfg driver.AgentKnowledgeBaseConfig) (*driver.AgentKnowledgeBase, error) {
	if err := validateAgentKnowledgeBase(cfg, false); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.agents.Has(cfg.AgentID) {
		return nil, agentNotFound(cfg.AgentID)
	}

	key := versionKey(cfg.AgentID, driver.DraftVersion, cfg.KnowledgeBaseID)

	assoc, ok := m.agentKBs.Get(key)
	if !ok {
		return nil, agentKBNotFound(cfg.KnowledgeBaseID)
	}

	updated := *assoc
	updated.Description = orDefault(cfg.Description, assoc.Description)
	updated.State = orDefault(cfg.State, assoc.State)
	updated.UpdatedAt = m.now()
	m.agentKBs.Set(key, &updated)
	m.markDraftChanged(cfg.AgentID)

	result := updated

	return &result, nil
}

// DisassociateAgentKnowledgeBase removes a knowledge base from the DRAFT.
func (m *Mock) DisassociateAgentKnowledgeBase(_ context.Context, agentID, version, knowledgeBaseID string) error {
	return deleteDraftChild(m, m.agentKBs, agentID, version, "knowledgeBaseId", knowledgeBaseID, agentKBNotFound)
}

// ListAgentKnowledgeBases lists one page of an agent version's knowledge-base
// associations.
func (m *Mock) ListAgentKnowledgeBases(_ context.Context, agentID, version string, page driver.Page) (
	[]driver.AgentKnowledgeBase, string, error,
) {
	if err := m.checkAgentVersion(agentID, version); err != nil {
		return nil, "", err
	}

	items := childrenOf(m.agentKBs, versionKey(agentID, version),
		func(a *driver.AgentKnowledgeBase) driver.AgentKnowledgeBase { return *a })

	return paginate(items, page)
}

// validateAgentKnowledgeBase checks an association's members; associate
// requires a description.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateAgentKnowledgeBase(cfg driver.AgentKnowledgeBaseConfig, associate bool) error {
	var v violations

	v.id("agentId", cfg.AgentID)
	v.draft(cfg.AgentVersion)
	v.id("knowledgeBaseId", cfg.KnowledgeBaseID)

	if associate {
		v.required("description", cfg.Description == "")
	}

	v.description(cfg.Description)
	v.enum("knowledgeBaseState", cfg.State, driver.AgentKnowledgeBaseEnabled, driver.AgentKnowledgeBaseDisabled)

	return v.err()
}

func agentKBNotFound(kbID string) error {
	return errors.Newf(errors.NotFound, "knowledge base %q is not associated with the agent", kbID)
}
