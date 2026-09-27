package bedrockagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func requireValidation(t *testing.T, err error, want string) {
	t.Helper()

	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
	assert.Equal(t, want, cerrors.Message(err))
}

func TestRequiredMembers(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	agent, err := m.CreateAgent(ctx, driver.AgentConfig{Name: "a"})
	require.NoError(t, err)

	kb := newVectorKB(t, m, nil)

	flow, err := m.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "role"})
	require.NoError(t, err)

	prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
	require.NoError(t, err)

	null := func(field string) string {
		return "Value null at '" + field + "' failed to satisfy constraint: Member must not be null"
	}
	one := "1 validation error detected: "

	_, err = m.CreateAgent(ctx, driver.AgentConfig{})
	requireValidation(t, err, one+null("agentName"))

	_, err = m.UpdateAgent(ctx, agent.ID, driver.AgentConfig{})
	requireValidation(t, err, "3 validation errors detected: "+null("agentName")+"; "+
		null("foundationModel")+"; "+null("agentResourceRoleArn"))

	_, err = m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agent.ID})
	requireValidation(t, err, one+null("agentAliasName"))

	_, err = m.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{Name: "kb", RoleArn: "role"})
	requireValidation(t, err, one+null("knowledgeBaseConfiguration"))

	_, err = m.UpdateKnowledgeBase(ctx, kb.ID, driver.KnowledgeBaseConfig{
		Name: "kb", RoleArn: "role", KnowledgeBaseConfiguration: json.RawMessage(`null`),
	})
	requireValidation(t, err, one+null("knowledgeBaseConfiguration"))

	_, err = m.CreateDataSource(ctx, driver.DataSourceConfig{KnowledgeBaseID: kb.ID})
	requireValidation(t, err, "2 validation errors detected: "+null("name")+"; "+null("dataSourceConfiguration"))

	_, err = m.UpdateFlow(ctx, flow.ID, driver.FlowConfig{Name: "f"})
	requireValidation(t, err, one+null("executionRoleArn"))

	_, err = m.UpdatePrompt(ctx, prompt.ID, driver.PromptConfig{})
	requireValidation(t, err, one+null("name"))
}

func TestVectorKnowledgeBaseNeedsStorage(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name: "kb", RoleArn: "role", KnowledgeBaseConfiguration: json.RawMessage(`{"type":"VECTOR"}`),
	})
	assert.True(t, cerrors.IsInvalidArgument(err))

	// A non-vector base (Kendra, SQL) carries no vector store.
	_, err = m.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name: "kendra", RoleArn: "role", KnowledgeBaseConfiguration: json.RawMessage(`{"type":"KENDRA"}`),
	})
	require.NoError(t, err)
}

func TestListPagination(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for range 5 {
		_, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
		require.NoError(t, err)
	}

	two := int32(2)
	seen := map[string]bool{}
	token := ""
	pages := 0

	for {
		page, next, err := m.ListPrompts(ctx, driver.Page{MaxResults: &two, NextToken: token})
		require.NoError(t, err)

		pages++

		for i := range page {
			seen[page[i].ID] = true
		}

		if next == "" {
			break
		}

		token = next
	}

	assert.Equal(t, 3, pages)
	assert.Len(t, seen, 5)

	all, next, err := m.ListPrompts(ctx, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, all, 5)
	assert.Empty(t, next)
}

func TestListBounds(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	zero, over := int32(0), int32(1001)

	_, _, err := m.ListAgents(ctx, driver.Page{MaxResults: &zero})
	requireValidation(t, err, "1 validation error detected: Value '0' at 'maxResults' failed to satisfy constraint: "+
		"Member must have value greater than or equal to 1")

	_, _, err = m.ListFlows(ctx, driver.Page{MaxResults: &over})
	requireValidation(t, err, "1 validation error detected: Value '1001' at 'maxResults' failed to satisfy constraint: "+
		"Member must have value less than or equal to 1000")

	_, _, err = m.ListKnowledgeBases(ctx, driver.Page{NextToken: "garbage"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	// An empty collection returns an empty (non-nil) page.
	got, _, err := m.ListAgents(ctx, driver.Page{})
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}
