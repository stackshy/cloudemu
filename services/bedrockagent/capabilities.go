package bedrockagent

import (
	"context"
	"encoding/json"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// The methods in this file expose the optional driver capabilities (agent
// versions and aliases, action groups, knowledge-base associations,
// collaborators, ingestion jobs, flow and prompt versions, documents, flow
// validation, resource policies) through the same error-injection, rate-limit,
// latency, metrics and recording pipeline as the primary operations. A driver
// that does not implement a capability answers FailedPrecondition.

// capabilityOf returns the optional capability T of the wrapped driver.
func capabilityOf[T any](b *BedrockAgent, op string) (T, error) {
	c, ok := b.driver.(T)
	if !ok {
		var zero T

		return zero, cerrors.Newf(cerrors.FailedPrecondition, "the Bedrock Agent driver does not implement %s", op)
	}

	return c, nil
}

// call runs fn through the pipeline and returns its typed result.
func call[T any](ctx context.Context, b *BedrockAgent, op string, input any, fn func() (T, error)) (T, error) {
	out, err := b.do(ctx, op, input, func() (any, error) { return fn() })
	if err != nil {
		var zero T

		return zero, err
	}

	return out.(T), nil
}

// callErr runs an operation that returns only an error through the pipeline.
func callErr(ctx context.Context, b *BedrockAgent, op string, input any, fn func() error) error {
	_, err := b.do(ctx, op, input, func() (any, error) { return nil, fn() })

	return err
}

// GetAgentVersion returns a numbered version of an agent.
func (b *BedrockAgent) GetAgentVersion(ctx context.Context, agentID, version string) (*driver.Agent, error) {
	c, err := capabilityOf[driver.AgentVersions](b, "GetAgentVersion")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetAgentVersion", agentID, func() (*driver.Agent, error) {
		return c.GetAgentVersion(ctx, agentID, version)
	})
}

// ListAgentVersions lists one page of an agent's versions, DRAFT first.
func (b *BedrockAgent) ListAgentVersions(ctx context.Context, agentID string, page driver.Page) ([]driver.Agent, string, error) {
	c, err := capabilityOf[driver.AgentVersions](b, "ListAgentVersions")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListAgentVersions", agentID, func() ([]driver.Agent, string, error) {
		return c.ListAgentVersions(ctx, agentID, page)
	})
}

// DeleteAgentVersion deletes a numbered version of an agent and returns its status.
func (b *BedrockAgent) DeleteAgentVersion(
	ctx context.Context, agentID, version string, skipResourceInUseCheck bool,
) (string, error) {
	c, err := capabilityOf[driver.AgentVersions](b, "DeleteAgentVersion")
	if err != nil {
		return "", err
	}

	return call(ctx, b, "DeleteAgentVersion", agentID, func() (string, error) {
		return c.DeleteAgentVersion(ctx, agentID, version, skipResourceInUseCheck)
	})
}

// GetAgentAlias returns an alias of an agent.
func (b *BedrockAgent) GetAgentAlias(ctx context.Context, agentID, aliasID string) (*driver.AgentAlias, error) {
	c, err := capabilityOf[driver.AgentVersions](b, "GetAgentAlias")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetAgentAlias", aliasID, func() (*driver.AgentAlias, error) {
		return c.GetAgentAlias(ctx, agentID, aliasID)
	})
}

// ListAgentAliases lists one page of an agent's aliases.
func (b *BedrockAgent) ListAgentAliases(
	ctx context.Context, agentID string, page driver.Page,
) ([]driver.AgentAlias, string, error) {
	c, err := capabilityOf[driver.AgentVersions](b, "ListAgentAliases")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListAgentAliases", agentID, func() ([]driver.AgentAlias, string, error) {
		return c.ListAgentAliases(ctx, agentID, page)
	})
}

// UpdateAgentAlias updates an agent alias.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateAgentAlias(
	ctx context.Context, aliasID string, cfg driver.AgentAliasConfig,
) (*driver.AgentAlias, error) {
	c, err := capabilityOf[driver.AgentVersions](b, "UpdateAgentAlias")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "UpdateAgentAlias", cfg, func() (*driver.AgentAlias, error) {
		return c.UpdateAgentAlias(ctx, aliasID, cfg)
	})
}

