package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	s3prov "github.com/stackshy/cloudemu/v2/providers/aws/s3"
	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// kb creates a knowledge base with a CUSTOM data source holding docs.
func (l *linked) kb(t *testing.T, docs map[string]string) (kbID, dsID string) {
	t.Helper()

	ctx := context.Background()

	kb, err := l.cp.CreateKnowledgeBase(ctx, badriver.KnowledgeBaseConfig{
		Name: "kb", RoleArn: "arn:aws:iam::123456789012:role/r",
		KnowledgeBaseConfiguration: json.RawMessage(`{"type":"VECTOR"}`),
		StorageConfiguration:       json.RawMessage(`{"type":"PINECONE"}`),
	})
	require.NoError(t, err)

	ds, err := l.cp.CreateDataSource(ctx, badriver.DataSourceConfig{
		KnowledgeBaseID: kb.ID, Name: "ds", DataSourceConfiguration: json.RawMessage(`{"type":"CUSTOM"}`),
	})
	require.NoError(t, err)

	for id, text := range docs {
		content, _ := json.Marshal(map[string]any{"dataSourceType": "CUSTOM", "custom": map[string]any{
			"customDocumentIdentifier": map[string]string{"id": id}, "sourceType": "IN_LINE",
			"inlineContent": map[string]any{"type": "TEXT", "textContent": map[string]string{"data": text}},
		}})

		_, err = l.cp.IngestKnowledgeBaseDocuments(ctx, kb.ID, ds.ID, []badriver.KnowledgeBaseDocument{{Content: content}})
		require.NoError(t, err)
	}

	return kb.ID, ds.ID
}

// fakeLambda answers every invoke with body, or fails; call runs first when
// set, like a function that calls back into the runtime.
type fakeLambda struct {
	body    string
	fail    string
	payload []byte
	name    string
	call    func()
}

func (f *fakeLambda) Invoke(_ context.Context, in serverlessdriver.InvokeInput) (*serverlessdriver.InvokeOutput, error) {
	if f.call != nil {
		f.call()
	}

	f.payload, f.name = in.Payload, in.FunctionName
	if f.fail != "" {
		return &serverlessdriver.InvokeOutput{Error: f.fail}, nil
	}

	return &serverlessdriver.InvokeOutput{Payload: []byte(f.body)}, nil
}

// recordingMonitor keeps every metric datum.
type recordingMonitor struct {
	mondriver.Monitoring

	data []mondriver.MetricDatum
}

func (r *recordingMonitor) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	r.data = append(r.data, data...)

	return nil
}

func TestAgentTurnFeatures(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	lam := &fakeLambda{body: `{"response":{"functionResponse":{"responseBody":{"TEXT":{"body":"sunny"}}}}}`}
	mon := &recordingMonitor{}
	l.rt.SetLambda(lam)
	l.rt.SetMonitoring(mon)

	kbID, _ := l.kb(t, map[string]string{"r": "Refunds take 5 days."})
	agentID := l.agent(t, nil)

	_, err := l.cp.AssociateAgentKnowledgeBase(ctx, badriver.AgentKnowledgeBaseConfig{
		AgentID: agentID, AgentVersion: "DRAFT", KnowledgeBaseID: kbID, Description: "faq",
	})
	require.NoError(t, err)

	_, err = l.cp.CreateAgentActionGroup(ctx, badriver.ActionGroupConfig{
		AgentID: agentID, AgentVersion: "DRAFT", Name: "tools",
		Executor:       json.RawMessage(`{"lambda":"arn:aws:lambda:us-east-1:123456789012:function:weather:live"}`),
		FunctionSchema: json.RawMessage(`{"functions":[{"name":"get_weather","parameters":{"city":{"type":"string"}}}]}`),
	})
	require.NoError(t, err)

	_, err = l.cp.CreateAgentActionGroup(ctx, badriver.ActionGroupConfig{
		AgentID: agentID, AgentVersion: "DRAFT", Name: "orders",
		Executor:  json.RawMessage(`{"lambda":"arn:aws:lambda:us-east-1:123456789012:function:weather:live"}`),
		APISchema: json.RawMessage(`{"payload":"{\"paths\":{\"/orders\":{\"get\":{}}}}"}`),
	})
	require.NoError(t, err)
	l.prepare(t, agentID)

	res, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "get weather city: Rome refunds",
		EnableTrace: true, SessionState: &driver.SessionState{SessionAttributes: map[string]string{"u": "1"}},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Completion, "sunny")
	assert.Contains(t, res.Completion, "Refunds take 5 days.")
	assert.Equal(t, "weather", lam.name)

	var ev lambdaEvent
	require.NoError(t, json.Unmarshal(lam.payload, &ev))
	assert.Equal(t, []lambdaParameter{{Name: "city", Type: "string", Value: "Rome"}}, ev.Parameters)
	assert.Equal(t, map[string]string{"u": "1"}, ev.SessionAttributes)

	kinds := map[string]bool{}
	for _, e := range res.Events {
		if e.Trace != nil {
			kinds[e.Trace.Part+"/"+e.Trace.ObservationType] = true
		}
	}

	assert.True(t, kinds["observation/ACTION_GROUP"] && kinds["observation/KNOWLEDGE_BASE"] && kinds["observation/FINISH"])
	assert.True(t, kinds["rationale/"] && kinds["invocationInput/"] && kinds["modelInvocationInput/"] && kinds["modelInvocationOutput/"])

	// API action groups: the path is picked when the input names it.
	api := pickAction([]actionGroup{{name: "g", apis: []apiDef{{path: "/orders", method: "GET"}}}}, "list my orders")
	require.NotNil(t, api)
	assert.Equal(t, "GET /orders", api.name())

	lam.fail = "Unhandled"
	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s2", InputText: "get weather"})
	assert.Equal(t, cerrors.Unavailable, cerrors.GetCode(err), "a failing Lambda is a dependency failure")

	names := map[string]bool{}
	for _, d := range mon.data {
		names[d.MetricName] = true
	}

	assert.True(t, names[metricInvocations] && names[metricServerErrors] && names[metricInputTokens])

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: "MISSING000", AgentAliasID: "TSTALIASID", SessionID: "s3"})
	require.True(t, cerrors.IsNotFound(err))
}

