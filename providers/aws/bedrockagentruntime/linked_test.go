package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	bedrockprov "github.com/stackshy/cloudemu/v2/providers/aws/bedrock"
	baprov "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// linked is a runtime mock wired to real control-plane and model mocks.
type linked struct {
	rt    *Mock
	cp    *baprov.Mock
	clock *config.FakeClock
}

func newLinked(t *testing.T, opts ...config.Option) *linked {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(append([]config.Option{config.WithClock(clock)}, opts...)...)
	cp := baprov.New(o)
	bed := bedrockprov.New(o)
	rt := New(o)
	rt.SetControlPlane(cp)
	rt.SetModels(bed)
	rt.SetGuardrails(bed)

	return &linked{rt: rt, cp: cp, clock: clock}
}

func (l *linked) agent(t *testing.T, mutate func(*badriver.AgentConfig)) string {
	t.Helper()

	cfg := badriver.AgentConfig{
		Name: "a", FoundationModel: "anthropic.claude-3-sonnet-20240229-v1:0",
		Instruction: "You are a helpful agent that answers questions about orders.",
	}
	if mutate != nil {
		mutate(&cfg)
	}

	a, err := l.cp.CreateAgent(context.Background(), cfg)
	require.NoError(t, err)

	l.prepare(t, a.ID)

	return a.ID
}

// prepare prepares an agent's draft, as callers must before invoking it
// through TSTALIASID and again after every change to it.
func (l *linked) prepare(t *testing.T, agentID string) {
	t.Helper()

	_, err := l.cp.PrepareAgent(context.Background(), agentID)
	require.NoError(t, err)
}

func TestConcurrentTurnsKeepEveryTurn(t *testing.T) {
	l := newLinked(t)
	agentID := l.agent(t, nil)

	var wg sync.WaitGroup

	for range 50 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := l.rt.InvokeAgent(context.Background(), driver.InvokeAgentInput{
				AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "shared", InputText: "hi",
			})
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	conv, ok := l.rt.conversations.Get(agentID + "/TSTALIASID/shared")
	require.True(t, ok)
	assert.Len(t, conv.Turns, 50, "no turn lost to a concurrent turn")
}

func TestConcurrentEndAndUpdateSession(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()

	s, err := l.rt.CreateSession(ctx, driver.SessionConfig{})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if i == 25 {
				_, _ = l.rt.EndSession(ctx, s.ID)

				return
			}

			_, _ = l.rt.UpdateSession(ctx, s.ID, map[string]string{"i": "x"})
		}()
	}

	wg.Wait()

	got, err := l.rt.GetSession(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, driver.SessionEnded, got.Status, "an update never resurrects an ended session")
}

func TestSessionAndStepLimits(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()

	big := map[string]string{}
	for i := range 51 {
		big[string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}

	_, err := l.rt.CreateSession(ctx, driver.SessionConfig{Metadata: big})
	assert.True(t, cerrors.IsInvalidArgument(err), "at most 50 metadata entries")

	_, err = l.rt.CreateSession(ctx, driver.SessionConfig{EncryptionKeyARN: "not-a-key"})
	assert.True(t, cerrors.IsInvalidArgument(err), "encryptionKeyArn must be a KMS key ARN")

	_, err = l.rt.CreateSession(ctx, driver.SessionConfig{Tags: map[string]string{"": "v"}})
	assert.True(t, cerrors.IsInvalidArgument(err), "tag keys are 1..128 characters")

	tagged, err := l.rt.CreateSession(ctx, driver.SessionConfig{})
	require.NoError(t, err)

	many := map[string]string{}
	for i := range maxSessionTags + 1 {
		many[fmt.Sprintf("k%d", i)] = "v"
	}

	err = l.rt.TagResource(ctx, tagged.ARN, many)
	assert.True(t, cerrors.IsInvalidArgument(err), "one request carries at most 200 tags")

	delete(many, "k0")
	require.NoError(t, l.rt.TagResource(ctx, tagged.ARN, many))

	err = l.rt.TagResource(ctx, tagged.ARN, map[string]string{"extra": "v"})
	assert.Equal(t, cerrors.ResourceExhausted, cerrors.GetCode(err), "a session holds at most 200 tags")

	s, err := l.rt.CreateSession(ctx, driver.SessionConfig{})
	require.NoError(t, err)

	inv, err := l.rt.CreateInvocation(ctx, s.ID, "", "")
	require.NoError(t, err)

	payload := json.RawMessage(`{"contentBlocks":[{"text":"x"}]}`)
	for range maxSessionSteps {
		_, err := l.rt.PutInvocationStep(ctx, driver.InvocationStepInput{SessionID: s.ID, InvocationID: inv.ID, StepTime: "t", Payload: payload})
		require.NoError(t, err)
	}

	_, err = l.rt.PutInvocationStep(ctx, driver.InvocationStepInput{SessionID: s.ID, InvocationID: inv.ID, StepTime: "t", Payload: payload})
	assert.Equal(t, cerrors.ResourceExhausted, cerrors.GetCode(err), "a session holds at most 1000 steps")

	_, err = l.rt.CreateInvocation(ctx, s.ID, "not-a-uuid", "")
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = l.rt.PutInvocationStep(ctx, driver.InvocationStepInput{SessionID: s.ID, InvocationID: inv.ID})
	assert.True(t, cerrors.IsInvalidArgument(err), "step time and payload are required")
}

func TestConversationIdleTimeoutStartsNewSession(t *testing.T) {
	l := newLinked(t)
	agentID := l.agent(t, func(c *badriver.AgentConfig) {
		c.IdleSessionTTLInSeconds = 60
		c.MemoryConfiguration = json.RawMessage(`{"enabledMemoryTypes":["SESSION_SUMMARY"]}`)
	})
	ctx := context.Background()

	_, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "first", MemoryID: "m-1",
	})
	require.NoError(t, err)

	l.clock.Advance(2 * time.Minute)

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "second"})
	require.NoError(t, err)

	conv, _ := l.rt.conversations.Get(agentID + "/TSTALIASID/s1")
	assert.Len(t, conv.Turns, 1, "the idle conversation ended and a new one began")

	mem, _, err := l.rt.GetAgentMemory(ctx, agentID, "TSTALIASID", "m-1", driver.MemoryTypeSessionSummary, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, mem, 1, "ending by idle timeout writes the memory summary (default 30 days)")
}

