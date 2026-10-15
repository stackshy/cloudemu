package bedrockagent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func TestActionGroupValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "v")

	cases := map[string]driver.ActionGroupConfig{
		"missing name":     {AgentID: agentID, AgentVersion: driver.DraftVersion},
		"bad name":         {AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "has space"},
		"numbered version": {AgentID: agentID, AgentVersion: "1", Name: "g"},
		"bad state":        {AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "g", State: "ON"},
		"bad signature":    {AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "g", ParentActionGroupSignature: "AMAZON.Other"},
		"short token":      {AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "g", ClientToken: "abc"},
		"both schemas": {AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "g",
			APISchema: json.RawMessage(`{"payload":"openapi: 3.0.0"}`), FunctionSchema: json.RawMessage(`{"functions":[{"name":"f"}]}`)},
		"payload not a document": {AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "g", APISchema: json.RawMessage(`{"payload":"abc"}`)},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := m.CreateAgentActionGroup(ctx, cfg)
			assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)
		})
	}

	err := m.DeleteAgentActionGroup(ctx, agentID, "1", "XXXXXXXXXX")
	assert.True(t, cerrors.IsInvalidArgument(err), "deletes target DRAFT only")

	_, _, err = m.ListAgentActionGroups(ctx, agentID, "4", driver.Page{})
	assert.True(t, cerrors.IsNotFound(err), "version 4 does not exist")

	for name, payload := range map[string]string{
		"json": `{"openapi":"3.0.0","paths":{"/orders":{"get":{}}}}`,
		"yaml": "openapi: 3.0.0\npaths:\n  /orders:\n    get: {}\n",
	} {
		raw, _ := json.Marshal(map[string]string{"payload": payload})
		_, err = m.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{
			AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "ok_" + name, APISchema: raw,
		})
		assert.NoError(t, err, "an OpenAPI payload in %s is accepted", name)
	}
}

func TestActionGroupReadsDoNotAliasState(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "copy")

	params := map[string]string{"k": "v"}
	g, err := m.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{
		AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "g",
		ParentActionGroupSignature: "ANTHROPIC.Computer", ParentActionGroupSignatureParams: params,
		FunctionSchema: []byte(`{"functions":[]}`),
	})
	require.NoError(t, err)

	params["k"] = "caller"
	g.ParentActionGroupSignatureParams["k"] = "result"
	g.FunctionSchema[0] = 'X'

	got, err := m.GetAgentActionGroup(ctx, agentID, driver.DraftVersion, g.ID)
	require.NoError(t, err)
	assert.Equal(t, "v", got.ParentActionGroupSignatureParams["k"])
	assert.JSONEq(t, `{"functions":[]}`, string(got.FunctionSchema))
}

func TestConcurrentSameNameActionGroupOneWins(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "racy")

	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)

	for range 50 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := m.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{
				AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "same",
			})
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, 1, ok)
}

func TestUpdateActionGroupRename(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "ren")

	a, err := m.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "a"})
	require.NoError(t, err)

	_, err = m.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "b"})
	require.NoError(t, err)

	_, err = m.UpdateAgentActionGroup(ctx, a.ID, driver.ActionGroupConfig{AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "b"})
	assert.True(t, cerrors.IsAlreadyExists(err))

	got, err := m.UpdateAgentActionGroup(ctx, a.ID, driver.ActionGroupConfig{AgentID: agentID, AgentVersion: driver.DraftVersion, Name: "a"})
	require.NoError(t, err)
	assert.Equal(t, driver.ActionGroupEnabled, got.State, "an omitted state keeps its value")
}
