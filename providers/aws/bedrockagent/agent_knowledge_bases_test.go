package bedrockagent

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func mustKB(t *testing.T, m *Mock, name string) *driver.KnowledgeBase {
	t.Helper()

	kb, err := m.CreateKnowledgeBase(context.Background(), driver.KnowledgeBaseConfig{
		Name: name, RoleArn: "arn:aws:iam::123456789012:role/kb",
		KnowledgeBaseConfiguration: []byte(`{"type":"VECTOR"}`),
		StorageConfiguration:       []byte(`{"type":"OPENSEARCH_SERVERLESS","opensearchServerlessConfiguration":{}}`),
	})
	require.NoError(t, err)

	return kb
}

func TestAgentKnowledgeBaseValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "a")
	kb := mustKB(t, m, "kb")

	_, err := m.AssociateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{
		AgentID: agentID, AgentVersion: driver.DraftVersion, KnowledgeBaseID: kb.ID,
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "description is required")

	_, err = m.AssociateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{
		AgentID: agentID, AgentVersion: driver.DraftVersion, KnowledgeBaseID: kb.ID, Description: "d", State: "ON",
	})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.UpdateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{
		AgentID: agentID, AgentVersion: driver.DraftVersion, KnowledgeBaseID: kb.ID,
	})
	assert.True(t, cerrors.IsNotFound(err))

	err = m.DisassociateAgentKnowledgeBase(ctx, agentID, driver.DraftVersion, kb.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestConcurrentAssociateSameKnowledgeBaseOneWins(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	agentID := mustAgent(t, m, "a")
	kb := mustKB(t, m, "kb")

	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)

	for range 50 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := m.AssociateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{
				AgentID: agentID, AgentVersion: driver.DraftVersion, KnowledgeBaseID: kb.ID, Description: "d",
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