// DeleteAgentAlias deletes an agent alias and returns its status.
func (b *BedrockAgent) DeleteAgentAlias(ctx context.Context, agentID, aliasID string) (string, error) {
	c, err := capabilityOf[driver.AgentVersions](b, "DeleteAgentAlias")
	if err != nil {
		return "", err
	}

	return call(ctx, b, "DeleteAgentAlias", aliasID, func() (string, error) {
		return c.DeleteAgentAlias(ctx, agentID, aliasID)
	})
}

// CreateAgentActionGroup adds an action group to an agent's DRAFT.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) CreateAgentActionGroup(ctx context.Context, cfg driver.ActionGroupConfig) (*driver.ActionGroup, error) {
	c, err := capabilityOf[driver.ActionGroups](b, "CreateAgentActionGroup")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "CreateAgentActionGroup", cfg, func() (*driver.ActionGroup, error) {
		return c.CreateAgentActionGroup(ctx, cfg)
	})
}

// GetAgentActionGroup returns an action group of an agent version.
func (b *BedrockAgent) GetAgentActionGroup(
	ctx context.Context, agentID, version, actionGroupID string,
) (*driver.ActionGroup, error) {
	c, err := capabilityOf[driver.ActionGroups](b, "GetAgentActionGroup")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetAgentActionGroup", actionGroupID, func() (*driver.ActionGroup, error) {
		return c.GetAgentActionGroup(ctx, agentID, version, actionGroupID)
	})
}

// UpdateAgentActionGroup updates a DRAFT action group.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateAgentActionGroup(
	ctx context.Context, actionGroupID string, cfg driver.ActionGroupConfig,
) (*driver.ActionGroup, error) {
	c, err := capabilityOf[driver.ActionGroups](b, "UpdateAgentActionGroup")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "UpdateAgentActionGroup", cfg, func() (*driver.ActionGroup, error) {
		return c.UpdateAgentActionGroup(ctx, actionGroupID, cfg)
	})
}

// DeleteAgentActionGroup removes an action group from the DRAFT.
func (b *BedrockAgent) DeleteAgentActionGroup(ctx context.Context, agentID, version, actionGroupID string) error {
	c, err := capabilityOf[driver.ActionGroups](b, "DeleteAgentActionGroup")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "DeleteAgentActionGroup", actionGroupID, func() error {
		return c.DeleteAgentActionGroup(ctx, agentID, version, actionGroupID)
	})
}

// ListAgentActionGroups lists one page of an agent version's action groups.
func (b *BedrockAgent) ListAgentActionGroups(
	ctx context.Context, agentID, version string, page driver.Page,
) ([]driver.ActionGroup, string, error) {
	c, err := capabilityOf[driver.ActionGroups](b, "ListAgentActionGroups")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListAgentActionGroups", agentID, func() ([]driver.ActionGroup, string, error) {
		return c.ListAgentActionGroups(ctx, agentID, version, page)
	})
}

// AssociateAgentKnowledgeBase associates a knowledge base with an agent's DRAFT.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) AssociateAgentKnowledgeBase(
	ctx context.Context, cfg driver.AgentKnowledgeBaseConfig,
) (*driver.AgentKnowledgeBase, error) {
	c, err := capabilityOf[driver.AgentKnowledgeBases](b, "AssociateAgentKnowledgeBase")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "AssociateAgentKnowledgeBase", cfg, func() (*driver.AgentKnowledgeBase, error) {
		return c.AssociateAgentKnowledgeBase(ctx, cfg)
	})
}

// GetAgentKnowledgeBase returns a knowledge-base association.
func (b *BedrockAgent) GetAgentKnowledgeBase(
	ctx context.Context, agentID, version, knowledgeBaseID string,
) (*driver.AgentKnowledgeBase, error) {
	c, err := capabilityOf[driver.AgentKnowledgeBases](b, "GetAgentKnowledgeBase")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetAgentKnowledgeBase", knowledgeBaseID, func() (*driver.AgentKnowledgeBase, error) {
		return c.GetAgentKnowledgeBase(ctx, agentID, version, knowledgeBaseID)
	})
}