func TestSnapshotRoundTripRuntime(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	agentID := l.agent(t, func(c *badriver.AgentConfig) {
		c.MemoryConfiguration = json.RawMessage(`{"enabledMemoryTypes":["SESSION_SUMMARY"]}`)
	})

	s, err := l.rt.CreateSession(ctx, driver.SessionConfig{Tags: map[string]string{"k": "v"}})
	require.NoError(t, err)

	inv, err := l.rt.CreateInvocation(ctx, s.ID, "", "d")
	require.NoError(t, err)

	step, err := l.rt.PutInvocationStep(ctx, driver.InvocationStepInput{
		SessionID: s.ID, InvocationID: inv.ID, StepTime: "2026-01-01T00:00:00Z", Payload: json.RawMessage(`{"contentBlocks":[]}`),
	})
	require.NoError(t, err)

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "c1", InputText: "x", MemoryID: "m1", EndSession: true,
	})
	require.NoError(t, err)

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "open", InputText: "y"})
	require.NoError(t, err)

	raw, err := l.rt.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := New(config.NewOptions())
	require.NoError(t, dst.Restore(ctx, raw))

	got, err := dst.GetSession(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, s.ARN, got.ARN)

	tags, err := dst.ListTagsForResource(ctx, s.ARN)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"k": "v"}, tags)

	_, err = dst.GetInvocationStep(ctx, s.ID, inv.ID, step)
	require.NoError(t, err)

	mem, _, err := dst.GetAgentMemory(ctx, agentID, "TSTALIASID", "m1", driver.MemoryTypeSessionSummary, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, mem, 1)
	assert.True(t, dst.conversations.Has(agentID+"/TSTALIASID/open"))
}

func TestRuntimeValidationEdges(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()

	_, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: "A", AgentAliasID: "B", SessionID: "x"})
	assert.True(t, cerrors.IsInvalidArgument(err), "sessionId is at least 2 characters")

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: "A", AgentAliasID: "B", SessionID: "s1", MemoryID: "m"})
	assert.True(t, cerrors.IsInvalidArgument(err), "memoryId is at least 2 characters")

	_, err = l.rt.InvokeInlineAgent(ctx, driver.InlineAgentInput{
		SessionID: "s1", FoundationModel: "m", Instruction: "You are a helpful agent that answers questions.", IdleSessionTTLInSeconds: 30,
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "idle TTL is 60..3600")

	_, _, err = l.rt.Rerank(ctx, driver.RerankInput{Query: "q", ModelARN: "m"})
	assert.True(t, cerrors.IsInvalidArgument(err), "at least one source")

	_, err = l.rt.OptimizePrompt(ctx, "", "m")
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = l.rt.OptimizePrompt(ctx, "x", "not a model!")
	assert.True(t, cerrors.IsInvalidArgument(err), "targetModelId must match the model id pattern")

	_, err = l.rt.GenerateQuery(ctx, "q", "arn:aws:bedrock:us-east-1:123456789012:knowledge-base/MISSING000")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = l.rt.AgenticRetrieveStream(ctx, driver.AgenticRetrieveInput{})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, _, err = l.rt.ListFlowExecutionEvents(ctx, "F", "A", "E", "Other", driver.Page{})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = l.rt.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{InputText: "x", Type: "KNOWLEDGE_BASE"})
	assert.True(t, cerrors.IsInvalidArgument(err), "a knowledge base and a model are required")

	_, _, err = l.rt.GetAgentMemory(ctx, "A", "B", "", "SESSION_SUMMARY", driver.Page{})
	assert.True(t, cerrors.IsInvalidArgument(err), "memoryId is required")

	err = l.rt.TagResource(ctx, "arn:aws:bedrock:us-east-1:123456789012:agent/ABCDEFGHIJ", map[string]string{"k": "v"})
	assert.True(t, cerrors.IsInvalidArgument(err), "only sessions are taggable")
}
