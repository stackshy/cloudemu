package bedrockagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func newMock() *Mock {
	clock := config.NewFakeClock(time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(clock)))
}

func TestAgentLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	agent, err := m.CreateAgent(ctx, driver.AgentConfig{Name: "a1", FoundationModel: "fm"})
	require.NoError(t, err)
	assert.Equal(t, driver.AgentNotPrepared, agent.Status)
	assert.Equal(t, driver.DraftVersion, agent.Version)
	assert.Equal(t, defaultIdleSessionTTL, agent.IdleSessionTTLInSeconds)
	assert.NotEmpty(t, agent.ARN)

	prepared, err := m.PrepareAgent(ctx, agent.ID)
	require.NoError(t, err)
	assert.Equal(t, driver.AgentPrepared, prepared.Status)
	assert.NotEmpty(t, prepared.PreparedAt)

	updated, err := m.UpdateAgent(ctx, agent.ID, driver.AgentConfig{Name: "a1-new"})
	require.NoError(t, err)
	assert.Equal(t, "a1-new", updated.Name)
	assert.Equal(t, "fm", updated.FoundationModel) // preserved

	agents, err := m.ListAgents(ctx)
	require.NoError(t, err)
	assert.Len(t, agents, 1)

	_, err = m.DeleteAgent(ctx, agent.ID)
	require.NoError(t, err)

	_, err = m.GetAgent(ctx, agent.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestCreateAgentValidation(t *testing.T) {
	_, err := newMock().CreateAgent(context.Background(), driver.AgentConfig{})
	assert.True(t, cerrors.IsInvalidArgument(err))
}

func TestAgentAliasRequiresAgent(t *testing.T) {
	m := newMock()

	_, err := m.CreateAgentAlias(context.Background(), driver.AgentAliasConfig{AgentID: "nope", Name: "x"})
	assert.True(t, cerrors.IsNotFound(err))
}

func TestKnowledgeBaseAndDataSource(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	kb, err := m.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name:                       "kb1",
		RoleArn:                    "role",
		KnowledgeBaseConfiguration: json.RawMessage(`{"type":"VECTOR"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, driver.KnowledgeBaseActive, kb.Status)

	ds, err := m.CreateDataSource(ctx, driver.DataSourceConfig{
		KnowledgeBaseID:         kb.ID,
		Name:                    "ds1",
		DataSourceConfiguration: json.RawMessage(`{"type":"S3"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, driver.DataSourceAvailable, ds.Status)
	assert.Equal(t, kb.ID, ds.KnowledgeBaseID)

	job, err := m.StartIngestionJob(ctx, kb.ID, ds.ID, "reindex")
	require.NoError(t, err)
	assert.Equal(t, driver.IngestionJobComplete, job.Status)

	sources, err := m.ListDataSources(ctx, kb.ID)
	require.NoError(t, err)
	assert.Len(t, sources, 1)

	_, err = m.DeleteDataSource(ctx, kb.ID, ds.ID)
	require.NoError(t, err)

	_, err = m.GetDataSource(ctx, kb.ID, ds.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestDeleteKnowledgeBaseCascade(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	kb, err := m.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name:                       "kb1",
		RoleArn:                    "role",
		KnowledgeBaseConfiguration: json.RawMessage(`{"type":"VECTOR"}`),
	})
	require.NoError(t, err)

	ds, err := m.CreateDataSource(ctx, driver.DataSourceConfig{
		KnowledgeBaseID:         kb.ID,
		Name:                    "ds1",
		DataSourceConfiguration: json.RawMessage(`{"type":"S3"}`),
	})
	require.NoError(t, err)

	job, err := m.StartIngestionJob(ctx, kb.ID, ds.ID, "reindex")
	require.NoError(t, err)

	_, err = m.DeleteKnowledgeBase(ctx, kb.ID)
	require.NoError(t, err)

	_, err = m.GetDataSource(ctx, kb.ID, ds.ID)
	assert.True(t, cerrors.IsNotFound(err), "data source should be cascade-deleted")

	sources, err := m.ListDataSources(ctx, kb.ID)
	require.NoError(t, err)
	assert.Empty(t, sources)

	assert.False(t, m.jobs.Has(job.ID), "ingestion job should be cascade-deleted")
}

func TestDeleteDataSourceCascadesJobs(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	kb, err := m.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name:                       "kb1",
		RoleArn:                    "role",
		KnowledgeBaseConfiguration: json.RawMessage(`{"type":"VECTOR"}`),
	})
	require.NoError(t, err)

	ds, err := m.CreateDataSource(ctx, driver.DataSourceConfig{
		KnowledgeBaseID:         kb.ID,
		Name:                    "ds1",
		DataSourceConfiguration: json.RawMessage(`{"type":"S3"}`),
	})
	require.NoError(t, err)

	job, err := m.StartIngestionJob(ctx, kb.ID, ds.ID, "reindex")
	require.NoError(t, err)

	_, err = m.DeleteDataSource(ctx, kb.ID, ds.ID)
	require.NoError(t, err)

	assert.False(t, m.jobs.Has(job.ID), "ingestion job should be cascade-deleted")
	// The knowledge base itself must survive its data source's deletion.
	assert.True(t, m.knowledge.Has(kb.ID))
}