// UpdateAgentKnowledgeBase updates a DRAFT knowledge-base association.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateAgentKnowledgeBase(
	ctx context.Context, cfg driver.AgentKnowledgeBaseConfig,
) (*driver.AgentKnowledgeBase, error) {
	c, err := capabilityOf[driver.AgentKnowledgeBases](b, "UpdateAgentKnowledgeBase")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "UpdateAgentKnowledgeBase", cfg, func() (*driver.AgentKnowledgeBase, error) {
		return c.UpdateAgentKnowledgeBase(ctx, cfg)
	})
}

// DisassociateAgentKnowledgeBase removes a knowledge base from the DRAFT.
func (b *BedrockAgent) DisassociateAgentKnowledgeBase(ctx context.Context, agentID, version, knowledgeBaseID string) error {
	c, err := capabilityOf[driver.AgentKnowledgeBases](b, "DisassociateAgentKnowledgeBase")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "DisassociateAgentKnowledgeBase", knowledgeBaseID, func() error {
		return c.DisassociateAgentKnowledgeBase(ctx, agentID, version, knowledgeBaseID)
	})
}

// ListAgentKnowledgeBases lists one page of an agent version's knowledge bases.
func (b *BedrockAgent) ListAgentKnowledgeBases(
	ctx context.Context, agentID, version string, page driver.Page,
) ([]driver.AgentKnowledgeBase, string, error) {
	c, err := capabilityOf[driver.AgentKnowledgeBases](b, "ListAgentKnowledgeBases")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListAgentKnowledgeBases", agentID, func() ([]driver.AgentKnowledgeBase, string, error) {
		return c.ListAgentKnowledgeBases(ctx, agentID, version, page)
	})
}

// DeleteAgentChecked deletes an agent unless another agent's collaborator uses
// one of its aliases, or skipResourceInUseCheck is set.
func (b *BedrockAgent) DeleteAgentChecked(ctx context.Context, agentID string, skipResourceInUseCheck bool) (string, error) {
	c, err := capabilityOf[driver.AgentInUseDeleter](b, "DeleteAgentChecked")
	if err != nil {
		return "", err
	}

	return call(ctx, b, "DeleteAgentChecked", agentID, func() (string, error) {
		return c.DeleteAgentChecked(ctx, agentID, skipResourceInUseCheck)
	})
}

// AssociateAgentCollaborator adds a collaborator to a supervisor agent's DRAFT.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) AssociateAgentCollaborator(
	ctx context.Context, cfg driver.AgentCollaboratorConfig,
) (*driver.AgentCollaborator, error) {
	c, err := capabilityOf[driver.AgentCollaborators](b, "AssociateAgentCollaborator")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "AssociateAgentCollaborator", cfg, func() (*driver.AgentCollaborator, error) {
		return c.AssociateAgentCollaborator(ctx, cfg)
	})
}

// GetAgentCollaborator returns a collaborator of an agent version.
func (b *BedrockAgent) GetAgentCollaborator(
	ctx context.Context, agentID, version, collaboratorID string,
) (*driver.AgentCollaborator, error) {
	c, err := capabilityOf[driver.AgentCollaborators](b, "GetAgentCollaborator")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetAgentCollaborator", collaboratorID, func() (*driver.AgentCollaborator, error) {
		return c.GetAgentCollaborator(ctx, agentID, version, collaboratorID)
	})
}

// UpdateAgentCollaborator updates a DRAFT collaborator.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateAgentCollaborator(
	ctx context.Context, collaboratorID string, cfg driver.AgentCollaboratorConfig,
) (*driver.AgentCollaborator, error) {
	c, err := capabilityOf[driver.AgentCollaborators](b, "UpdateAgentCollaborator")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "UpdateAgentCollaborator", cfg, func() (*driver.AgentCollaborator, error) {
		return c.UpdateAgentCollaborator(ctx, collaboratorID, cfg)
	})
}

// DisassociateAgentCollaborator removes a collaborator from the DRAFT.
func (b *BedrockAgent) DisassociateAgentCollaborator(ctx context.Context, agentID, version, collaboratorID string) error {
	c, err := capabilityOf[driver.AgentCollaborators](b, "DisassociateAgentCollaborator")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "DisassociateAgentCollaborator", collaboratorID, func() error {
		return c.DisassociateAgentCollaborator(ctx, agentID, version, collaboratorID)
	})
}

