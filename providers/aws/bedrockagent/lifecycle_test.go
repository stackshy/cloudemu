package bedrockagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// TestAgentDeployLifecycle walks the alias and version operations end to end
// on the provider: deploy, re-route, redeploy, in-use checks, delete.
func TestAgentDeployLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "deploy")

	alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agentID, Name: "prod", Description: "d"})
	require.NoError(t, err)

	got, err := m.GetAgentAlias(ctx, agentID, alias.ID)
	require.NoError(t, err)
	assert.Equal(t, "prod", got.Name)

	test, err := m.GetAgentAlias(ctx, agentID, driver.AgentTestAliasID)
	require.NoError(t, err)
	assert.Equal(t, driver.AgentTestAliasName, test.Name)
	assert.Equal(t, driver.DraftVersion, test.RoutingConfiguration[0].AgentVersion)

	_, err = m.GetAgentAlias(ctx, "MISSING000", alias.ID)
	assert.True(t, cerrors.IsNotFound(err))

	aliases, next, err := m.ListAgentAliases(ctx, agentID, driver.Page{})
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, aliases, 2)
	assert.Equal(t, driver.AgentTestAliasID, aliases[0].ID)

	// Re-route without a version cuts version 2 and closes the first event.
	updated, err := m.UpdateAgentAlias(ctx, alias.ID, driver.AgentAliasConfig{
		AgentID: agentID, Name: "prod", InvocationState: "REJECT_INVOCATIONS",
	})
	require.NoError(t, err)
	assert.Equal(t, "2", updated.RoutingConfiguration[0].AgentVersion)
	assert.Equal(t, "REJECT_INVOCATIONS", updated.InvocationState)
	require.Len(t, updated.HistoryEvents, 2)
	assert.NotEmpty(t, updated.HistoryEvents[1].EndDate)

	// Same routing again records no new event.
	same, err := m.UpdateAgentAlias(ctx, alias.ID, driver.AgentAliasConfig{
		AgentID: agentID, Name: "prod", RoutingConfiguration: []driver.AgentAliasRoute{{AgentVersion: "2"}},
	})
	require.NoError(t, err)
	assert.Len(t, same.HistoryEvents, 2)
	assert.Equal(t, "REJECT_INVOCATIONS", same.InvocationState, "an omitted invocation state is kept")

	_, err = m.UpdateAgentAlias(ctx, alias.ID, driver.AgentAliasConfig{
		AgentID: agentID, Name: "prod", RoutingConfiguration: []driver.AgentAliasRoute{{AgentVersion: "9"}},
	})
	assert.True(t, cerrors.IsNotFound(err))

	other, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{
		AgentID: agentID, Name: "draft", RoutingConfiguration: []driver.AgentAliasRoute{{AgentVersion: driver.DraftVersion}},
	})
	require.NoError(t, err)

	_, err = m.UpdateAgentAlias(ctx, other.ID, driver.AgentAliasConfig{AgentID: agentID, Name: "prod"})
	assert.True(t, cerrors.IsAlreadyExists(err))

	_, err = m.DeleteAgentVersion(ctx, agentID, "2", false)
	assert.True(t, cerrors.IsFailedPrecondition(err), "version 2 is routed by prod")

	status, err := m.DeleteAgentVersion(ctx, agentID, "1", false)
	require.NoError(t, err)
	assert.Equal(t, statusDeleting, status)

	_, err = m.DeleteAgentVersion(ctx, agentID, "2", true)
	require.NoError(t, err)

	_, err = m.DeleteAgentAlias(ctx, agentID, driver.AgentTestAliasID)
	assert.True(t, cerrors.IsInvalidArgument(err))

	status, err = m.DeleteAgentAlias(ctx, agentID, alias.ID)
	require.NoError(t, err)
	assert.Equal(t, statusDeleting, status)

	_, _, err = m.ListAgentAliases(ctx, "MISSING000", driver.Page{})
	assert.True(t, cerrors.IsNotFound(err))
}