func TestReturnControlForApiAndAbandon(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	agentID := l.agent(t, nil)

	_, err := l.cp.CreateAgentActionGroup(ctx, badriver.ActionGroupConfig{
		AgentID: agentID, AgentVersion: "DRAFT", Name: "orders",
		Executor:  json.RawMessage(`{"customControl":"RETURN_CONTROL"}`),
		APISchema: json.RawMessage(`{"payload":"{\"paths\":{\"/orders\":{\"post\":{}}}}"}`),
	})
	require.NoError(t, err)
	l.prepare(t, agentID)

	first, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "create orders"})
	require.NoError(t, err)
	require.NotNil(t, first.Events[len(first.Events)-1].ReturnControl)
	assert.Equal(t, "POST", first.Events[len(first.Events)-1].ReturnControl.Inputs[0].HTTPMethod)

	// A new input abandons the pending call and answers normally.
	second, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "hello"})
	require.NoError(t, err)
	assert.Contains(t, second.Completion, "hello")

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1",
		SessionState: &driver.SessionState{InvocationID: "x", ReturnControlResults: []driver.InvocationResult{{ResponseBody: "b"}}},
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "nothing is pending any more")
}

func TestInlineAgentAndGuardrail(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	kbID, _ := l.kb(t, map[string]string{"a": "Paris is in France."})

	res, err := l.rt.InvokeInlineAgent(ctx, driver.InlineAgentInput{
		SessionID: "inline", FoundationModel: "anthropic.claude-3-sonnet-20240229-v1:0",
		Instruction:    "You are a helpful travel agent that answers questions.",
		InputText:      "where is paris",
		KnowledgeBases: []driver.InlineKnowledgeBase{{KnowledgeBaseID: kbID, NumberOfResults: ptr(int32(1))}},
		ActionGroups: []driver.InlineActionGroup{
			{Name: "user", ParentActionGroupSignature: "AMAZON.UserInput"},
			{Name: "rc", ReturnControl: true, Functions: []driver.FunctionDef{{Name: "book", Parameters: []driver.ParameterDef{{Name: "city"}}}}},
		},
		Guardrail: &driver.GuardrailRef{ID: "MISSING", Version: "DRAFT"},
	})
	assert.True(t, cerrors.IsNotFound(err), "an unknown guardrail is not found: %v", err)
	assert.Nil(t, res)

	res, err = l.rt.InvokeInlineAgent(ctx, driver.InlineAgentInput{
		SessionID: "inline", FoundationModel: "anthropic.claude-3-sonnet-20240229-v1:0",
		Instruction:    "You are a helpful travel agent that answers questions.",
		InputText:      "where is paris",
		KnowledgeBases: []driver.InlineKnowledgeBase{{KnowledgeBaseID: kbID}},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Completion, "Paris is in France.")

	_, err = l.rt.InvokeInlineAgent(ctx, driver.InlineAgentInput{
		SessionID: "inline", FoundationModel: "m", Instruction: "You are a helpful travel agent that answers questions.",
		KnowledgeBases: []driver.InlineKnowledgeBase{{KnowledgeBaseID: "MISSING000"}},
	})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = l.rt.InvokeInlineAgent(ctx, driver.InlineAgentInput{
		SessionID: "inline", FoundationModel: "not-a-model", Instruction: "You are a helpful travel agent that answers questions.",
		InputText: "x",
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "an unknown model can't be invoked")

	// Guardrails: the guardrail service decides; a nil service applies nothing.
	action, msg, err := l.rt.applyGuardrail(ctx, nil, guardrailInput, "x")
	require.NoError(t, err)
	assert.Empty(t, action+msg)

	l.rt.SetGuardrails(fakeGuardrails{})
	action, msg, err = l.rt.applyGuardrail(ctx, &driver.GuardrailRef{ID: "g"}, guardrailOutput, "x")
	require.NoError(t, err)
	assert.Equal(t, guardrailIntervened, action)
	assert.Equal(t, "no", msg)
}

type fakeGuardrails struct{}

func (fakeGuardrails) ApplyGuardrail(context.Context, bedrockdriver.ApplyGuardrailInput) (*bedrockdriver.ApplyGuardrailOutput, error) {
	return &bedrockdriver.ApplyGuardrailOutput{Action: bedrockdriver.GuardrailActionIntervened, Outputs: []string{"no"}}, nil
}

func TestRetrievalFeatures(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	kbID, dsID := l.kb(t, map[string]string{"r": "Refunds take 5 days.", "s": "Shipping takes 2 days."})

	res, err := l.rt.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: "arn:aws:bedrock:us-east-1:123456789012:knowledge-base/" + kbID, QueryText: "refunds"})
	require.NoError(t, err)
	require.Len(t, res.Results, 2)
	assert.Equal(t, "r", res.Results[0].CustomDocumentID)

	_, err = l.rt.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: kbID, QueryText: "x", NumberOfResults: ptr(int32(0))})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = l.rt.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: kbID, QueryText: "x", NextToken: "garbage"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	rag, err := l.rt.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{
		InputText: "refunds", Type: driver.RAGKnowledgeBase, KnowledgeBaseID: kbID, NumberOfResults: ptr(int32(1)),
		ModelARN:  "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-sonnet-20240229-v1:0",
		Guardrail: &driver.GuardrailRef{ID: "MISSING"},
	})
	assert.True(t, cerrors.IsNotFound(err), "an unknown guardrail is not found")
	assert.Nil(t, rag)

	rag, err = l.rt.RetrieveAndGenerateStream(ctx, driver.RetrieveAndGenerateInput{
		InputText: "refunds", Type: driver.RAGExternalSources, ModelARN: "anthropic.claude-3-sonnet-20240229-v1:0",
		ExternalSources: []driver.ExternalSource{{SourceType: "S3", S3URI: "s3://b/k", Text: "refunds fast"}},
	})
	require.NoError(t, err)
	require.Len(t, rag.Citations, 1)
	assert.Equal(t, driver.LocationS3, rag.Citations[0].References[0].LocationType)

	legacy, err := l.rt.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{InputText: "q", SessionID: "keep-me"})
	require.NoError(t, err)
	assert.Equal(t, "keep-me", legacy.SessionID)

	doc, err := l.rt.GetDocumentContent(ctx, kbID, dsID, "r", "RAW")
	require.NoError(t, err)

	body, mime, err := l.rt.ReadDocumentContent(ctx, doc.Token)
	require.NoError(t, err)
	assert.Equal(t, "Refunds take 5 days.", string(body))
	assert.Equal(t, "text/plain", mime)

	l.clock.Advance(16 * time.Minute)

	_, _, err = l.rt.ReadDocumentContent(ctx, doc.Token)
	assert.True(t, cerrors.IsPermissionDenied(err), "the URL expires after 15 minutes")

	_, _, err = l.rt.ReadDocumentContent(ctx, "unknown")
	assert.True(t, cerrors.IsPermissionDenied(err))

	_, err = l.rt.GetDocumentContent(ctx, kbID, dsID, "r", "PDF")
	assert.True(t, cerrors.IsInvalidArgument(err))

	ag, err := l.rt.AgenticRetrieveStream(ctx, driver.AgenticRetrieveInput{Messages: []string{"nothing matches"}, KnowledgeBaseIDs: []string{kbID}, GenerateResponse: true})
	require.NoError(t, err)
	assert.Len(t, ag.Steps, 2)
	assert.Contains(t, ag.Answer, "Based on the retrieved results")

	assert.Equal(t, "No results were found for: q", agenticAnswer("q", nil))

	_, err = l.rt.AgenticRetrieveStream(ctx, driver.AgenticRetrieveInput{Messages: []string{"x"}, KnowledgeBaseIDs: []string{"MISSING000"}})
	assert.True(t, cerrors.IsNotFound(err))

	sqlKB, err := l.cp.CreateKnowledgeBase(ctx, badriver.KnowledgeBaseConfig{
		Name: "sales", RoleArn: "arn:aws:iam::123456789012:role/r", KnowledgeBaseConfiguration: json.RawMessage(`{"type":"SQL"}`),
	})
	require.NoError(t, err)

	q, err := l.rt.GenerateQuery(ctx, "total sales", sqlKB.ARN)
	require.NoError(t, err)
	assert.Contains(t, q[0].SQL, `FROM "sales"`)

	_, err = l.rt.GenerateQuery(ctx, "x", "arn:aws:bedrock:us-east-1:123456789012:knowledge-base/"+kbID)
	assert.True(t, cerrors.IsInvalidArgument(err), "a VECTOR knowledge base can't generate SQL")

	ranked, next, err := l.rt.Rerank(ctx, driver.RerankInput{
		Query: "refund", ModelARN: "m", NumberOfResults: ptr(int32(1)),
		Sources: []driver.RerankSource{{Text: "shipping"}, {JSON: json.RawMessage(`{"topic":"refund"}`)}},
	})
	require.NoError(t, err)
	require.Len(t, ranked, 1, "numberOfResults caps the total, not the page")
	assert.Equal(t, int32(1), ranked[0].Index)
	assert.Empty(t, next)

	opt, err := l.rt.OptimizePrompt(ctx, "summarize  this", "m")
	require.NoError(t, err)
	assert.Contains(t, opt.Text, "summarize this")
}