// ListAgentCollaborators lists one page of an agent version's collaborators.
func (b *BedrockAgent) ListAgentCollaborators(
	ctx context.Context, agentID, version string, page driver.Page,
) ([]driver.AgentCollaborator, string, error) {
	c, err := capabilityOf[driver.AgentCollaborators](b, "ListAgentCollaborators")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListAgentCollaborators", agentID, func() ([]driver.AgentCollaborator, string, error) {
		return c.ListAgentCollaborators(ctx, agentID, version, page)
	})
}

// GetIngestionJob returns an ingestion job.
func (b *BedrockAgent) GetIngestionJob(ctx context.Context, kbID, dsID, jobID string) (*driver.IngestionJob, error) {
	c, err := capabilityOf[driver.IngestionJobs](b, "GetIngestionJob")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetIngestionJob", jobID, func() (*driver.IngestionJob, error) {
		return c.GetIngestionJob(ctx, kbID, dsID, jobID)
	})
}

// ListIngestionJobs lists one page of a data source's ingestion jobs.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) ListIngestionJobs(
	ctx context.Context, in driver.ListIngestionJobsInput,
) ([]driver.IngestionJob, string, error) {
	c, err := capabilityOf[driver.IngestionJobs](b, "ListIngestionJobs")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListIngestionJobs", in, func() ([]driver.IngestionJob, string, error) {
		return c.ListIngestionJobs(ctx, in)
	})
}

// StopIngestionJob stops a running ingestion job.
func (b *BedrockAgent) StopIngestionJob(ctx context.Context, kbID, dsID, jobID string) (*driver.IngestionJob, error) {
	c, err := capabilityOf[driver.IngestionJobs](b, "StopIngestionJob")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "StopIngestionJob", jobID, func() (*driver.IngestionJob, error) {
		return c.StopIngestionJob(ctx, kbID, dsID, jobID)
	})
}

// CreateFlowVersion publishes a flow's DRAFT as a numbered version.
func (b *BedrockAgent) CreateFlowVersion(ctx context.Context, flowID, description string) (*driver.Flow, error) {
	c, err := capabilityOf[driver.FlowVersions](b, "CreateFlowVersion")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "CreateFlowVersion", flowID, func() (*driver.Flow, error) {
		return c.CreateFlowVersion(ctx, flowID, description)
	})
}

// GetFlowVersion returns a numbered version of a flow.
func (b *BedrockAgent) GetFlowVersion(ctx context.Context, flowID, version string) (*driver.Flow, error) {
	c, err := capabilityOf[driver.FlowVersions](b, "GetFlowVersion")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetFlowVersion", flowID, func() (*driver.Flow, error) {
		return c.GetFlowVersion(ctx, flowID, version)
	})
}

// ListFlowVersions lists one page of a flow's versions.
func (b *BedrockAgent) ListFlowVersions(ctx context.Context, flowID string, page driver.Page) ([]driver.Flow, string, error) {
	c, err := capabilityOf[driver.FlowVersions](b, "ListFlowVersions")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListFlowVersions", flowID, func() ([]driver.Flow, string, error) {
		return c.ListFlowVersions(ctx, flowID, page)
	})
}

// DeleteFlowVersion deletes a numbered version of a flow.
func (b *BedrockAgent) DeleteFlowVersion(ctx context.Context, flowID, version string, skipResourceInUseCheck bool) error {
	c, err := capabilityOf[driver.FlowVersions](b, "DeleteFlowVersion")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "DeleteFlowVersion", flowID, func() error {
		return c.DeleteFlowVersion(ctx, flowID, version, skipResourceInUseCheck)
	})
}

// CreateFlowAlias creates an alias of a flow.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) CreateFlowAlias(ctx context.Context, cfg driver.FlowAliasConfig) (*driver.FlowAlias, error) {
	c, err := capabilityOf[driver.FlowVersions](b, "CreateFlowAlias")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "CreateFlowAlias", cfg, func() (*driver.FlowAlias, error) {
		return c.CreateFlowAlias(ctx, cfg)
	})
}