// TestAgentChildrenLifecycle covers the update and list paths of action
// groups, knowledge-base associations and collaborators.
func TestAgentChildrenLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	sup, err := m.CreateAgent(ctx, driver.AgentConfig{Name: "sup", AgentCollaboration: "SUPERVISOR_ROUTER"})
	require.NoError(t, err)

	kb := mustKB(t, m, "kb")
	workerID := mustAgent(t, m, "worker")

	alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: workerID, Name: "live"})
	require.NoError(t, err)

	group, err := m.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{AgentID: sup.ID, AgentVersion: driver.DraftVersion, Name: "g"})
	require.NoError(t, err)

	groups, _, err := m.ListAgentActionGroups(ctx, sup.ID, driver.DraftVersion, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, groups, 1)

	_, err = m.GetAgentActionGroup(ctx, sup.ID, driver.DraftVersion, "MISSING000")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.UpdateAgentActionGroup(ctx, "MISSING000", driver.ActionGroupConfig{AgentID: sup.ID, AgentVersion: driver.DraftVersion, Name: "x"})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.UpdateAgentActionGroup(ctx, group.ID, driver.ActionGroupConfig{AgentID: "MISSING000", AgentVersion: driver.DraftVersion, Name: "x"})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.AssociateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, KnowledgeBaseID: kb.ID, Description: "d",
	})
	require.NoError(t, err)

	assoc, err := m.UpdateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, KnowledgeBaseID: kb.ID, Description: "new",
	})
	require.NoError(t, err)
	assert.Equal(t, "new", assoc.Description)
	assert.Equal(t, driver.AgentKnowledgeBaseEnabled, assoc.State)

	kbs, _, err := m.ListAgentKnowledgeBases(ctx, sup.ID, driver.DraftVersion, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, kbs, 1)

	_, err = m.GetAgentKnowledgeBase(ctx, sup.ID, driver.DraftVersion, "MISSING000")
	assert.True(t, cerrors.IsNotFound(err))

	col, err := m.AssociateAgentCollaborator(ctx, driver.AgentCollaboratorConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, AliasARN: alias.ARN, Name: "w", Instruction: "i",
	})
	require.NoError(t, err)

	_, err = m.AssociateAgentCollaborator(ctx, driver.AgentCollaboratorConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, AliasARN: alias.ARN, Name: "v", Instruction: "i",
	})
	require.NoError(t, err)

	updated, err := m.UpdateAgentCollaborator(ctx, col.ID, driver.AgentCollaboratorConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, AliasARN: alias.ARN, Name: "w", Instruction: "new",
		RelayConversationHistory: driver.RelayToCollaborator,
	})
	require.NoError(t, err)
	assert.Equal(t, "new", updated.Instruction)

	_, err = m.UpdateAgentCollaborator(ctx, col.ID, driver.AgentCollaboratorConfig{
		AgentID: sup.ID, AgentVersion: driver.DraftVersion, AliasARN: alias.ARN, Name: "v", Instruction: "i",
	})
	assert.True(t, cerrors.IsAlreadyExists(err), "renaming onto another collaborator's name")

	cols, _, err := m.ListAgentCollaborators(ctx, sup.ID, driver.DraftVersion, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, cols, 2)

	_, err = m.GetAgentCollaborator(ctx, sup.ID, driver.DraftVersion, "MISSING000")
	assert.True(t, cerrors.IsNotFound(err))

	require.NoError(t, m.DisassociateAgentCollaborator(ctx, sup.ID, driver.DraftVersion, col.ID))
	assert.True(t, cerrors.IsNotFound(m.DisassociateAgentCollaborator(ctx, sup.ID, driver.DraftVersion, col.ID)))
	assert.True(t, cerrors.IsNotFound(m.DisassociateAgentCollaborator(ctx, "MISSING000", driver.DraftVersion, col.ID)))

	require.NoError(t, m.DisassociateAgentKnowledgeBase(ctx, sup.ID, driver.DraftVersion, kb.ID))
	require.NoError(t, m.DeleteAgentActionGroup(ctx, sup.ID, driver.DraftVersion, group.ID))
}

func TestFlowAliasLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	f := mustFlow(t, m)

	_, err := m.CreateFlowVersion(ctx, f.ID, "v1")
	require.NoError(t, err)

	got, err := m.GetFlowVersion(ctx, f.ARN, "1")
	require.NoError(t, err)
	assert.Equal(t, "v1", got.Description)

	_, err = m.GetFlowVersion(ctx, "MISSING000", "1")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.GetFlowVersion(ctx, f.ID, "2")
	assert.True(t, cerrors.IsNotFound(err))

	versions, _, err := m.ListFlowVersions(ctx, f.ID, driver.Page{})
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, driver.DraftVersion, versions[0].Version)

	_, _, err = m.ListFlowVersions(ctx, "MISSING000", driver.Page{})
	assert.True(t, cerrors.IsNotFound(err))

	limit := int32(3)
	alias, err := m.CreateFlowAlias(ctx, driver.FlowAliasConfig{
		FlowID: f.ID, Name: "live", RoutingConfiguration: []driver.FlowAliasRoute{{FlowVersion: "1"}},
		Concurrency: &driver.FlowAliasConcurrency{Type: driver.FlowConcurrencyManual, MaxConcurrency: &limit},
	})
	require.NoError(t, err)

	limit = 99
	byARN, err := m.GetFlowAlias(ctx, f.ID, alias.ARN)
	require.NoError(t, err)
	assert.Equal(t, int32(3), *byARN.Concurrency.MaxConcurrency, "the caller's pointer is not aliased")

	*byARN.Concurrency.MaxConcurrency = 50
	again, err := m.GetFlowAlias(ctx, f.ID, alias.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(3), *again.Concurrency.MaxConcurrency, "reads are copies")

	aliases, _, err := m.ListFlowAliases(ctx, f.ID, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, aliases, 1)

	_, _, err = m.ListFlowAliases(ctx, "MISSING000", driver.Page{})
	assert.True(t, cerrors.IsNotFound(err))

	assert.True(t, cerrors.IsFailedPrecondition(m.DeleteFlowVersion(ctx, f.ID, "1", false)))

	updated, err := m.UpdateFlowAlias(ctx, alias.ID, driver.FlowAliasConfig{
		FlowID: f.ID, Name: "live", RoutingConfiguration: []driver.FlowAliasRoute{{FlowVersion: driver.DraftVersion}},
	})
	require.NoError(t, err)
	assert.Nil(t, updated.Concurrency)

	_, err = m.UpdateFlowAlias(ctx, "MISSING000", driver.FlowAliasConfig{
		FlowID: f.ID, Name: "live", RoutingConfiguration: []driver.FlowAliasRoute{{FlowVersion: driver.DraftVersion}},
	})
	assert.True(t, cerrors.IsNotFound(err))

	require.NoError(t, m.DeleteFlowVersion(ctx, f.ID, "1", false))
	deleted, err := m.DeleteFlowAlias(ctx, f.ID, alias.ID)
	require.NoError(t, err)
	assert.Equal(t, alias.ID, deleted.ID)

	_, err = m.DeleteFlow(ctx, f.ID)
	require.NoError(t, err)
	assert.Empty(t, m.flowAliases.All())
}

func TestIngestionJobLifecycle(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(clock), config.WithAsyncSettle()))
	ctx := context.Background()
	kb := mustKB(t, m, "kb")
	dsID := mustDataSource(t, m, kb.ID, "S3")

	job, err := m.StartIngestionJob(ctx, kb.ID, dsID, "")
	require.NoError(t, err)
	assert.Equal(t, driver.IngestionJobInProgress, job.Status)

	_, err = m.StartIngestionJob(ctx, kb.ID, "MISSING000", "")
	assert.True(t, cerrors.IsNotFound(err))

	got, err := m.GetIngestionJob(ctx, kb.ID, dsID, job.ID)
	require.NoError(t, err)
	assert.Equal(t, driver.IngestionJobInProgress, got.Status)

	_, err = m.GetIngestionJob(ctx, kb.ID, "MISSING000", job.ID)
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.GetIngestionJob(ctx, kb.ID, dsID, "MISSING000")
	assert.True(t, cerrors.IsNotFound(err))

	inProgress, _, err := m.ListIngestionJobs(ctx, driver.ListIngestionJobsInput{
		KnowledgeBaseID: kb.ID, DataSourceID: dsID,
		Filters: []driver.IngestionJobFilter{{Attribute: "STATUS", Operator: "EQ", Values: []string{"IN_PROGRESS"}}},
	})
	require.NoError(t, err)
	assert.Len(t, inProgress, 1)

	_, _, err = m.ListIngestionJobs(ctx, driver.ListIngestionJobsInput{KnowledgeBaseID: kb.ID, DataSourceID: "MISSING000"})
	assert.True(t, cerrors.IsNotFound(err))

	stopped, err := m.StopIngestionJob(ctx, kb.ID, dsID, job.ID)
	require.NoError(t, err)
	assert.Equal(t, driver.IngestionJobStopped, stopped.Status)

	_, err = m.StopIngestionJob(ctx, kb.ID, dsID, job.ID)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	_, err = m.StopIngestionJob(ctx, kb.ID, dsID, "MISSING000")
	assert.True(t, cerrors.IsNotFound(err))

	clock.Advance(time.Minute)

	desc, _, err := m.ListIngestionJobs(ctx, driver.ListIngestionJobsInput{
		KnowledgeBaseID: kb.ID, DataSourceID: dsID, SortBy: &driver.IngestionJobSort{Attribute: "STARTED_AT", Order: "DESCENDING"},
	})
	require.NoError(t, err)
	assert.Len(t, desc, 1)
	assert.Equal(t, driver.IngestionJobStopped, desc[0].Status, "a stopped job stays stopped after the window")
}