func TestFlowFeatures(t *testing.T) {
	l := newLinked(t, config.WithAsyncSettle())
	ctx := context.Background()

	flow, err := l.cp.CreateFlow(ctx, badriver.FlowConfig{
		Name: "f", ExecutionRoleArn: "arn:aws:iam::123456789012:role/r",
		Definition: json.RawMessage(`{"nodes":[{"name":"in","type":"Input"},{"name":"cond","type":"Condition"},
			{"name":"yes","type":"Output"},{"name":"no","type":"Output"}],
			"connections":[{"source":"in","target":"cond","type":"Data"},
			{"source":"cond","target":"no","type":"Conditional","configuration":{"conditional":{"condition":"other"}}},
			{"source":"cond","target":"yes","type":"Conditional","configuration":{"conditional":{"condition":"default"}}}]}`),
	})
	require.NoError(t, err)

	_, err = l.cp.PrepareFlow(ctx, flow.ID)
	require.NoError(t, err)

	in := driver.FlowInvocationInput{FlowID: flow.ARN, AliasID: "TSTALIASID", Inputs: []driver.FlowInput{{NodeName: "in", Content: json.RawMessage(`"doc"`)}}}

	res, err := l.rt.InvokeFlow(ctx, in)
	require.NoError(t, err)
	require.Len(t, res.Outputs, 1)
	assert.Equal(t, "yes", res.Outputs[0].NodeName, "a Condition node follows its default connection")
	assert.Empty(t, res.Traces, "no traces unless asked")

	_, err = l.rt.InvokeFlow(ctx, driver.FlowInvocationInput{FlowID: flow.ID, AliasID: "TSTALIASID", Inputs: []driver.FlowInput{{NodeName: "yes"}}})
	assert.True(t, cerrors.IsInvalidArgument(err), "the input must name an Input node")

	_, err = l.rt.InvokeFlow(ctx, driver.FlowInvocationInput{FlowID: flow.ID, AliasID: "TSTALIASID"})
	assert.True(t, cerrors.IsInvalidArgument(err), "exactly one input")

	ex, err := l.rt.StartFlowExecution(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, driver.FlowExecutionRunning, ex.Status)
	assert.Empty(t, ex.EndedAt)

	_, err = l.rt.StartFlowExecution(ctx, driver.FlowInvocationInput{FlowID: flow.ID, AliasID: "TSTALIASID", ExecutionName: "bad name!"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	list, _, err := l.rt.ListFlowExecutions(ctx, flow.ID, "", driver.Page{})
	require.NoError(t, err)
	assert.Len(t, list, 1)

	_, _, err = l.rt.ListFlowExecutions(ctx, "MISSING000", "", driver.Page{})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = l.rt.GetFlowExecution(ctx, flow.ID, "TSTALIASID", "")
	assert.True(t, cerrors.IsInvalidArgument(err))

	standalone := New(config.NewOptions())
	_, err = standalone.InvokeFlow(ctx, in)
	assert.True(t, cerrors.IsInvalidArgument(err), "flows need the control plane")
}

func TestStandaloneKeepsLegacyBehavior(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	res, err := m.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: "x", AgentAliasID: "y"})
	require.NoError(t, err)
	assert.NotEmpty(t, res.SessionID, "a session id is minted")

	_, err = m.InvokeAgent(ctx, driver.InvokeAgentInput{AgentAliasID: "y"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	r, err := m.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: "kb1", QueryText: "q"})
	require.NoError(t, err)
	assert.Len(t, r.Results, 2)

	_, err = m.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{InputText: "q", Type: driver.RAGKnowledgeBase, KnowledgeBaseID: "kb1", ModelARN: "m"})
	assert.True(t, cerrors.IsInvalidArgument(err), "knowledge-base RAG needs the control plane")

	_, err = m.GetDocumentContent(ctx, "kb", "ds", "d", "")
	assert.True(t, cerrors.IsInvalidArgument(err))

	require.NoError(t, m.DeleteAgentMemory(ctx, "A", "B", "", ""))
}