// GetFlowAlias returns an alias of a flow.
func (b *BedrockAgent) GetFlowAlias(ctx context.Context, flowID, aliasID string) (*driver.FlowAlias, error) {
	c, err := capabilityOf[driver.FlowVersions](b, "GetFlowAlias")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetFlowAlias", aliasID, func() (*driver.FlowAlias, error) {
		return c.GetFlowAlias(ctx, flowID, aliasID)
	})
}

// ListFlowAliases lists one page of a flow's aliases.
func (b *BedrockAgent) ListFlowAliases(ctx context.Context, flowID string, page driver.Page) ([]driver.FlowAlias, string, error) {
	c, err := capabilityOf[driver.FlowVersions](b, "ListFlowAliases")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListFlowAliases", flowID, func() ([]driver.FlowAlias, string, error) {
		return c.ListFlowAliases(ctx, flowID, page)
	})
}

// UpdateFlowAlias updates a flow alias.
//
//nolint:gocritic // the argument matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateFlowAlias(
	ctx context.Context, aliasID string, cfg driver.FlowAliasConfig,
) (*driver.FlowAlias, error) {
	c, err := capabilityOf[driver.FlowVersions](b, "UpdateFlowAlias")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "UpdateFlowAlias", cfg, func() (*driver.FlowAlias, error) {
		return c.UpdateFlowAlias(ctx, aliasID, cfg)
	})
}

// DeleteFlowAlias deletes a flow alias and returns it.
func (b *BedrockAgent) DeleteFlowAlias(ctx context.Context, flowID, aliasID string) (*driver.FlowAlias, error) {
	c, err := capabilityOf[driver.FlowVersions](b, "DeleteFlowAlias")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "DeleteFlowAlias", aliasID, func() (*driver.FlowAlias, error) {
		return c.DeleteFlowAlias(ctx, flowID, aliasID)
	})
}

// CreatePromptVersion publishes a prompt's DRAFT as a numbered version.
func (b *BedrockAgent) CreatePromptVersion(
	ctx context.Context, promptID, description string, tags map[string]string,
) (*driver.Prompt, error) {
	c, err := capabilityOf[driver.PromptVersions](b, "CreatePromptVersion")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "CreatePromptVersion", promptID, func() (*driver.Prompt, error) {
		return c.CreatePromptVersion(ctx, promptID, description, tags)
	})
}

// GetPromptVersion returns DRAFT or a numbered version of a prompt.
func (b *BedrockAgent) GetPromptVersion(ctx context.Context, promptID, version string) (*driver.Prompt, error) {
	c, err := capabilityOf[driver.PromptVersions](b, "GetPromptVersion")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetPromptVersion", promptID, func() (*driver.Prompt, error) {
		return c.GetPromptVersion(ctx, promptID, version)
	})
}

// ListPromptVersions lists one page of a prompt's versions.
func (b *BedrockAgent) ListPromptVersions(
	ctx context.Context, promptID string, page driver.Page,
) ([]driver.Prompt, string, error) {
	c, err := capabilityOf[driver.PromptVersions](b, "ListPromptVersions")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListPromptVersions", promptID, func() ([]driver.Prompt, string, error) {
		return c.ListPromptVersions(ctx, promptID, page)
	})
}

// DeletePromptVersion deletes one numbered version of a prompt.
func (b *BedrockAgent) DeletePromptVersion(ctx context.Context, promptID, version string) error {
	c, err := capabilityOf[driver.PromptVersions](b, "DeletePromptVersion")
	if err != nil {
		return err
	}

	return callErr(ctx, b, "DeletePromptVersion", promptID, func() error {
		return c.DeletePromptVersion(ctx, promptID, version)
	})
}

// IngestKnowledgeBaseDocuments ingests documents into a data source.
func (b *BedrockAgent) IngestKnowledgeBaseDocuments(
	ctx context.Context, kbID, dsID string, docs []driver.KnowledgeBaseDocument,
) ([]driver.DocumentDetail, error) {
	c, err := capabilityOf[driver.KnowledgeBaseDocuments](b, "IngestKnowledgeBaseDocuments")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "IngestKnowledgeBaseDocuments", dsID, func() ([]driver.DocumentDetail, error) {
		return c.IngestKnowledgeBaseDocuments(ctx, kbID, dsID, docs)
	})
}

