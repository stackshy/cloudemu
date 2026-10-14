package bedrockagentruntime

import (
	"context"
	"regexp"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// InvokeInlineAgent constraints from the service model.
const (
	minInstructionLen = 40
	minInlineTTL      = 60
	maxInlineTTL      = 3600
	inlineAgentID     = "INLINEAGENT"
	namePattern       = `^([0-9a-zA-Z][_-]?){1,100}$`
)

var nameRE = regexp.MustCompile(namePattern)

// InvokeInlineAgent runs an agent defined in the request: its model,
// instruction, action groups and knowledge bases. Conversations are kept per
// sessionId, like InvokeAgent's.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) InvokeInlineAgent(ctx context.Context, in driver.InlineAgentInput) (*driver.InvokeAgentResult, error) {
	if err := validateInline(&in); err != nil {
		return nil, err
	}

	cfg := &agentConfig{
		name: in.AgentName, agentID: inlineAgentID, aliasID: inlineAgentID, version: "DRAFT",
		foundationModel: in.FoundationModel, instruction: in.Instruction, idleTTLSeconds: in.IdleSessionTTLInSeconds,
		operation: opInvokeInlineAgent,
	}

	for i := range in.ActionGroups {
		if in.ActionGroups[i].ParentActionGroupSignature != "" {
			continue
		}

		cfg.actionGroups = append(cfg.actionGroups, inlineActionGroup(&in.ActionGroups[i]))
	}

	kbs, err := m.inlineKnowledgeBases(ctx, in.KnowledgeBases)
	if err != nil {
		return nil, err
	}

	cfg.knowledgeBases = kbs

	if err := applyKnowledgeBaseConfigs(cfg, in.SessionState); err != nil {
		return nil, err
	}

	if m.cp == nil {
		cfg.knowledgeBases = nil
	}

	if in.Guardrail != nil {
		cfg.guardrail = &guardrailRef{id: in.Guardrail.ID, version: in.Guardrail.Version}
	}

	return m.runTurn(ctx, cfg, turnRequest{
		sessionID: in.SessionID, inputText: in.InputText, enableTrace: in.EnableTrace,
		endSession: in.EndSession, state: in.SessionState,
	})
}

func inlineActionGroup(g *driver.InlineActionGroup) actionGroup {
	out := actionGroup{name: g.Name, description: g.Description, returnControl: g.ReturnControl, lambdaARN: g.LambdaARN}

	for _, f := range g.Functions {
		fd := functionDef{name: f.Name, description: f.Description}
		for _, p := range f.Parameters {
			fd.params = append(fd.params, paramDef{name: p.Name, typ: p.Type, required: p.Required})
		}

		out.functions = append(out.functions, fd)
	}

	out.apis = parseOpenAPI(g.APISchema)

	return out
}

func validateInline(in *driver.InlineAgentInput) error {
	var v violations

	v.sessionID(in.SessionID)
	v.required("foundationModel", in.FoundationModel == "")
	v.required("instruction", in.Instruction == "")

	if in.Instruction != "" && len(in.Instruction) < minInstructionLen {
		v.addf("Value '%s' at 'instruction' failed to satisfy constraint: Member must have length greater than or equal to %d",
			in.Instruction, minInstructionLen)
	}

	v.pattern("agentName", in.AgentName, nameRE, namePattern)
	v.enum("agentCollaboration", in.AgentCollaboration, "SUPERVISOR", "SUPERVISOR_ROUTER", "DISABLED")

	if t := in.IdleSessionTTLInSeconds; t != 0 && (t < minInlineTTL || t > maxInlineTTL) {
		v.addf("Value '%d' at 'idleSessionTTLInSeconds' failed to satisfy constraint: Member must have value between %d and %d",
			t, minInlineTTL, maxInlineTTL)
	}

	for _, g := range in.ActionGroups {
		v.required("actionGroups.member.actionGroupName", g.Name == "")
	}

	return v.err()
}

// inlineKnowledgeBases resolves an inline agent's knowledge bases (each must
// exist when the runtime is linked to the control plane) with their filters.
func (m *Mock) inlineKnowledgeBases(ctx context.Context, kbs []driver.InlineKnowledgeBase) ([]knowledgeBaseRef, error) {
	out := make([]knowledgeBaseRef, 0, len(kbs))

	for _, kb := range kbs {
		var v violations

		v.numberOfResults("knowledgeBases.member.retrievalConfiguration.vectorSearchConfiguration.numberOfResults", kb.NumberOfResults)

		if err := v.err(); err != nil {
			return nil, err
		}

		if m.cp != nil {
			if _, err := m.cp.GetKnowledgeBase(ctx, knowledgeBaseID(kb.KnowledgeBaseID)); err != nil {
				return nil, err
			}
		}

		filter, err := parseFilter(kb.Filter, "knowledgeBases.member.retrievalConfiguration.vectorSearchConfiguration.filter")
		if err != nil {
			return nil, err
		}

		out = append(out, knowledgeBaseRef{id: knowledgeBaseID(kb.KnowledgeBaseID), numberOfResults: kb.NumberOfResults, filter: filter})
	}

	return out, nil
}