func TestSessionAndTagPaths(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()

	s, err := l.rt.CreateSession(ctx, driver.SessionConfig{})
	require.NoError(t, err)

	require.NoError(t, l.rt.TagResource(ctx, s.ARN, map[string]string{"a": "1", "b": "2"}))
	require.NoError(t, l.rt.UntagResource(ctx, s.ARN, []string{"a"}))

	tags, err := l.rt.ListTagsForResource(ctx, s.ARN)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"b": "2"}, tags)

	err = l.rt.TagResource(ctx, s.ARN, map[string]string{"": "x"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	err = l.rt.UntagResource(ctx, s.ARN, nil)
	assert.True(t, cerrors.IsInvalidArgument(err))

	inv, err := l.rt.CreateInvocation(ctx, s.ARN, "", "d")
	require.NoError(t, err)

	_, err = l.rt.PutInvocationStep(ctx, driver.InvocationStepInput{
		SessionID: s.ID, InvocationID: inv.ID, StepTime: "2026-01-01T00:00:00Z", Payload: json.RawMessage(`{"contentBlocks":[]}`),
	})
	require.NoError(t, err)

	invs, _, err := l.rt.ListInvocations(ctx, s.ID, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, invs, 1)

	steps, _, err := l.rt.ListInvocationSteps(ctx, s.ID, "", driver.Page{})
	require.NoError(t, err)
	assert.Len(t, steps, 1)
	assert.Nil(t, steps[0].Payload, "summaries carry no payload")

	_, err = l.rt.GetInvocationStep(ctx, s.ID, inv.ID, "missing")
	assert.True(t, cerrors.IsNotFound(err))

	all, _, err := l.rt.ListSessions(ctx, driver.Page{MaxResults: ptr(int32(5000))})
	assert.True(t, cerrors.IsInvalidArgument(err), "maxResults is at most 1000")
	assert.Nil(t, all)

	_, err = l.rt.EndSession(ctx, s.ID)
	require.NoError(t, err)

	_, err = l.rt.EndSession(ctx, s.ID)
	assert.True(t, cerrors.IsFailedPrecondition(err), "an ended session can't end again")

	require.NoError(t, l.rt.DeleteSession(ctx, s.ARN))
	assert.Empty(t, l.rt.steps.All())
	assert.Empty(t, l.rt.invocations.All())

	err = l.rt.DeleteSession(ctx, s.ID)
	assert.True(t, cerrors.IsNotFound(err))

	_, err = l.rt.GetSession(ctx, "arn:aws:bedrock:us-east-1:999999999999:session/"+s.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestExecutionPaths(t *testing.T) {
	l := newLinked(t, config.WithAsyncSettle())
	ctx := context.Background()

	flow, err := l.cp.CreateFlow(ctx, badriver.FlowConfig{
		Name: "f", ExecutionRoleArn: "arn:aws:iam::123456789012:role/r",
		Definition: json.RawMessage(`{"nodes":[{"name":"in","type":"Input"},{"name":"p","type":"Prompt"},{"name":"out","type":"Output"}],
			"connections":[{"source":"in","target":"p","type":"Data"},{"source":"p","target":"out","type":"Data",
			"configuration":{"data":{"sourceOutput":"modelCompletion","targetInput":"document"}}}]}`),
	})
	require.NoError(t, err)

	_, err = l.cp.PrepareFlow(ctx, flow.ID)
	require.NoError(t, err)

	_, err = l.cp.CreateFlowVersion(ctx, flow.ID, "")
	require.NoError(t, err)

	alias, err := l.cp.CreateFlowAlias(ctx, badriver.FlowAliasConfig{
		FlowID: flow.ID, Name: "live", RoutingConfiguration: []badriver.FlowAliasRoute{{FlowVersion: "1"}},
	})
	require.NoError(t, err)

	in := driver.FlowInvocationInput{
		FlowID: flow.ID, AliasID: alias.ARN, ExecutionName: "e1", EnableTrace: true,
		Inputs: []driver.FlowInput{{NodeName: "in", NodeOutputName: "document", Content: json.RawMessage(`"x"`)}},
	}

	res, err := l.rt.InvokeFlow(ctx, in)
	require.NoError(t, err)
	assert.NotEmpty(t, res.Traces)

	ex, err := l.rt.StartFlowExecution(ctx, in)
	require.NoError(t, err)

	_, err = l.rt.StartFlowExecution(ctx, in)
	assert.True(t, cerrors.IsAlreadyExists(err), "execution names are unique")

	events, _, err := l.rt.ListFlowExecutionEvents(ctx, flow.ID, alias.ID, "e1", "Node", driver.Page{})
	require.NoError(t, err)
	assert.Empty(t, events, "a running execution has no node events yet")

	events, _, err = l.rt.ListFlowExecutionEvents(ctx, flow.ID, alias.ID, "e1", "Flow", driver.Page{})
	require.NoError(t, err)
	require.Len(t, events, 1, "only the flow input while running")
	assert.Equal(t, evFlowInput, events[0].Kind)

	snap, err := l.rt.GetExecutionFlowSnapshot(ctx, flow.ARN, alias.ID, ex.ARN)
	require.NoError(t, err)
	assert.Equal(t, "1", snap.FlowVersion)

	stopped, err := l.rt.StopFlowExecution(ctx, flow.ID, alias.ID, "e1")
	require.NoError(t, err)
	assert.Equal(t, driver.FlowExecutionAborted, stopped.Status)
	assert.NotEmpty(t, stopped.EndedAt)

	events, _, err = l.rt.ListFlowExecutionEvents(ctx, flow.ID, alias.ID, "e1", "Flow", driver.Page{})
	require.NoError(t, err)
	assert.Len(t, events, 1, "an aborted execution produced no output")

	_, err = l.rt.GetExecutionFlowSnapshot(ctx, flow.ID, "OTHER00000", "e1")
	assert.True(t, cerrors.IsNotFound(err), "an execution is found only under its own alias")
}

func TestVersionedAliasAndResults(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	agentID := l.agent(t, nil)

	alias, err := l.cp.CreateAgentAlias(ctx, badriver.AgentAliasConfig{AgentID: agentID, Name: "live"})
	require.NoError(t, err)

	res, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: alias.ID, SessionID: "s1", InputText: "hi", EnableTrace: true})
	require.NoError(t, err)
	assert.Equal(t, "1", res.Events[0].Trace.AgentVersion, "the alias routes to version 1")

	assert.Equal(t, "a\nb", resultsText([]driver.InvocationResult{{ResponseBody: "a"}, {ResponseBody: "b"}}))
	assert.Equal(t, "plain", lambdaBody([]byte("plain")))
	assert.Equal(t, "api", lambdaBody([]byte(`{"response":{"responseBody":{"application/json":{"body":"api"}}}}`)))
	name, qualifier := lambdaFunction("fn")
	assert.Equal(t, [2]string{"fn", ""}, [2]string{name, qualifier})

	// Memory filters.
	l.rt.memories.Set(memoryKey(agentID, alias.ID, "m1", "s1"), &driver.MemorySummary{MemoryID: "m1", SessionID: "s1"})
	l.rt.memories.Set(memoryKey(agentID, alias.ID, "m1", "s2"), &driver.MemorySummary{MemoryID: "m1", SessionID: "s2"})
	require.NoError(t, l.rt.DeleteAgentMemory(ctx, agentID, alias.ID, "m1", "s1"))
	assert.Len(t, l.rt.memories.All(), 1, "only s1's summary is deleted")

	err = l.rt.DeleteAgentMemory(ctx, agentID, "MISSING000", "", "")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: alias.ID, SessionID: "s1",
		InputText: "x", SessionState: &driver.SessionState{PromptSessionAttributes: map[string]string{"p": "1"}},
	})
	require.NoError(t, err)

	conv, _ := l.rt.conversations.Get(agentID + "/" + alias.ID + "/s1")
	assert.Equal(t, map[string]string{"p": "1"}, conv.PromptSessionAttributes)
}

