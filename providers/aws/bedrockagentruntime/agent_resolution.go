package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"

	"github.com/stackshy/cloudemu/v2/errors"
	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Path-member constraints of the agent operations, from the service model.
const (
	agentIDPattern   = `^[0-9a-zA-Z]+$`
	sessionIDPattern = `^[0-9a-zA-Z._:-]+$`
	maxAgentIDLen    = 10
	minSessionIDLen  = 2
	maxSessionIDLen  = 100
	defaultMemDays   = 30
	rejectInvocation = "REJECT_INVOCATIONS"
)

var (
	agentIDRE   = regexp.MustCompile(agentIDPattern)
	sessionIDRE = regexp.MustCompile(sessionIDPattern)
)

// validateAgentRef checks the agentId, agentAliasId and sessionId members.
func validateAgentRef(agentID, aliasID, sessionID string) error {
	var v violations

	v.agentID("agentId", agentID)
	v.agentID("agentAliasId", aliasID)
	v.sessionID(sessionID)

	return v.err()
}

// resolveAgent loads the agent version an alias routes to, with its action
// groups and knowledge bases. TSTALIASID routes to DRAFT, which must be
// prepared (every change to the draft returns it to NOT_PREPARED).
func (m *Mock) resolveAgent(ctx context.Context, agentID, aliasID string) (*agentConfig, error) {
	agent, err := m.cp.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}

	cfg := &agentConfig{agentID: agentID, aliasID: aliasID, version: badriver.DraftVersion, operation: opInvokeAgent}

	if rerr := m.routeAlias(ctx, cfg); rerr != nil {
		return nil, rerr
	}

	if cfg.version != badriver.DraftVersion {
		if agent, err = m.cp.GetAgentVersion(ctx, agentID, cfg.version); err != nil {
			return nil, err
		}
	} else if agent.Status != badriver.AgentPrepared {
		return nil, errors.Newf(errors.InvalidArgument,
			"Agent %s is %s. Call PrepareAgent before you invoke its working draft (TSTALIASID).", agentID, agent.Status)
	}

	applyAgent(cfg, agent)

	if err := m.loadAgentChildren(ctx, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// routeAlias sets the alias ARN and the version the alias routes to:
// TSTALIASID routes to DRAFT; an alias rejecting invocations is refused.
func (m *Mock) routeAlias(ctx context.Context, cfg *agentConfig) error {
	if cfg.aliasID == badriver.AgentTestAliasID {
		cfg.aliasARN = m.aliasARN(cfg.agentID, cfg.aliasID)

		return nil
	}

	alias, err := m.cp.GetAgentAlias(ctx, cfg.agentID, cfg.aliasID)
	if err != nil {
		return err
	}

	if alias.InvocationState == rejectInvocation {
		return errors.Newf(errors.InvalidArgument,
			"Agent alias %s is not accepting invocations (aliasInvocationState is REJECT_INVOCATIONS).", cfg.aliasID)
	}

	cfg.aliasARN = alias.ARN

	if len(alias.RoutingConfiguration) > 0 && alias.RoutingConfiguration[0].AgentVersion != "" {
		cfg.version = alias.RoutingConfiguration[0].AgentVersion
	}

	return nil
}

// applyAgent copies an agent version's settings into cfg.
func applyAgent(cfg *agentConfig, agent *badriver.Agent) {
	cfg.name = agent.Name
	cfg.foundationModel = agent.FoundationModel
	cfg.instruction = agent.Instruction
	cfg.idleTTLSeconds = agent.IdleSessionTTLInSeconds

	var g guardrailConfigJSON
	if json.Unmarshal(agent.GuardrailConfiguration, &g) == nil && g.GuardrailIdentifier != "" {
		cfg.guardrail = &guardrailRef{id: g.GuardrailIdentifier, version: g.GuardrailVersion}
	}

	var po promptOverrideJSON
	if json.Unmarshal(agent.PromptOverrideConfiguration, &po) == nil {
		for _, pc := range po.PromptConfigurations {
			enabled := pc.PromptState == "ENABLED"

			switch pc.PromptType {
			case promptPre:
				cfg.preProcessing = enabled
			case promptPost:
				cfg.postProcessing = enabled
			}
		}
	}

	var mem memoryConfigJSON
	if json.Unmarshal(agent.MemoryConfiguration, &mem) == nil && slices.Contains(mem.EnabledMemoryTypes, "SESSION_SUMMARY") {
		cfg.memoryEnabled = true
		cfg.memoryDays = mem.StorageDays

		if cfg.memoryDays == 0 {
			cfg.memoryDays = defaultMemDays
		}
	}
}

// loadAgentChildren reads the version's enabled action groups and knowledge
// bases.
func (m *Mock) loadAgentChildren(ctx context.Context, cfg *agentConfig) error {
	page := badriver.Page{MaxResults: ptr(maxListPage)}

	groups, _, err := m.cp.ListAgentActionGroups(ctx, cfg.agentID, cfg.version, page)
	if err != nil {
		return err
	}

	for i := range groups {
		if g, ok := toActionGroup(&groups[i]); ok {
			cfg.actionGroups = append(cfg.actionGroups, g)
		}
	}

	kbs, _, err := m.cp.ListAgentKnowledgeBases(ctx, cfg.agentID, cfg.version, page)
	if err != nil {
		return err
	}

	for _, kb := range kbs {
		if kb.State != badriver.AgentKnowledgeBaseDisabled {
			cfg.knowledgeBases = append(cfg.knowledgeBases, knowledgeBaseRef{id: kb.KnowledgeBaseID})
		}
	}

	return nil
}

func ptr[T any](v T) *T { return &v }
