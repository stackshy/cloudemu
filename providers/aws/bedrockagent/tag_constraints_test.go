package bedrockagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func TestTagCharacterPatterns(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
	require.NoError(t, err)

	// Every character the model allows is accepted.
	require.NoError(t, m.TagResource(ctx, prompt.ARN, map[string]string{"a1 ._:/=+@-": "b2 ._:/=+@-", "empty": ""}))

	err = m.TagResource(ctx, prompt.ARN, map[string]string{"bad#key": "v"})
	requireValidation(t, err, "1 validation error detected: Value 'bad#key' at 'tags' failed to satisfy constraint: "+
		"Map keys must satisfy constraint: [Member must satisfy regular expression pattern: "+tagCharPattern+"]")

	err = m.TagResource(ctx, prompt.ARN, map[string]string{"k": "bad*value", "k2#": "v"})
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(cerrors.Message(err), "2 validation errors detected: "), cerrors.Message(err))

	_, err = m.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "role", Tags: map[string]string{"k": "v!"}})
	assert.True(t, cerrors.IsInvalidArgument(err))
}

func TestUntagKeyLimits(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p", Tags: map[string]string{"k": "v"}})
	require.NoError(t, err)

	keys := make([]string, maxUntagKeys)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}

	require.NoError(t, m.UntagResource(ctx, prompt.ARN, keys), "200 keys is the limit, not over it")

	err = m.UntagResource(ctx, prompt.ARN, append(keys, "one-more"))
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))
	assert.True(t, strings.HasPrefix(cerrors.Message(err), "1 validation error detected: Value '[k0, k1"), cerrors.Message(err))
	assert.Contains(t, cerrors.Message(err), "at 'tagKeys' failed to satisfy constraint: Member must have length less than or equal to 200")

	err = m.UntagResource(ctx, prompt.ARN, []string{"ok", "", "bad#"})
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(cerrors.Message(err), "2 validation errors detected: "), cerrors.Message(err))
}

// TestTagRaceWithDelete stresses TagResource and create-time tags against
// deletes. However the calls interleave, no tag entry may outlive its
// resource, since an orphan would be written into snapshots.
func TestTagRaceWithDelete(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	tags := map[string]string{"k": "v"}

	var wg sync.WaitGroup

	for range 50 {
		prompt, err := m.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
		require.NoError(t, err)

		agent, err := m.CreateAgent(ctx, driver.AgentConfig{Name: "a"})
		require.NoError(t, err)

		alias, err := m.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agent.ID, Name: "live"})
		require.NoError(t, err)

		wg.Add(4)

		go func() {
			defer wg.Done()

			_ = m.TagResource(ctx, prompt.ARN, tags)
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DeletePrompt(ctx, prompt.ID)
		}()

		go func() {
			defer wg.Done()

			_ = m.TagResource(ctx, alias.ARN, tags)
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DeleteAgent(ctx, agent.ID)
		}()
	}

	// Create-time tags racing a delete of the same resource.
	for range 50 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			flow, err := m.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "role", Tags: tags})
			if err == nil {
				_, _ = m.DeleteFlow(ctx, flow.ID)
			}
		}()
	}

	wg.Wait()

	for arn := range m.tags.All() {
		assert.True(t, m.arnExists(arn), "orphan tags for %s", arn)
	}
}