func TestPreAndPostProcessingTraces(t *testing.T) {
	l := newLinked(t)
	agentID := l.agent(t, func(c *badriver.AgentConfig) {
		c.PromptOverrideConfiguration = json.RawMessage(`{"promptConfigurations":[` +
			`{"promptType":"PRE_PROCESSING","promptState":"ENABLED"},{"promptType":"POST_PROCESSING","promptState":"ENABLED"}]}`)
	})

	res, err := l.rt.InvokeAgent(context.Background(), driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "hi", EnableTrace: true,
	})
	require.NoError(t, err)

	var traces []*driver.Trace

	for _, e := range res.Events {
		if e.Trace != nil {
			traces = append(traces, e.Trace)
		}
	}

	require.GreaterOrEqual(t, len(traces), 4)
	assert.Equal(t, [2]string{tracePre, promptPre}, [2]string{traces[0].Kind, traces[0].PromptType})
	assert.Equal(t, tracePost, traces[len(traces)-1].Kind)
	assert.Equal(t, res.Completion, traces[len(traces)-1].Text, "post-processing returns the final answer")
}

func TestOutputGuardrail(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	l.rt.SetGuardrails(outputOnly{})
	agentID := l.agent(t, func(c *badriver.AgentConfig) {
		c.GuardrailConfiguration = json.RawMessage(`{"guardrailIdentifier":"g","guardrailVersion":"1"}`)
	})

	res, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "hi", EnableTrace: true})
	require.NoError(t, err)
	assert.Equal(t, "masked", res.Completion, "the answer is replaced by the guardrail's message")
}

// outputOnly intervenes on OUTPUT only.
type outputOnly struct{}

func (outputOnly) ApplyGuardrail(_ context.Context, in bedrockdriver.ApplyGuardrailInput) (*bedrockdriver.ApplyGuardrailOutput, error) {
	if in.Source == bedrockdriver.GuardrailSourceOutput {
		return &bedrockdriver.ApplyGuardrailOutput{Action: bedrockdriver.GuardrailActionIntervened, Outputs: []string{"masked"}}, nil
	}

	return &bedrockdriver.ApplyGuardrailOutput{Action: bedrockdriver.GuardrailActionNone}, nil
}

func TestDraftMustBePrepared(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	agentID := l.agent(t, nil)
	in := driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "hi"}

	_, err := l.rt.InvokeAgent(ctx, in)
	require.NoError(t, err, "a prepared draft answers")

	_, err = l.cp.CreateAgentActionGroup(ctx, badriver.ActionGroupConfig{
		AgentID: agentID, AgentVersion: "DRAFT", Name: "orders",
		Executor:       json.RawMessage(`{"customControl":"RETURN_CONTROL"}`),
		FunctionSchema: json.RawMessage(`{"functions":[{"name":"lookup"}]}`),
	})
	require.NoError(t, err)

	_, err = l.rt.InvokeAgent(ctx, in)
	assert.True(t, cerrors.IsInvalidArgument(err), "a changed draft must be prepared again")

	l.prepare(t, agentID)

	_, err = l.rt.InvokeAgent(ctx, in)
	require.NoError(t, err)
}

func TestRetrievalFilterOperators(t *testing.T) {
	md := map[string]any{"team": "billing", "year": 2024.0, "tags": []string{"money", "refund"}, "public": true}
	f := func(raw string) *retrievalFilter {
		t.Helper()

		got, err := parseFilter(json.RawMessage(raw), "filter")
		require.NoError(t, err, raw)

		return got
	}

	cases := map[string]bool{
		`{"equals":{"key":"team","value":"billing"}}`:                                                       true,
		`{"equals":{"key":"public","value":true}}`:                                                          true,
		`{"equals":{"key":"tags","value":["money","refund"]}}`:                                              true,
		`{"notEquals":{"key":"team","value":"billing"}}`:                                                    false,
		`{"notEquals":{"key":"missing","value":"x"}}`:                                                       true,
		`{"greaterThan":{"key":"year","value":2023}}`:                                                       true,
		`{"greaterThanOrEquals":{"key":"year","value":2024}}`:                                               true,
		`{"lessThan":{"key":"year","value":2024}}`:                                                          false,
		`{"lessThanOrEquals":{"key":"team","value":5}}`:                                                     false,
		`{"in":{"key":"team","value":["ops","billing"]}}`:                                                   true,
		`{"notIn":{"key":"team","value":["ops"]}}`:                                                          true,
		`{"startsWith":{"key":"team","value":"bil"}}`:                                                       true,
		`{"listContains":{"key":"tags","value":"refund"}}`:                                                  true,
		`{"stringContains":{"key":"tags","value":"fun"}}`:                                                   true,
		`{"stringContains":{"key":"team","value":"zzz"}}`:                                                   false,
		`{"andAll":[{"equals":{"key":"team","value":"billing"}},{"lessThan":{"key":"year","value":2000}}]}`: false,
		`{"orAll":[{"equals":{"key":"team","value":"ops"}},{"greaterThan":{"key":"year","value":2000}}]}`:   true,
	}

	for raw, want := range cases {
		assert.Equal(t, want, f(raw).matches(md), raw)
	}

	assert.True(t, (*retrievalFilter)(nil).matches(nil), "no filter keeps everything")

	for _, bad := range []string{
		`{}`,
		`{"equals":{"key":"a","value":1},"notEquals":{"key":"a","value":1}}`,
		`{"andAll":[{"equals":{"key":"a","value":1}}]}`,
		`{"equals":{"key":"","value":1}}`,
		`{"orAll":[{"equals":{"key":"a","value":1}},{}]}`,
		`[1]`,
	} {
		_, err := parseFilter(json.RawMessage(bad), "filter")
		assert.True(t, cerrors.IsInvalidArgument(err), bad)
	}
}

