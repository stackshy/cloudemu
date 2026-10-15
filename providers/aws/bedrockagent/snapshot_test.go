package bedrockagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// TestSnapshotRoundTripBedrockAgent proves a snapshot/restore round-trip
// preserves agents, knowledge bases, and prompts under their original ids.
func TestSnapshotRoundTripBedrockAgent(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	agent, err := src.CreateAgent(ctx, driver.AgentConfig{Name: "a1", FoundationModel: "fm"})
	require.NoError(t, err)

	kb, err := src.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name:                       "kb1",
		RoleArn:                    "arn:aws:iam::000000000000:role/kb",
		KnowledgeBaseConfiguration: []byte(`{"type":"VECTOR"}`),
		StorageConfiguration:       []byte(`{"type":"OPENSEARCH_SERVERLESS"}`),
		Tags:                       map[string]string{"env": "test"},
	})
	require.NoError(t, err)

	prompt, err := src.CreatePrompt(ctx, driver.PromptConfig{Name: "p1"})
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newMock()
	require.NoError(t, dst.Restore(ctx, raw))

	gotAgent, err := dst.GetAgent(ctx, agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "a1", gotAgent.Name)
	assert.Equal(t, "fm", gotAgent.FoundationModel)
	assert.Equal(t, agent.ARN, gotAgent.ARN)

	gotKB, err := dst.GetKnowledgeBase(ctx, kb.ID)
	require.NoError(t, err)
	assert.Equal(t, "kb1", gotKB.Name)

	gotTags, err := dst.ListTagsForResource(ctx, kb.ARN)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "test"}, gotTags)

	gotPrompt, err := dst.GetPrompt(ctx, prompt.ID)
	require.NoError(t, err)
	assert.Equal(t, "p1", gotPrompt.Name)
}

// TestSnapshotRoundTripVersionsAndChildren proves the stores #1452 added
// survive a snapshot: versions, aliases and their routing, action groups,
// knowledge-base associations, collaborators, flow aliases, prompt versions,
// documents, resource policies and the version counters.
func TestSnapshotRoundTripVersionsAndChildren(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	sup, err := src.CreateAgent(ctx, driver.AgentConfig{Name: "sup", AgentCollaboration: "SUPERVISOR"})
	require.NoError(t, err)

	workerID := mustAgent(t, src, "worker")
	kb := mustKB(t, src, "kb")
	dsID := mustDataSource(t, src, kb.ID, "CUSTOM")

	_, err = src.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{AgentID: sup.ID, AgentVersion: driver.DraftVersion, Name: "g"})
	require.NoError(t, err)

	_, err = src.AssociateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, KnowledgeBaseID: kb.ID, Description: "d",
	})
	require.NoError(t, err)

	workerAlias, err := src.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: workerID, Name: "live"})
	require.NoError(t, err)

	col, err := src.AssociateAgentCollaborator(ctx, driver.AgentCollaboratorConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, AliasARN: workerAlias.ARN, Name: "w", Instruction: "i",
	})
	require.NoError(t, err)

	supAlias, err := src.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: sup.ID, Name: "prod"})
	require.NoError(t, err)

	flow := mustFlow(t, src)
	_, err = src.CreateFlowVersion(ctx, flow.ID, "")
	require.NoError(t, err)

	flowAlias, err := src.CreateFlowAlias(ctx, driver.FlowAliasConfig{
		FlowID: flow.ID, Name: "a", RoutingConfiguration: []driver.FlowAliasRoute{{FlowVersion: "1"}},
		Tags: map[string]string{"k": "v"},
	})
	require.NoError(t, err)

	prompt, err := src.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
	require.NoError(t, err)

	pv, err := src.CreatePromptVersion(ctx, prompt.ID, "", nil)
	require.NoError(t, err)

	_, err = src.IngestKnowledgeBaseDocuments(ctx, kb.ID, dsID, []driver.KnowledgeBaseDocument{{
		Content: []byte(`{"dataSourceType":"CUSTOM","custom":{"customDocumentIdentifier":{"id":"d1"},"sourceType":"IN_LINE"}}`),
	}})
	require.NoError(t, err)

	_, err = src.PutResourcePolicy(ctx, kb.ARN, `{"a":1}`, "")
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newMock()
	require.NoError(t, dst.Restore(ctx, raw))

	alias, err := dst.GetAgentAlias(ctx, sup.ID, supAlias.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", alias.RoutingConfiguration[0].AgentVersion)

	groups, _, err := dst.ListAgentActionGroups(ctx, sup.ID, "1", driver.Page{})
	require.NoError(t, err)
	assert.Len(t, groups, 1)

	_, err = dst.GetAgentKnowledgeBase(ctx, sup.ID, "1", kb.ID)
	require.NoError(t, err)

	_, err = dst.GetAgentCollaborator(ctx, sup.ID, "1", col.ID)
	require.NoError(t, err)

	tags, err := dst.ListTagsForResource(ctx, flowAlias.ARN)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"k": "v"}, tags)

	_, err = dst.GetPromptVersion(ctx, prompt.ID, pv.Version)
	require.NoError(t, err)

	docs, _, err := dst.ListKnowledgeBaseDocuments(ctx, kb.ID, dsID, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, docs, 1)

	_, err = dst.GetResourcePolicy(ctx, kb.ARN)
	require.NoError(t, err)

	// The counter survives too: the next alias cuts version 2, not 1.
	next, err := dst.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: sup.ID, Name: "next"})
	require.NoError(t, err)
	assert.Equal(t, "2", next.RoutingConfiguration[0].AgentVersion)
}

// TestRestoreLegacyAgentReportsDefaults covers state written before
// agentCollaboration and orchestrationType existed.
func TestRestoreLegacyAgentReportsDefaults(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	// An agent as an older build stored it, without the two members.
	src.agents.Set("LEGACY0001", &driver.Agent{ID: "LEGACY0001", Name: "old", Status: driver.AgentNotPrepared, Version: driver.DraftVersion})

	raw, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	m := newMock()
	require.NoError(t, m.Restore(ctx, raw))

	got, err := m.GetAgent(ctx, "LEGACY0001")
	require.NoError(t, err)
	assert.Equal(t, driver.AgentCollaborationDisabled, got.AgentCollaboration)
	assert.Equal(t, driver.OrchestrationTypeDefault, got.OrchestrationType)
}
