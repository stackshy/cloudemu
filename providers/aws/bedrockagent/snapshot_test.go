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

	gotPrompt, err := dst.GetPrompt(ctx, prompt.ID)
	require.NoError(t, err)
	assert.Equal(t, "p1", gotPrompt.Name)
}