func TestFilteredRetrievalAndAgentKnowledgeBaseConfig(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	kbID, dsID := l.kb(t, nil)

	for id, team := range map[string]string{"a": "billing", "b": "support"} {
		content, _ := json.Marshal(map[string]any{"dataSourceType": "CUSTOM", "custom": map[string]any{
			"customDocumentIdentifier": map[string]string{"id": id}, "sourceType": "IN_LINE",
			"inlineContent": map[string]any{"type": "TEXT", "textContent": map[string]string{"data": "refund policy " + id}},
		}})
		meta := json.RawMessage(`{"type":"IN_LINE_ATTRIBUTE","inlineAttributes":[{"key":"team","value":{"type":"STRING","stringValue":"` + team + `"}}]}`)
		_, err := l.cp.IngestKnowledgeBaseDocuments(ctx, kbID, dsID, []badriver.KnowledgeBaseDocument{{Content: content, Metadata: meta}})
		require.NoError(t, err)
	}

	onlySupport := json.RawMessage(`{"equals":{"key":"team","value":"support"}}`)

	res, err := l.rt.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: kbID, QueryText: "refund", Filter: onlySupport})
	require.NoError(t, err)
	require.Len(t, res.Results, 1)
	assert.Equal(t, "b", res.Results[0].CustomDocumentID)
	assert.Equal(t, map[string]any{"team": "support", metaDataSourceID: dsID}, res.Results[0].Metadata)

	_, err = l.rt.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: kbID, QueryText: "refund", Filter: json.RawMessage(`{}`)})
	assert.True(t, cerrors.IsInvalidArgument(err))

	rag, err := l.rt.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{
		InputText: "refund", Type: driver.RAGKnowledgeBase, KnowledgeBaseID: kbID, Filter: onlySupport,
		ModelARN: "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-sonnet-20240229-v1:0",
	})
	require.NoError(t, err)
	require.NotEmpty(t, rag.Citations)
	assert.Equal(t, "b", rag.Citations[0].References[0].CustomDocumentID)

	agentID := l.agent(t, nil)
	_, err = l.cp.AssociateAgentKnowledgeBase(ctx, badriver.AgentKnowledgeBaseConfig{
		AgentID: agentID, AgentVersion: "DRAFT", KnowledgeBaseID: kbID, Description: "faq",
	})
	require.NoError(t, err)
	l.prepare(t, agentID)

	turn, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "refund policy",
		SessionState: &driver.SessionState{KnowledgeBases: []driver.KnowledgeBaseConfig{
			{KnowledgeBaseID: kbID, NumberOfResults: ptr(int32(5)), Filter: onlySupport},
		}},
	})
	require.NoError(t, err)
	assert.Contains(t, turn.Completion, "refund policy b")
	assert.NotContains(t, turn.Completion, "refund policy a", "the per-call filter applies to the agent's knowledge base")

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s2", InputText: "x",
		SessionState: &driver.SessionState{KnowledgeBases: []driver.KnowledgeBaseConfig{{KnowledgeBaseID: "OTHERKB000"}}},
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "a knowledge base the agent doesn't use")
}

func TestExternalS3SourceAndDocTokenSnapshot(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	s3 := s3prov.New(config.NewOptions())
	l.rt.SetObjectStore(s3)

	require.NoError(t, s3.CreateBucket(ctx, "src"))
	require.NoError(t, s3.PutObject(ctx, "src", "sky.txt", []byte("The sky is blue."), "text/plain", nil))

	rag, err := l.rt.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{
		InputText: "sky colour", Type: driver.RAGExternalSources,
		ModelARN:        "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-sonnet-20240229-v1:0",
		ExternalSources: []driver.ExternalSource{{SourceType: "S3", S3URI: "s3://src/sky.txt"}},
	})
	require.NoError(t, err)
	assert.Contains(t, rag.Text, "The sky is blue.")

	_, err = l.rt.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{
		InputText: "x", Type: driver.RAGExternalSources, ModelARN: "m",
		ExternalSources: []driver.ExternalSource{{SourceType: "S3", S3URI: "s3://src/missing.txt"}},
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "an S3 source that doesn't exist")

	kbID, dsID := l.kb(t, map[string]string{"doc": "kept across restarts"})
	content, err := l.rt.GetDocumentContent(ctx, kbID, dsID, "doc", "RAW")
	require.NoError(t, err)

	snap, err := l.rt.Snapshot(ctx, false)
	require.NoError(t, err)

	restored := New(config.NewOptions(config.WithClock(l.clock)))
	require.NoError(t, restored.Restore(ctx, snap))

	body, _, err := restored.ReadDocumentContent(ctx, content.Token)
	require.NoError(t, err)
	assert.Equal(t, "kept across restarts", string(body))
}

func TestNumberOfResultsRangeOnEveryPath(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	kbID, _ := l.kb(t, map[string]string{"a": "refund policy"})
	bad := ptr(int32(-1))
	model := "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-sonnet-20240229-v1:0"

	_, err := l.rt.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{
		InputText: "refund", Type: driver.RAGKnowledgeBase, KnowledgeBaseID: kbID, ModelARN: model, NumberOfResults: bad,
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "RetrieveAndGenerate numberOfResults is 1..100")

	agentID := l.agent(t, nil)
	_, err = l.cp.AssociateAgentKnowledgeBase(ctx, badriver.AgentKnowledgeBaseConfig{
		AgentID: agentID, AgentVersion: "DRAFT", KnowledgeBaseID: kbID, Description: "faq",
	})
	require.NoError(t, err)
	l.prepare(t, agentID)

	_, err = l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "refund",
		SessionState: &driver.SessionState{KnowledgeBases: []driver.KnowledgeBaseConfig{{KnowledgeBaseID: kbID, NumberOfResults: bad}}},
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "sessionState knowledge-base numberOfResults is 1..100")

	_, err = l.rt.InvokeInlineAgent(ctx, driver.InlineAgentInput{
		SessionID: "s1", InputText: "refund", FoundationModel: "anthropic.claude-3-sonnet-20240229-v1:0",
		Instruction:    "You are a helpful agent that answers questions about orders.",
		KnowledgeBases: []driver.InlineKnowledgeBase{{KnowledgeBaseID: kbID, Description: "faq", NumberOfResults: bad}},
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "inline knowledge-base numberOfResults is 1..100")
}

func TestLambdaActionGroupCanCallTheRuntime(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	session, err := l.rt.CreateSession(ctx, driver.SessionConfig{})
	require.NoError(t, err)

	lam := &fakeLambda{body: "tagged", call: func() {
		assert.NoError(t, l.rt.TagResource(ctx, session.ARN, map[string]string{"by": "lambda"}))
	}}
	l.rt.SetLambda(lam)

	agentID := l.agent(t, nil)
	_, err = l.cp.CreateAgentActionGroup(ctx, badriver.ActionGroupConfig{
		AgentID: agentID, AgentVersion: "DRAFT", Name: "tools",
		Executor:       json.RawMessage(`{"lambda":"arn:aws:lambda:us-east-1:123456789012:function:tagger"}`),
		FunctionSchema: json.RawMessage(`{"functions":[{"name":"tag_session"}]}`),
	})
	require.NoError(t, err)
	l.prepare(t, agentID)

	done := make(chan error, 1)

	go func() {
		_, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "tag session"})
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the turn deadlocked: a Lambda action group called back into the runtime")
	}

	tags, err := l.rt.ListTagsForResource(ctx, session.ARN)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"by": "lambda"}, tags)
}