func TestDeleteAgentCascadesAliases(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	agent, err := m.CreateAgent(ctx, driver.AgentConfig{Name: "a1", FoundationModel: "fm"})
	require.NoError(t, err)

	alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agent.ID, Name: "prod"})
	require.NoError(t, err)
	require.True(t, m.aliases.Has(alias.ID))

	_, err = m.DeleteAgent(ctx, agent.ID)
	require.NoError(t, err)

	assert.False(t, m.aliases.Has(alias.ID), "alias should be cascade-deleted")
}

func TestDataSourceCopyOutImmutable(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	kb, err := m.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name:                       "kb1",
		RoleArn:                    "role",
		KnowledgeBaseConfiguration: json.RawMessage(`{"type":"VECTOR"}`),
	})
	require.NoError(t, err)

	original := `{"type":"S3"}`
	ds, err := m.CreateDataSource(ctx, driver.DataSourceConfig{
		KnowledgeBaseID:         kb.ID,
		Name:                    "ds1",
		DataSourceConfiguration: json.RawMessage(original),
	})
	require.NoError(t, err)

	got, err := m.GetDataSource(ctx, kb.ID, ds.ID)
	require.NoError(t, err)

	// Mutate the returned config bytes; stored state must not change.
	for i := range got.DataSourceConfiguration {
		got.DataSourceConfiguration[i] = 'X'
	}

	again, err := m.GetDataSource(ctx, kb.ID, ds.ID)
	require.NoError(t, err)
	assert.JSONEq(t, original, string(again.DataSourceConfiguration))
}

func TestDataSourceRequiresKnowledgeBase(t *testing.T) {
	m := newMock()

	_, err := m.CreateDataSource(context.Background(), driver.DataSourceConfig{
		KnowledgeBaseID:         "missing",
		Name:                    "ds1",
		DataSourceConfiguration: json.RawMessage(`{"type":"S3"}`),
	})
	assert.True(t, cerrors.IsNotFound(err))
}

func TestFlowLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	flow, err := m.CreateFlow(ctx, driver.FlowConfig{Name: "f1", ExecutionRoleArn: "role"})
	require.NoError(t, err)
	assert.Equal(t, driver.FlowNotPrepared, flow.Status)

	prepared, err := m.PrepareFlow(ctx, flow.ID)
	require.NoError(t, err)
	assert.Equal(t, driver.FlowPrepared, prepared.Status)

	id, err := m.DeleteFlow(ctx, flow.ID)
	require.NoError(t, err)
	assert.Equal(t, flow.ID, id)

	_, err = m.GetFlow(ctx, flow.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestPromptLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p1"})
	require.NoError(t, err)
	assert.Equal(t, driver.DraftVersion, prompt.Version)

	updated, err := m.UpdatePrompt(ctx, prompt.ID, driver.PromptConfig{Name: "p1-new"})
	require.NoError(t, err)
	assert.Equal(t, "p1-new", updated.Name)

	prompts, err := m.ListPrompts(ctx)
	require.NoError(t, err)
	assert.Len(t, prompts, 1)

	_, err = m.DeletePrompt(ctx, prompt.ID)
	require.NoError(t, err)

	_, err = m.GetPrompt(ctx, prompt.ID)
	assert.True(t, cerrors.IsNotFound(err))
}
