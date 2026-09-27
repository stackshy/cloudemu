package bedrockagent

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

var tenCharID = regexp.MustCompile(`^[0-9A-Z]{10}$`)

func newVectorKB(t *testing.T, m *Mock, tags map[string]string) *driver.KnowledgeBase {
	t.Helper()

	kb, err := m.CreateKnowledgeBase(context.Background(), driver.KnowledgeBaseConfig{
		Name:                       "kb",
		RoleArn:                    "role",
		KnowledgeBaseConfiguration: json.RawMessage(`{"type":"VECTOR"}`),
		StorageConfiguration:       json.RawMessage(`{"type":"OPENSEARCH_SERVERLESS"}`),
		Tags:                       tags,
	})
	require.NoError(t, err)

	return kb
}

// TestTagsOnEveryTaggableResource covers create-time tags on each taggable
// type, their 10-character ids, and the ARN shapes the tagging API accepts.
func TestTagsOnEveryTaggableResource(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	tags := map[string]string{"k": "v"}

	agent, err := m.CreateAgent(ctx, driver.AgentConfig{Name: "a", Tags: tags})
	require.NoError(t, err)

	alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agent.ID, Name: "live", Tags: tags})
	require.NoError(t, err)

	kb := newVectorKB(t, m, tags)

	flow, err := m.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "role", Tags: tags})
	require.NoError(t, err)

	prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p", Tags: tags})
	require.NoError(t, err)

	for _, id := range []string{agent.ID, alias.ID, kb.ID, flow.ID, prompt.ID} {
		assert.Regexp(t, tenCharID, id)
	}

	for _, arn := range []string{agent.ARN, alias.ARN, kb.ARN, flow.ARN, prompt.ARN} {
		got, lerr := m.ListTagsForResource(ctx, arn)
		require.NoError(t, lerr, arn)
		assert.Equal(t, tags, got, arn)
	}
}

func TestTagMergeUntagAndCopyOut(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	flow, err := m.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "role"})
	require.NoError(t, err)

	got, err := m.ListTagsForResource(ctx, flow.ARN)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)

	require.NoError(t, m.TagResource(ctx, flow.ARN, map[string]string{"a": "1", "b": "2"}))
	require.NoError(t, m.TagResource(ctx, flow.ARN, map[string]string{"a": "9"}))

	got, err = m.ListTagsForResource(ctx, flow.ARN)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "9", "b": "2"}, got)

	got["a"] = "mutated"

	again, err := m.ListTagsForResource(ctx, flow.ARN)
	require.NoError(t, err)
	assert.Equal(t, "9", again["a"], "caller mutation must not reach stored tags")

	require.NoError(t, m.UntagResource(ctx, flow.ARN, []string{"a", "b", "absent"}))

	got, err = m.ListTagsForResource(ctx, flow.ARN)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, m.tags.Has(flow.ARN), "an emptied tag set is dropped")
}

func TestTagErrors(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
	require.NoError(t, err)

	err = m.TagResource(ctx, prompt.ARN, nil)
	assert.True(t, cerrors.IsInvalidArgument(err))
	assert.Contains(t, cerrors.Message(err), "Value null at 'tags'")

	err = m.UntagResource(ctx, prompt.ARN, nil)
	assert.True(t, cerrors.IsInvalidArgument(err))

	err = m.TagResource(ctx, prompt.ARN, map[string]string{strings.Repeat("k", 129): "v"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	err = m.TagResource(ctx, prompt.ARN, map[string]string{"k": strings.Repeat("v", 257)})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.CreateAgent(ctx, driver.AgentConfig{Name: "a", Tags: map[string]string{"": "v"}})
	assert.True(t, cerrors.IsInvalidArgument(err))

	// Well-formed but naming nothing: not found. Includes a flow alias and a
	// prompt version, which are not modeled yet.
	for _, arn := range []string{
		"arn:aws:bedrock:us-east-1:123456789012:agent/ZZZZZZZZZZ",
		"arn:aws:bedrock:us-east-1:123456789012:flow/ZZZZZZZZZZ/alias/YYYYYYYYYY",
		prompt.ARN + ":1",
	} {
		_, err = m.ListTagsForResource(ctx, arn)
		assert.True(t, cerrors.IsNotFound(err), arn)
	}

	// Not a taggable bedrock-agent ARN shape: validation error.
	for _, arn := range []string{
		"arn:aws:bedrock:us-east-1:123456789012:agent/short",
		"arn:aws:bedrock:us-east-1:123456789012:session/ABCDEFGHIJ",
		"arn:aws:eks:us-east-1:123456789012:cluster/c1",
		"not-an-arn",
	} {
		_, err = m.ListTagsForResource(ctx, arn)
		assert.True(t, cerrors.IsInvalidArgument(err), arn)
	}
}

// TestDeleteDropsTags covers that deleting a resource (and cascading to its
// aliases) forgets the tags, so its ARN stops resolving.
func TestDeleteDropsTags(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	tags := map[string]string{"k": "v"}

	agent, err := m.CreateAgent(ctx, driver.AgentConfig{Name: "a", Tags: tags})
	require.NoError(t, err)

	alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agent.ID, Name: "live", Tags: tags})
	require.NoError(t, err)

	kb := newVectorKB(t, m, tags)

	flow, err := m.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "role", Tags: tags})
	require.NoError(t, err)

	prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p", Tags: tags})
	require.NoError(t, err)

	_, err = m.DeleteAgent(ctx, agent.ID)
	require.NoError(t, err)
	_, err = m.DeleteKnowledgeBase(ctx, kb.ID)
	require.NoError(t, err)
	_, err = m.DeleteFlow(ctx, flow.ID)
	require.NoError(t, err)
	_, err = m.DeletePrompt(ctx, prompt.ID)
	require.NoError(t, err)

	assert.Zero(t, m.tags.Len(), "no tag entries survive their resources")

	for _, arn := range []string{agent.ARN, alias.ARN, kb.ARN, flow.ARN, prompt.ARN} {
		_, err = m.ListTagsForResource(ctx, arn)
		assert.True(t, cerrors.IsNotFound(err), arn)
	}
}

func TestConcurrentTagging(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			key := string(rune('a' + n))
			assert.NoError(t, m.TagResource(ctx, prompt.ARN, map[string]string{key: "v"}))
		}(i)
	}

	wg.Wait()

	got, err := m.ListTagsForResource(ctx, prompt.ARN)
	require.NoError(t, err)
	assert.Len(t, got, 20, "no concurrent merge is lost")
}