func TestExpiredDocTokensAreSwept(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	kbID, dsID := l.kb(t, map[string]string{"doc": "text"})

	for range 3 {
		_, err := l.rt.GetDocumentContent(ctx, kbID, dsID, "doc", "RAW")
		require.NoError(t, err)
	}

	assert.Len(t, l.rt.docTokens.All(), 3)

	l.clock.Advance(docTokenLifetime + time.Second)

	fresh, err := l.rt.GetDocumentContent(ctx, kbID, dsID, "doc", "RAW")
	require.NoError(t, err)
	assert.Len(t, l.rt.docTokens.All(), 1, "issuing a handle drops the expired ones")

	_, _, err = l.rt.ReadDocumentContent(ctx, fresh.Token)
	require.NoError(t, err)
}

// kbWithTeams creates a knowledge base whose two documents carry a "team"
// attribute, for filter tests.
func (l *linked) kbWithTeams(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	kbID, dsID := l.kb(t, nil)

	for id, team := range map[string]string{"billing-doc": "billing", "support-doc": "support"} {
		content, _ := json.Marshal(map[string]any{"dataSourceType": "CUSTOM", "custom": map[string]any{
			"customDocumentIdentifier": map[string]string{"id": id}, "sourceType": "IN_LINE",
			"inlineContent": map[string]any{"type": "TEXT", "textContent": map[string]string{"data": "refund policy of " + team}},
		}})
		meta := json.RawMessage(`{"type":"IN_LINE_ATTRIBUTE","inlineAttributes":[{"key":"team","value":{"type":"STRING","stringValue":"` + team + `"}}]}`)
		_, err := l.cp.IngestKnowledgeBaseDocuments(ctx, kbID, dsID, []badriver.KnowledgeBaseDocument{{Content: content, Metadata: meta}})
		require.NoError(t, err)
	}

	return kbID
}

// TestLambdaResponseContract follows the agents Lambda response format: the
// returned session attributes and knowledge-base settings shape the turn and
// the next ones, FAILURE is a DependencyFailedException naming the function,
// and REPROMPT is read as the observation.
func TestLambdaResponseContract(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	kbID := l.kbWithTeams(t)
	lam := &fakeLambda{}
	l.rt.SetLambda(lam)

	const fn = "arn:aws:lambda:us-east-1:123456789012:function:tools"

	agentID := l.agent(t, nil)
	_, err := l.cp.AssociateAgentKnowledgeBase(ctx, badriver.AgentKnowledgeBaseConfig{
		AgentID: agentID, AgentVersion: "DRAFT", KnowledgeBaseID: kbID, Description: "policies",
	})
	require.NoError(t, err)
	_, err = l.cp.CreateAgentActionGroup(ctx, badriver.ActionGroupConfig{
		AgentID: agentID, AgentVersion: "DRAFT", Name: "tools", Executor: json.RawMessage(`{"lambda":"` + fn + `"}`),
		FunctionSchema: json.RawMessage(`{"functions":[{"name":"lookup"}]}`),
	})
	require.NoError(t, err)
	l.prepare(t, agentID)

	turn := func(session, body string) (*driver.InvokeAgentResult, error) {
		lam.body = body

		return l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: session,
			InputText: "lookup the refund policy"})
	}

	res, err := turn("s1", `{"response":{"functionResponse":{"responseBody":{"TEXT":{"body":"looked up"}}}},`+
		`"sessionAttributes":{"cart":"42"},"promptSessionAttributes":{"tone":"brief"},`+
		`"knowledgeBasesConfiguration":[{"knowledgeBaseId":"`+kbID+`","retrievalConfiguration":`+
		`{"vectorSearchConfiguration":{"filter":{"equals":{"key":"team","value":"support"}}}}}]}`)
	require.NoError(t, err)
	assert.Contains(t, res.Completion, "looked up")
	assert.Contains(t, res.Completion, "refund policy of support")
	assert.NotContains(t, res.Completion, "refund policy of billing", "the Lambda's knowledge-base filter applies to the turn")

	conv, _ := l.rt.conversations.Get(agentID + "/TSTALIASID/s1")
	assert.Equal(t, map[string]string{"cart": "42"}, conv.SessionAttributes)
	assert.Equal(t, map[string]string{"tone": "brief"}, conv.PromptSessionAttributes)

	_, err = turn("s1", `{"response":{"functionResponse":{"responseBody":{"TEXT":{"body":"again"}}}}}`)
	require.NoError(t, err)

	var event lambdaEvent
	require.NoError(t, json.Unmarshal(lam.payload, &event))
	assert.Equal(t, map[string]string{"cart": "42"}, event.SessionAttributes, "the next turn's event carries what the Lambda set")

	_, err = turn("s2", `{"response":{"functionResponse":{"responseState":"FAILURE","responseBody":{"TEXT":{"body":"card declined"}}}}}`)
	require.Equal(t, cerrors.Unavailable, cerrors.GetCode(err))

	var re *driver.ResourceError
	require.ErrorAs(t, err, &re)
	assert.Equal(t, fn, re.ResourceName)
	assert.Contains(t, err.Error(), "card declined")

	res, err = turn("s3", `{"response":{"functionResponse":{"responseState":"REPROMPT","responseBody":{"TEXT":{"body":"need order id"}}}}}`)
	require.NoError(t, err)
	assert.Contains(t, res.Completion, "need order id")

	_, err = turn("s4", `{"response":{"functionResponse":{"responseBody":{"TEXT":{"body":"x"}}}},`+
		`"knowledgeBasesConfiguration":[{"knowledgeBaseId":"OTHERKB000"}]}`)
	assert.Equal(t, cerrors.Unavailable, cerrors.GetCode(err), "a knowledge base the agent doesn't use")
}

func TestTurnLocksAreReleased(t *testing.T) {
	l := newLinked(t)
	agentID := l.agent(t, nil)

	for i := range 20 {
		_, err := l.rt.InvokeAgent(context.Background(), driver.InvokeAgentInput{
			AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: fmt.Sprintf("s-%d", i), InputText: "hi",
		})
		require.NoError(t, err)
	}

	l.rt.turnLocksMu.Lock()
	defer l.rt.turnLocksMu.Unlock()

	assert.Empty(t, l.rt.turnLocks, "no lock outlives its turns")
}

