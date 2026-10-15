package bedrockagent

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func mustAgent(t *testing.T, m *Mock, name string) string {
	t.Helper()

	a, err := m.CreateAgent(context.Background(), driver.AgentConfig{Name: name, FoundationModel: "fm"})
	require.NoError(t, err)

	return a.ID
}

// TestConcurrentAliasCreatesCutDistinctVersions runs many alias creates at
// once: each must cut its own version number, none reused or lost.
func TestConcurrentAliasCreatesCutDistinctVersions(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "busy")

	const n = 50

	versions := make([]string, n)

	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()

			alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agentID, Name: "a" + strconv.Itoa(i)})
			assert.NoError(t, err)

			versions[i] = alias.RoutingConfiguration[0].AgentVersion
		}()
	}

	wg.Wait()

	seen := map[string]bool{}
	for _, v := range versions {
		assert.False(t, seen[v], "version %s cut twice", v)
		seen[v] = true
	}

	all, _, err := m.ListAgentVersions(ctx, agentID, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, all, n+1, "DRAFT plus one version per alias")
}

// TestConcurrentSameNameAliasOneWins checks the name-uniqueness scan and the
// insert are one step.
func TestConcurrentSameNameAliasOneWins(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "racy")

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		ok, clash int
	)

	for range 50 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agentID, Name: "prod"})

			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				ok++
			} else if cerrors.IsAlreadyExists(err) {
				clash++
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, 1, ok)
	assert.Equal(t, 49, clash)
}

func TestAliasReadsDoNotAliasState(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "copy")

	alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agentID, Name: "prod"})
	require.NoError(t, err)

	alias.RoutingConfiguration[0].AgentVersion = "999"
	alias.HistoryEvents[0].RoutingConfiguration[0].AgentVersion = "999"

	got, err := m.GetAgentAlias(ctx, agentID, alias.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.RoutingConfiguration[0].AgentVersion)
	assert.Equal(t, "1", got.HistoryEvents[0].RoutingConfiguration[0].AgentVersion)
}

func TestVersionSnapshotIsImmutable(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "snap")

	_, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agentID, Name: "prod"})
	require.NoError(t, err)

	_, err = m.UpdateAgent(ctx, agentID, driver.AgentConfig{
		Name: "renamed", FoundationModel: "fm2", ResourceRoleArn: "arn:aws:iam::123456789012:role/r",
	})
	require.NoError(t, err)

	v1, err := m.GetAgentVersion(ctx, agentID, "1")
	require.NoError(t, err)
	assert.Equal(t, "snap", v1.Name, "a version keeps the DRAFT it was cut from")
	assert.Equal(t, "fm", v1.FoundationModel)
}

func TestDeleteAgentCascadesVersionsAndChildren(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "gone")

	_, err := m.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "g"})
	require.NoError(t, err)

	_, err = m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agentID, Name: "prod"})
	require.NoError(t, err)

	_, err = m.DeleteAgent(ctx, agentID)
	require.NoError(t, err)

	assert.Empty(t, m.agentVersions.All())
	assert.Empty(t, m.actionGroups.All())
	assert.Empty(t, m.aliases.All())
	assert.Empty(t, m.versionSeq.All())
}

func TestAgentVersionErrors(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "errs")

	_, err := m.GetAgentVersion(ctx, agentID, driver.DraftVersion)
	assert.True(t, cerrors.IsInvalidArgument(err), "GetAgentVersion takes a number")

	_, err = m.GetAgentVersion(ctx, agentID, "3")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.DeleteAgentVersion(ctx, "MISSING000", "1", false)
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.UpdateAgentAlias(ctx, "MISSING000", driver.AgentAliasConfig{AgentID: agentID, Name: "x"})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.UpdateAgentAlias(ctx, "MISSING000", driver.AgentAliasConfig{
		AgentID: agentID, Name: "x", InvocationState: "PAUSED",
	})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.CreateAgentAlias(ctx, driver.AgentAliasConfig{
		AgentID: agentID, Name: "x",
		RoutingConfiguration: []driver.AgentAliasRoute{{AgentVersion: "1"}, {AgentVersion: "2"}},
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "at most one route")

	_, err = m.DeleteAgentAlias(ctx, agentID, "MISSING000")
	assert.True(t, cerrors.IsNotFound(err))
}

// TestAliasCutPreparesDraft follows Terraform's aws_bedrockagent_agent_alias:
// after CreateAgentAlias it polls GetAgent until the agent is PREPARED, so
// cutting a version from an unprepared DRAFT must prepare DRAFT too.
func TestAliasCutPreparesDraft(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "unprepared")

	agent, err := m.GetAgent(ctx, agentID)
	require.NoError(t, err)
	require.Equal(t, driver.AgentNotPrepared, agent.Status)

	alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agentID, Name: "live"})
	require.NoError(t, err)
	assert.Equal(t, "1", alias.RoutingConfiguration[0].AgentVersion)

	agent, err = m.GetAgent(ctx, agentID)
	require.NoError(t, err)
	assert.Equal(t, driver.AgentPrepared, agent.Status, "the waiter sees PREPARED at once")
	assert.NotEmpty(t, agent.PreparedAt)

	version, err := m.GetAgentVersion(ctx, agentID, "1")
	require.NoError(t, err)
	assert.Equal(t, agentVersionPrepared, version.Status)
}