// GetKnowledgeBaseDocuments reports the status of documents in a data source.
func (b *BedrockAgent) GetKnowledgeBaseDocuments(
	ctx context.Context, kbID, dsID string, ids []driver.DocumentIdentifier,
) ([]driver.DocumentDetail, error) {
	c, err := capabilityOf[driver.KnowledgeBaseDocuments](b, "GetKnowledgeBaseDocuments")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetKnowledgeBaseDocuments", dsID, func() ([]driver.DocumentDetail, error) {
		return c.GetKnowledgeBaseDocuments(ctx, kbID, dsID, ids)
	})
}

// ListKnowledgeBaseDocuments lists one page of a data source's documents.
func (b *BedrockAgent) ListKnowledgeBaseDocuments(
	ctx context.Context, kbID, dsID string, page driver.Page,
) ([]driver.DocumentDetail, string, error) {
	c, err := capabilityOf[driver.KnowledgeBaseDocuments](b, "ListKnowledgeBaseDocuments")
	if err != nil {
		return nil, "", err
	}

	return listCall(ctx, b, "ListKnowledgeBaseDocuments", dsID, func() ([]driver.DocumentDetail, string, error) {
		return c.ListKnowledgeBaseDocuments(ctx, kbID, dsID, page)
	})
}

// DeleteKnowledgeBaseDocuments deletes documents from a data source.
func (b *BedrockAgent) DeleteKnowledgeBaseDocuments(
	ctx context.Context, kbID, dsID string, ids []driver.DocumentIdentifier,
) ([]driver.DocumentDetail, error) {
	c, err := capabilityOf[driver.KnowledgeBaseDocuments](b, "DeleteKnowledgeBaseDocuments")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "DeleteKnowledgeBaseDocuments", dsID, func() ([]driver.DocumentDetail, error) {
		return c.DeleteKnowledgeBaseDocuments(ctx, kbID, dsID, ids)
	})
}

// ValidateFlowDefinition checks a flow definition without storing it.
func (b *BedrockAgent) ValidateFlowDefinition(ctx context.Context, definition json.RawMessage) ([]driver.FlowValidation, error) {
	c, err := capabilityOf[driver.FlowValidator](b, "ValidateFlowDefinition")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "ValidateFlowDefinition", definition, func() ([]driver.FlowValidation, error) {
		return c.ValidateFlowDefinition(ctx, definition)
	})
}

// PutResourcePolicy attaches a resource policy to a knowledge base.
func (b *BedrockAgent) PutResourcePolicy(
	ctx context.Context, resourceARN, policy, expectedRevisionID string,
) (*driver.ResourcePolicy, error) {
	c, err := capabilityOf[driver.ResourcePolicies](b, "PutResourcePolicy")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "PutResourcePolicy", resourceARN, func() (*driver.ResourcePolicy, error) {
		return c.PutResourcePolicy(ctx, resourceARN, policy, expectedRevisionID)
	})
}

// GetResourcePolicy returns a knowledge base's resource policy.
func (b *BedrockAgent) GetResourcePolicy(ctx context.Context, resourceARN string) (*driver.ResourcePolicy, error) {
	c, err := capabilityOf[driver.ResourcePolicies](b, "GetResourcePolicy")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "GetResourcePolicy", resourceARN, func() (*driver.ResourcePolicy, error) {
		return c.GetResourcePolicy(ctx, resourceARN)
	})
}

// DeleteResourcePolicy removes a knowledge base's resource policy.
func (b *BedrockAgent) DeleteResourcePolicy(
	ctx context.Context, resourceARN, expectedRevisionID string,
) (*driver.ResourcePolicy, error) {
	c, err := capabilityOf[driver.ResourcePolicies](b, "DeleteResourcePolicy")
	if err != nil {
		return nil, err
	}

	return call(ctx, b, "DeleteResourcePolicy", resourceARN, func() (*driver.ResourcePolicy, error) {
		return c.DeleteResourcePolicy(ctx, resourceARN, expectedRevisionID)
	})
}