func TestIdleSessionSummaryWithoutAnotherTurn(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	agentID := l.agent(t, func(c *badriver.AgentConfig) {
		c.IdleSessionTTLInSeconds = 60
		c.MemoryConfiguration = json.RawMessage(`{"enabledMemoryTypes":["SESSION_SUMMARY"]}`)
	})

	_, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "book a window seat", MemoryID: "m1",
	})
	require.NoError(t, err)

	mem, _, err := l.rt.GetAgentMemory(ctx, agentID, "TSTALIASID", "m1", driver.MemoryTypeSessionSummary, driver.Page{})
	require.NoError(t, err)
	assert.Empty(t, mem, "the session is still open")

	l.clock.Advance(61 * time.Second)

	mem, _, err = l.rt.GetAgentMemory(ctx, agentID, "TSTALIASID", "m1", driver.MemoryTypeSessionSummary, driver.Page{})
	require.NoError(t, err)
	require.Len(t, mem, 1, "the timed-out session is summarized without another turn")
	assert.Contains(t, mem[0].SummaryText, "window seat")
	assert.False(t, l.rt.conversations.Has(agentID+"/TSTALIASID/s1"))
}

func TestAnswerContextAndConversationHistory(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	kbID, _ := l.kb(t, map[string]string{"refunds": "Refunds take 5 days.", "shipping": "Shipping takes 2 days."})
	agentID := l.agent(t, nil)

	_, err := l.cp.AssociateAgentKnowledgeBase(ctx, badriver.AgentKnowledgeBaseConfig{
		AgentID: agentID, AgentVersion: "DRAFT", KnowledgeBaseID: kbID, Description: "faq",
	})
	require.NoError(t, err)
	l.prepare(t, agentID)

	res, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
		AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: "refunds?",
		SessionState: &driver.SessionState{History: []driver.HistoryMessage{
			{Role: "user", Text: "hi"}, {Role: "assistant", Text: "hello"}, {Role: "user", Text: "who are you"},
		}},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Completion, "Refunds take 5 days.")
	assert.NotContains(t, res.Completion, "Shipping", "a chunk sharing no word with the question is not context")

	conv, _ := l.rt.conversations.Get(agentID + "/TSTALIASID/s1")
	require.Len(t, conv.Turns, 3, "two turns from conversationHistory, then this one")
	assert.Equal(t, turn{User: "hi", Assistant: "hello"}, conv.Turns[0])
	assert.Equal(t, "who are you", conv.Turns[1].User)
}

func TestOpenAPIParsingAndAPICalls(t *testing.T) {
	jsonDoc := `{"paths":{"/orders/{id}":{"summary":"x","parameters":[` +
		`{"name":"region","in":"header"},{"name":"id","in":"path","schema":{"type":"string"}}],"get":{"parameters":[` +
		`{"name":"id","in":"path","required":true,"schema":{"type":"integer"}},{"in":"query"},{"name":"verbose"}]}}}}`
	yamlDoc := "paths:\n  /refunds:\n    post:\n      requestBody:\n        content:\n          application/json:\n" +
		"            schema:\n              properties:\n                amount: {type: number}\n                note: {}\n" +
		"              required: [amount, 3]\n"

	apis := parseOpenAPI(jsonDoc)
	require.Len(t, apis, 1, "path-level keys that are not methods are skipped")
	assert.Equal(t, []paramDef{
		{name: "id", typ: "integer", required: true, in: "path"}, {name: "verbose", typ: "string"},
		{name: "region", typ: "string", in: "header"},
	}, apis[0].params, "path-level parameters are added; the operation's id overrides the path's")
	assert.Empty(t, apis[0].bodyType)

	apis = parseOpenAPI(yamlDoc)
	require.Len(t, apis, 1)
	assert.Equal(t, "application/json", apis[0].bodyType)
	assert.Equal(t, []paramDef{{name: "amount", typ: "number", required: true}, {name: "note", typ: "string"}}, apis[0].body)

	assert.Empty(t, parseOpenAPI("::: not yaml"))
	assert.Empty(t, parseOpenAPI(""))

	call := apiInvocation("g", &apis[0], "refunds amount=5")
	require.NotNil(t, call.RequestBody)
	assert.Equal(t, []driver.Parameter{{Name: "amount", Type: "number", Value: "5"}}, call.RequestBody.Properties)

	body := toLambdaRequestBody(call.RequestBody)
	assert.Equal(t, "5", body.Content["application/json"].Properties[0].Value)
	assert.Nil(t, toLambdaRequestBody(nil))

	l := newLinked(t)
	assert.Empty(t, l.rt.openAPIOperations(context.Background(), json.RawMessage(`{"s3":{"s3BucketName":"b","s3ObjectKey":"k"}}`)),
		"an S3 schema the runtime can't read has no operations")
	assert.Empty(t, l.rt.openAPIOperations(context.Background(), json.RawMessage(`not json`)))

	ts := &turnState{conv: &conversation{}}
	text, err := applyLambdaResponse(ts, "fn", []byte("plain text reply"))
	require.NoError(t, err)
	assert.Equal(t, "plain text reply", text, "a reply that is not JSON is the observation")
}

// TestLambdaReadsMemoryDuringItsTurn has an action-group Lambda read agent
// memory on its own session's second turn (the session is stored by then).
// The idle sweep in GetAgentMemory must skip the busy session, not wait for
// the lock the calling turn holds.
func TestLambdaReadsMemoryDuringItsTurn(t *testing.T) {
	l := newLinked(t)
	ctx := context.Background()
	agentID := l.agent(t, func(c *badriver.AgentConfig) {
		c.MemoryConfiguration = json.RawMessage(`{"enabledMemoryTypes":["SESSION_SUMMARY"]}`)
	})

	var memErr error

	lam := &fakeLambda{body: `{"response":{"functionResponse":{"responseBody":{"TEXT":{"body":"personalized"}}}}}`}
	lam.call = func() {
		_, _, memErr = l.rt.GetAgentMemory(ctx, agentID, "TSTALIASID", "m1", driver.MemoryTypeSessionSummary, driver.Page{})
	}
	l.rt.SetLambda(lam)

	_, err := l.cp.CreateAgentActionGroup(ctx, badriver.ActionGroupConfig{
		AgentID: agentID, AgentVersion: "DRAFT", Name: "profile",
		Executor:       json.RawMessage(`{"lambda":"arn:aws:lambda:us-east-1:123456789012:function:profile"}`),
		FunctionSchema: json.RawMessage(`{"functions":[{"name":"personalize"}]}`),
	})
	require.NoError(t, err)
	l.prepare(t, agentID)

	turn := func(text string) error {
		done := make(chan error, 1)

		go func() {
			_, err := l.rt.InvokeAgent(ctx, driver.InvokeAgentInput{
				AgentID: agentID, AgentAliasID: "TSTALIASID", SessionID: "s1", InputText: text, MemoryID: "m1",
			})
			done <- err
		}()

		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("the turn deadlocked: its Lambda read agent memory")

			return nil
		}
	}

	require.NoError(t, turn("hello"), "first turn stores the session")
	require.NoError(t, turn("personalize my offers"), "second turn's Lambda reads memory")
	require.NoError(t, memErr)
}