func TestPromptAndDocumentPaths(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	p, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
	require.NoError(t, err)

	v1, err := m.CreatePromptVersion(ctx, p.ARN, "first", map[string]string{"k": "v"})
	require.NoError(t, err)

	_, err = m.CreatePromptVersion(ctx, "MISSING000", "", nil)
	assert.True(t, cerrors.IsNotFound(err))

	draft, err := m.GetPromptVersion(ctx, p.ID, driver.DraftVersion)
	require.NoError(t, err)
	assert.Equal(t, driver.DraftVersion, draft.Version)

	viaARN, err := m.GetPrompt(ctx, v1.ARN)
	require.NoError(t, err)
	assert.Equal(t, "first", viaARN.Description)

	_, err = m.GetPromptVersion(ctx, "MISSING000", "1")
	assert.True(t, cerrors.IsNotFound(err))

	versions, _, err := m.ListPromptVersions(ctx, p.ID, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, versions, 2)

	assert.True(t, cerrors.IsNotFound(m.DeletePromptVersion(ctx, "MISSING000", "1")))

	_, err = m.DeletePrompt(ctx, p.ID)
	require.NoError(t, err)
	assert.Empty(t, m.promptVersions.All())
	assert.Empty(t, m.tags.All(), "version tags go with the prompt")

	kb := mustKB(t, m, "kb")
	custom := mustDataSource(t, m, kb.ID, "CUSTOM")
	id := driver.DocumentIdentifier{DataSourceType: "CUSTOM", CustomID: "d1"}

	_, err = m.IngestKnowledgeBaseDocuments(ctx, kb.ID, custom, []driver.KnowledgeBaseDocument{{
		Content: []byte(`{"dataSourceType":"CUSTOM","custom":{"customDocumentIdentifier":{"id":"d1"},"sourceType":"IN_LINE"}}`),
	}})
	require.NoError(t, err)

	got, err := m.GetKnowledgeBaseDocuments(ctx, kb.ID, custom, []driver.DocumentIdentifier{id})
	require.NoError(t, err)
	assert.Equal(t, driver.DocumentIndexed, got[0].Status)

	_, err = m.GetKnowledgeBaseDocuments(ctx, kb.ID, custom, []driver.DocumentIdentifier{{DataSourceType: "CUSTOM"}})
	assert.True(t, cerrors.IsInvalidArgument(err), "a CUSTOM identifier needs an id")

	_, _, err = m.ListKnowledgeBaseDocuments(ctx, kb.ID, "MISSING000", driver.Page{})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.DeleteDataSource(ctx, kb.ID, custom)
	require.NoError(t, err)
	assert.Empty(t, m.documents.All(), "documents go with their data source")

	policy, err := m.PutResourcePolicy(ctx, kb.ARN, `{}`, "")
	require.NoError(t, err)

	removed, err := m.DeleteResourcePolicy(ctx, kb.ARN, policy.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, policy.RevisionID, removed.RevisionID)

	_, err = m.GetResourcePolicy(ctx, "arn:aws:bedrock:us-east-1:123456789012:knowledge-base/MISSING000")
	assert.True(t, cerrors.IsNotFound(err))
}

// TestReviewFixes pins the fixes from the self-review: no resurrection after a
// racing delete, malformed ids are validation errors, flow versions report
// Prepared, and an unknown agent is reported as such.
func TestReviewFixes(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for range 200 {
		f := mustFlow(t, m)

		var wg sync.WaitGroup

		wg.Add(2)

		go func() { defer wg.Done(); _, _ = m.PrepareFlow(ctx, f.ID) }()
		go func() { defer wg.Done(); _, _ = m.DeleteFlow(ctx, f.ID) }()

		wg.Wait()

		_, err := m.GetFlow(ctx, f.ID)
		require.True(t, cerrors.IsNotFound(err), "PrepareFlow must not bring back a deleted flow")
	}

	kb := mustKB(t, m, "kb")
	dsID := mustDataSource(t, m, kb.ID, "S3")

	for range 100 {
		var wg sync.WaitGroup

		wg.Add(2)

		go func() { defer wg.Done(); _, _ = m.StartIngestionJob(ctx, kb.ID, dsID, "") }()
		go func() { defer wg.Done(); _, _ = m.DeleteDataSource(ctx, kb.ID, dsID) }()

		wg.Wait()

		for _, job := range m.jobs.All() {
			require.True(t, m.dataSource.Has(job.DataSourceID), "no job outlives its data source")
		}

		dsID = mustDataSource(t, m, kb.ID, "S3")
	}

	agentID := mustAgent(t, m, "a")

	_, err := m.GetAgentActionGroup(ctx, agentID, driver.DraftVersion, "bad")
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.GetAgentAlias(ctx, "bad", "ABCDEFGHIJ")
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, _, err = m.ListAgentKnowledgeBases(ctx, "bad", driver.DraftVersion, driver.Page{})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.DeleteAgentAlias(ctx, "MISSING000", "ABCDEFGHIJ")
	assert.Contains(t, cerrors.Message(err), "agent \"MISSING000\" not found")

	f := mustFlow(t, m)

	v, err := m.CreateFlowVersion(ctx, f.ID, "")
	require.NoError(t, err)
	assert.Equal(t, driver.FlowPrepared, v.Status)
}
