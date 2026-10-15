package bedrockagentruntime

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Operation names, the Operation dimension of the AWS/Bedrock/Agents metrics.
const (
	opInvokeAgent       = "InvokeAgent"
	opInvokeInlineAgent = "InvokeInlineAgent"
)

// Trace kinds and parts (members of the Trace and OrchestrationTrace unions).
const (
	traceOrchestration = "orchestrationTrace"
	tracePre           = "preProcessingTrace"
	tracePost          = "postProcessingTrace"
	traceGuardrail     = "guardrailTrace"
	partModelInput     = "modelInvocationInput"
	partModelOutput    = "modelInvocationOutput"
	partRationale      = "rationale"
	partInvocation     = "invocationInput"
	partObservation    = "observation"
	obsFinish          = "FINISH"
	obsActionGroup     = "ACTION_GROUP"
	obsKnowledgeBase   = "KNOWLEDGE_BASE"
	promptOrchestrate  = "ORCHESTRATION"
	promptPre          = "PRE_PROCESSING"
	promptPost         = "POST_PROCESSING"
	defaultIdleTTL     = 600
	summaryWords       = 60
)

// conversation is the state of one InvokeAgent session (agent, alias and the
// caller's sessionId). It ends on endSession or after idleSessionTTL.
type conversation struct {
	AgentID                 string                   `json:"agentId"`
	AliasID                 string                   `json:"aliasId"`
	SessionID               string                   `json:"sessionId"`
	MemoryID                string                   `json:"memoryId,omitempty"`
	StartedAt               string                   `json:"startedAt"`
	LastActiveAt            string                   `json:"lastActiveAt"`
	Turns                   []turn                   `json:"turns,omitempty"`
	SessionAttributes       map[string]string        `json:"sessionAttributes,omitempty"`
	PromptSessionAttributes map[string]string        `json:"promptSessionAttributes,omitempty"`
	PendingInvocationID     string                   `json:"pendingInvocationId,omitempty"`
	PendingInput            string                   `json:"pendingInput,omitempty"`
	PendingInputs           []driver.InvocationInput `json:"pendingInputs,omitempty"`
}

type turn struct {
	User      string `json:"user"`
	Assistant string `json:"assistant"`
}

// turnRequest is one agent invocation, from InvokeAgent or InvokeInlineAgent.
type turnRequest struct {
	sessionID   string
	inputText   string
	enableTrace bool
	endSession  bool
	memoryID    string
	state       *driver.SessionState
}

// InvokeAgent runs one turn of an agent through an alias. Linked to the
// control plane it resolves the agent version the alias routes to and runs its
// action groups and knowledge bases; alone it returns the simulated answer it
// always did.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) InvokeAgent(ctx context.Context, in driver.InvokeAgentInput) (*driver.InvokeAgentResult, error) {
	if m.cp == nil {
		return simulatedAgentAnswer(&in)
	}

	if err := validateAgentRef(in.AgentID, in.AgentAliasID, in.SessionID); err != nil {
		return nil, err
	}

	var v violations

	v.memoryID(in.MemoryID, false)

	if err := v.err(); err != nil {
		return nil, err
	}

	cfg, err := m.resolveAgent(ctx, in.AgentID, in.AgentAliasID)
	if err != nil {
		m.recordInvocation(opInvokeAgent, nil, err, 0)

		return nil, err
	}

	if err := applyKnowledgeBaseConfigs(cfg, in.SessionState); err != nil {
		m.recordInvocation(opInvokeAgent, cfg, err, 0)

		return nil, err
	}

	return m.runTurn(ctx, cfg, turnRequest{
		sessionID: in.SessionID, inputText: in.InputText, enableTrace: in.EnableTrace,
		endSession: in.EndSession, memoryID: in.MemoryID, state: in.SessionState,
	})
}

// simulatedAgentAnswer is the standalone answer: it needs only the agent and
// alias ids and mints a session id when the caller sends none.
func simulatedAgentAnswer(in *driver.InvokeAgentInput) (*driver.InvokeAgentResult, error) {
	var v violations

	v.required("agentId", in.AgentID == "")
	v.required("agentAliasId", in.AgentAliasID == "")

	if err := v.err(); err != nil {
		return nil, err
	}

	sessionID := in.SessionID
	if sessionID == "" {
		sessionID = idgen.GenerateID("session-")
	}

	text := "This is a simulated agent response to: " + in.InputText
	if in.InputText == "" {
		text = fmt.Sprintf("This is a simulated agent response from agent %s.", in.AgentID)
	}

	return &driver.InvokeAgentResult{
		Completion: text, SessionID: sessionID, ContentType: contentTypeJSON,
		Events: []driver.AgentEvent{{Chunk: &driver.Chunk{Text: text}}},
	}, nil
}

// turnState carries the working state of one turn.
type turnState struct {
	cfg     *agentConfig
	req     turnRequest
	conv    *conversation
	traceID string
	events  []driver.AgentEvent
	tokens  [2]int // input, output
}

// runTurn runs one agent turn and records it in the conversation.
func (m *Mock) runTurn(ctx context.Context, cfg *agentConfig, req turnRequest) (*driver.InvokeAgentResult, error) {
	start := m.now()

	unlock := m.lockConversation(conversationKey(cfg, req.sessionID))
	defer unlock()

	traceID, err := newUUID()
	if err != nil {
		return nil, err
	}

	ts := &turnState{cfg: cfg, req: req, conv: m.openConversation(cfg, req), traceID: traceID}

	answer, err := m.answer(ctx, ts)
	if err != nil {
		m.recordInvocation(cfg.operation, cfg, err, 0)

		return nil, err
	}

	res := &driver.InvokeAgentResult{
		SessionID: req.sessionID, ContentType: contentTypeJSON, MemoryID: req.memoryID, Events: ts.events,
	}

	if answer != nil {
		res.Completion = answer.Text
		res.Events = append(res.Events, driver.AgentEvent{Chunk: answer})
	}

	m.closeTurn(ts, answer)
	m.recordInvocation(cfg.operation, cfg, nil, m.now().Sub(start))
	m.recordModelUsage(cfg, ts.tokens)

	return res, nil
}

// turnLock serializes the turns of one conversation; refs counts the turns
// holding or waiting for it.
type turnLock struct {
	mu   sync.Mutex
	refs int
}

// lockConversation takes the conversation's turn lock and returns its unlock,
// which removes the lock once no other turn holds or waits for it.
func (m *Mock) lockConversation(key string) func() {
	m.turnLocksMu.Lock()

	l := m.turnLockOf(key)
	l.refs++
	m.turnLocksMu.Unlock()

	l.mu.Lock()

	return m.releaseTurnLock(key, l)
}

// tryLockConversation takes the conversation's turn lock only when no turn
// holds it, and reports whether it did. A caller that must not wait for a
// turn in progress (or for the turn that called it) uses it.
func (m *Mock) tryLockConversation(key string) (unlock func(), ok bool) {
	m.turnLocksMu.Lock()
	defer m.turnLocksMu.Unlock()

	l := m.turnLockOf(key)
	if !l.mu.TryLock() {
		return nil, false
	}

	l.refs++

	return m.releaseTurnLock(key, l), true
}

// turnLockOf returns the conversation's turn lock, adding it when there is
// none. Callers hold turnLocksMu.
func (m *Mock) turnLockOf(key string) *turnLock {
	if m.turnLocks == nil {
		m.turnLocks = map[string]*turnLock{}
	}

	l, ok := m.turnLocks[key]
	if !ok {
		l = &turnLock{}
		m.turnLocks[key] = l
	}

	return l
}

// releaseTurnLock returns the unlock of a held turn lock, which removes the
// lock once no turn holds or waits for it.
func (m *Mock) releaseTurnLock(key string, l *turnLock) func() {
	return func() {
		l.mu.Unlock()

		m.turnLocksMu.Lock()
		defer m.turnLocksMu.Unlock()

		l.refs--
		if l.refs == 0 {
			delete(m.turnLocks, key)
		}
	}
}

// answer produces the turn's answer chunk, or nil when the turn hands control
// back to the caller (returnControl).
func (m *Mock) answer(ctx context.Context, ts *turnState) (*driver.Chunk, error) {
	if ts.cfg.preProcessing && ts.req.inputText != "" {
		ts.processingTrace(tracePre, promptPre, ts.req.inputText, "The input is a valid request for this agent.")
	}

	userText, observation, handedBack, err := m.resumeOrAct(ctx, ts)
	if err != nil || handedBack {
		return nil, err
	}

	if blocked, gerr := m.guardInput(ctx, ts, userText); gerr != nil || blocked != nil {
		return blocked, gerr
	}

	return m.finish(ctx, ts, userText, observation)
}

// finish answers the user text from the agent's knowledge bases and the
// action's observation, guards the answer and records the closing traces.
func (m *Mock) finish(ctx context.Context, ts *turnState, userText, observation string) (*driver.Chunk, error) {
	sources := m.lookupKnowledgeBases(ts, userText)
	if observation != "" {
		sources = append(sources, driver.RetrievalResult{Text: observation})
	}

	text, err := m.converse(ctx, ts, userText, sources)
	if err != nil {
		return nil, err
	}

	text, err = m.guardOutput(ctx, ts, text)
	if err != nil {
		return nil, err
	}

	ts.trace(traceOrchestration, partObservation, func(t *driver.Trace) { t.ObservationType, t.Text = obsFinish, text })

	if ts.cfg.postProcessing {
		ts.processingTrace(tracePost, promptPost, text, text)
	}

	return &driver.Chunk{Text: text, Citations: cite(text, kbOnly(sources))}, nil
}

// guardInput applies the agent's guardrail to the user text; when it
// intervenes the turn's answer is the guardrail's blocked message.
func (m *Mock) guardInput(ctx context.Context, ts *turnState, userText string) (*driver.Chunk, error) {
	if ts.cfg.guardrail == nil {
		return nil, nil //nolint:nilnil // no guardrail: the turn goes on.
	}

	action, msg, err := m.applyGuardrail(ctx, toGuardrail(ts.cfg.guardrail), guardrailInput, userText)
	if err != nil || action != guardrailIntervened {
		return nil, err
	}

	ts.trace(traceGuardrail, "", func(t *driver.Trace) { t.GuardrailAction = guardrailIntervened })

	return &driver.Chunk{Text: msg}, nil
}

// resumeOrAct either finishes a pending returnControl with the caller's
// results, or picks and runs an action group for the new input (a new input
// abandons a pending returnControl). It returns the user text the answer is
// for, the action's observation, and whether control was handed back.
func (m *Mock) resumeOrAct(ctx context.Context, ts *turnState) (userText, observation string, handedBack bool, err error) {
	if st := ts.req.state; st != nil && len(st.ReturnControlResults) > 0 {
		if ts.conv.PendingInvocationID == "" || st.InvocationID != ts.conv.PendingInvocationID {
			return "", "", false, errors.Newf(errors.InvalidArgument,
				"invocationId %q does not match a pending returnControl of session %s.", st.InvocationID, ts.req.sessionID)
		}

		userText = ts.conv.PendingInput
		ts.conv.PendingInvocationID, ts.conv.PendingInput, ts.conv.PendingInputs = "", "", nil

		return userText, resultsText(st.ReturnControlResults), false, nil
	}

	ts.conv.PendingInvocationID, ts.conv.PendingInput, ts.conv.PendingInputs = "", "", nil

	call := pickAction(ts.cfg.actionGroups, ts.req.inputText)
	if call == nil {
		return ts.req.inputText, "", false, nil
	}

	ts.trace(traceOrchestration, partRationale, func(t *driver.Trace) {
		t.Text = fmt.Sprintf("To answer, call %s of action group %s.", call.name(), call.input.ActionGroup)
	})
	ts.trace(traceOrchestration, partInvocation, func(t *driver.Trace) {
		t.ActionGroup, t.Function = call.input.ActionGroup, call.name()
	})

	if call.group.returnControl {
		return "", "", true, m.handBack(ts, call)
	}

	obs, err := m.invokeActionLambda(ctx, ts, call)
	if err != nil {
		return "", "", false, err
	}

	ts.trace(traceOrchestration, partObservation, func(t *driver.Trace) { t.ObservationType, t.Text = obsActionGroup, obs })

	return ts.req.inputText, obs, false, nil
}

// handBack records a pending returnControl and emits its event.
func (*Mock) handBack(ts *turnState, call *actionCall) error {
	id, err := newUUID()
	if err != nil {
		return err
	}

	ts.conv.PendingInvocationID, ts.conv.PendingInput = id, ts.req.inputText
	ts.conv.PendingInputs = []driver.InvocationInput{call.input}
	ts.events = append(ts.events, driver.AgentEvent{ReturnControl: &driver.ReturnControl{
		InvocationID: id, Inputs: []driver.InvocationInput{call.input},
	}})

	return nil
}

// applyKnowledgeBaseConfigs applies sessionState.knowledgeBaseConfigurations:
// each names one of the agent's knowledge bases and sets how many results it
// returns and the filter they must pass for this call.
func applyKnowledgeBaseConfigs(cfg *agentConfig, st *driver.SessionState) error {
	if st == nil {
		return nil
	}

	for _, c := range st.KnowledgeBases {
		id := knowledgeBaseID(c.KnowledgeBaseID)

		i := slices.IndexFunc(cfg.knowledgeBases, func(kb knowledgeBaseRef) bool { return kb.id == id })
		if i < 0 {
			return errors.Newf(errors.InvalidArgument,
				"Knowledge base %s in sessionState.knowledgeBaseConfigurations is not associated with the agent.", c.KnowledgeBaseID)
		}

		var v violations

		v.numberOfResults("sessionState.knowledgeBaseConfigurations.member.retrievalConfiguration."+
			"vectorSearchConfiguration.numberOfResults", c.NumberOfResults)

		if err := v.err(); err != nil {
			return err
		}

		filter, err := parseFilter(c.Filter,
			"sessionState.knowledgeBaseConfigurations.member.retrievalConfiguration.vectorSearchConfiguration.filter")
		if err != nil {
			return err
		}

		if c.NumberOfResults != nil {
			cfg.knowledgeBases[i].numberOfResults = c.NumberOfResults
		}

		if filter != nil {
			cfg.knowledgeBases[i].filter = filter
		}
	}

	return nil
}

// lookupKnowledgeBases searches the agent's knowledge bases for the user text.
func (m *Mock) lookupKnowledgeBases(ts *turnState, userText string) []driver.RetrievalResult {
	var out []driver.RetrievalResult

	for _, kb := range ts.cfg.knowledgeBases {
		ranked := m.search(kb.id, userText, kb.filter)

		n := defaultNumberOfResults
		if kb.numberOfResults != nil {
			n = int(*kb.numberOfResults)
		}

		ranked = relevant(ranked)
		out = append(out, ranked[:min(n, len(ranked))]...)
	}

	if len(out) > 0 {
		ts.trace(traceOrchestration, partObservation, func(t *driver.Trace) {
			t.ObservationType, t.References = obsKnowledgeBase, slices.Clone(out)
		})
	}

	return out
}

// converse asks the agent's model for the answer, with the sources as context.
func (m *Mock) converse(ctx context.Context, ts *turnState, userText string, sources []driver.RetrievalResult) (string, error) {
	ts.trace(traceOrchestration, partModelInput, func(t *driver.Trace) {
		t.PromptType, t.FoundationModel, t.Text = promptOrchestrate, ts.cfg.foundationModel, userText
	})

	if m.models == nil {
		return "This is a simulated agent response to: " + userText, nil
	}

	msgs := make([]bedrockdriver.Message, 0, 2*len(ts.conv.Turns)+1)
	for _, t := range ts.conv.Turns {
		msgs = append(msgs,
			bedrockdriver.Message{Role: "user", Text: []string{t.User}},
			bedrockdriver.Message{Role: "assistant", Text: []string{t.Assistant}})
	}

	msgs = append(msgs, bedrockdriver.Message{Role: "user", Text: []string{withContext(userText, sources)}})

	out, err := m.models.Converse(ctx, bedrockdriver.ConverseInput{
		ModelID: ts.cfg.foundationModel, System: []string{ts.cfg.instruction}, Messages: msgs,
	})
	if err != nil {
		return "", errors.Newf(errors.InvalidArgument, "The agent's foundation model %q can't be invoked: %s",
			ts.cfg.foundationModel, errors.Message(err))
	}

	ts.tokens[0] += out.InputTokens
	ts.tokens[1] += out.OutputTokens

	text := strings.Join(out.Message.Text, "")

	ts.trace(traceOrchestration, partModelOutput, func(t *driver.Trace) {
		t.Text, t.InputTokens, t.OutputTokens = text, int32(out.InputTokens), int32(out.OutputTokens) //nolint:gosec // token counts are small.
	})

	return text, nil
}

// generate answers a prompt with a model (RetrieveAndGenerate).
func (m *Mock) generate(ctx context.Context, modelID, instruction, prompt string, sources []driver.RetrievalResult) (string, error) {
	if m.models == nil {
		return "This is a simulated retrieve-and-generate answer to: " + prompt, nil
	}

	out, err := m.models.Converse(ctx, bedrockdriver.ConverseInput{
		ModelID: modelID, System: []string{instruction},
		Messages: []bedrockdriver.Message{{Role: "user", Text: []string{withContext(prompt, sources)}}},
	})
	if err != nil {
		return "", err
	}

	return strings.Join(out.Message.Text, ""), nil
}

// withContext appends the sources to a prompt the way a RAG prompt does.
func withContext(prompt string, sources []driver.RetrievalResult) string {
	if len(sources) == 0 {
		return prompt
	}

	var b strings.Builder

	b.WriteString(prompt)
	b.WriteString("\n\nContext:")

	for _, s := range sources {
		b.WriteString("\n- ")
		b.WriteString(s.Text)
	}

	return b.String()
}

// guardOutput applies the agent's guardrail to its answer.
func (m *Mock) guardOutput(ctx context.Context, ts *turnState, text string) (string, error) {
	if ts.cfg.guardrail == nil {
		return text, nil
	}

	action, msg, err := m.applyGuardrail(ctx, toGuardrail(ts.cfg.guardrail), guardrailOutput, text)
	if err != nil {
		return "", err
	}

	if action == guardrailIntervened {
		ts.trace(traceGuardrail, "", func(t *driver.Trace) { t.GuardrailAction = guardrailIntervened })

		return msg, nil
	}

	return text, nil
}

// trace records a trace event when the caller enabled tracing.
func (ts *turnState) trace(kind, part string, fill func(*driver.Trace)) {
	if !ts.req.enableTrace {
		return
	}

	t := &driver.Trace{
		AgentID: ts.cfg.agentID, AgentAliasID: ts.cfg.aliasID, AgentVersion: ts.cfg.version,
		SessionID: ts.req.sessionID, Kind: kind, Part: part, TraceID: ts.traceID,
		EventTime: ts.conv.LastActiveAt,
	}
	fill(t)
	ts.events = append(ts.events, driver.AgentEvent{Trace: t})
}

// processingTrace records the model input and parsed output of an enabled
// pre- or post-processing step.
func (ts *turnState) processingTrace(kind, promptType, input, output string) {
	ts.trace(kind, partModelInput, func(t *driver.Trace) {
		t.PromptType, t.FoundationModel, t.Text = promptType, ts.cfg.foundationModel, input
	})
	ts.trace(kind, partModelOutput, func(t *driver.Trace) { t.Text = output })
}

// openConversation loads the conversation of the session, starting a new one
// when there is none or the previous one idled out (which ends it).
func (m *Mock) openConversation(cfg *agentConfig, req turnRequest) *conversation {
	key := conversationKey(cfg, req.sessionID)
	now := m.nowString()

	if c, ok := m.conversations.Get(key); ok {
		conv := cloneConversation(c)
		if !m.idledOut(cfg, &conv) {
			conv.LastActiveAt = now
			mergeAttributes(&conv, req.state)

			return &conv
		}

		m.endConversation(cfg, &conv)
	}

	conv := &conversation{
		AgentID: cfg.agentID, AliasID: cfg.aliasID, SessionID: req.sessionID, MemoryID: req.memoryID,
		StartedAt: now, LastActiveAt: now, Turns: historyTurns(req.state),
	}
	mergeAttributes(conv, req.state)

	return conv
}

// historyTurns turns sessionState.conversationHistory into the earlier turns
// of a new conversation: each user message, with the assistant message that
// follows it when there is one.
func historyTurns(st *driver.SessionState) []turn {
	if st == nil {
		return nil
	}

	var out []turn

	for _, msg := range st.History {
		switch {
		case msg.Role == "user":
			out = append(out, turn{User: msg.Text})
		case msg.Role == "assistant" && len(out) > 0 && out[len(out)-1].Assistant == "":
			out[len(out)-1].Assistant = msg.Text
		}
	}

	return out
}

// closeTurn records the turn, then ends the conversation on endSession.
func (m *Mock) closeTurn(ts *turnState, answer *driver.Chunk) {
	if answer != nil {
		input := ts.req.inputText
		if input == "" && len(ts.conv.Turns) > 0 {
			input = ts.conv.Turns[len(ts.conv.Turns)-1].User
		}

		ts.conv.Turns = append(ts.conv.Turns, turn{User: input, Assistant: answer.Text})
	}

	if ts.req.memoryID != "" {
		ts.conv.MemoryID = ts.req.memoryID
	}

	key := conversationKey(ts.cfg, ts.req.sessionID)

	if ts.req.endSession {
		m.endConversation(ts.cfg, ts.conv)
		m.conversations.Delete(key)

		return
	}

	m.conversations.Set(key, ts.conv)
}

// endConversation writes the session summary to the agent's memory when memory
// is enabled and the conversation has a memoryId.
func (m *Mock) endConversation(cfg *agentConfig, conv *conversation) {
	if !cfg.memoryEnabled || conv.MemoryID == "" || len(conv.Turns) == 0 {
		return
	}

	started, _ := time.Parse(time.RFC3339Nano, conv.StartedAt)
	summary := &driver.MemorySummary{
		MemoryID:         conv.MemoryID,
		SessionID:        conv.SessionID,
		SummaryText:      summarize(conv),
		SessionStartTime: conv.StartedAt,
		SessionExpiry:    started.AddDate(0, 0, int(cfg.memoryDays)).Format(time.RFC3339Nano),
	}
	m.memories.Set(memoryKey(cfg.agentID, cfg.aliasID, conv.MemoryID, conv.SessionID), summary)
}

// idledOut reports whether the conversation passed the agent's idle TTL.
func (m *Mock) idledOut(cfg *agentConfig, conv *conversation) bool {
	ttl := cfg.idleTTLSeconds
	if ttl == 0 {
		ttl = defaultIdleTTL
	}

	last, err := time.Parse(time.RFC3339Nano, conv.LastActiveAt)

	return err == nil && m.now().After(last.Add(time.Duration(ttl)*time.Second))
}

// summarize writes the deterministic session summary kept in agent memory.
func summarize(conv *conversation) string {
	asked := make([]string, 0, len(conv.Turns))
	for _, t := range conv.Turns {
		asked = append(asked, t.User)
	}

	all := strings.Fields("The user asked: " + strings.Join(asked, " | ") + ". The agent answered: " +
		conv.Turns[len(conv.Turns)-1].Assistant)

	return strings.Join(all[:min(summaryWords, len(all))], " ")
}

func mergeAttributes(conv *conversation, st *driver.SessionState) {
	if st == nil {
		return
	}

	if st.SessionAttributes != nil {
		conv.SessionAttributes = maps.Clone(st.SessionAttributes)
	}

	if st.PromptSessionAttributes != nil {
		conv.PromptSessionAttributes = maps.Clone(st.PromptSessionAttributes)
	}
}

func cloneConversation(c *conversation) conversation {
	out := *c
	out.Turns = append([]turn(nil), c.Turns...)
	out.SessionAttributes = maps.Clone(c.SessionAttributes)
	out.PromptSessionAttributes = maps.Clone(c.PromptSessionAttributes)
	out.PendingInputs = append([]driver.InvocationInput(nil), c.PendingInputs...)

	return out
}

func conversationKey(cfg *agentConfig, sessionID string) string {
	return cfg.agentID + "/" + cfg.aliasID + "/" + sessionID
}

func memoryKey(agentID, aliasID, memoryID, sessionID string) string {
	return agentID + "/" + aliasID + "/" + memoryID + "/" + sessionID
}

// kbOnly keeps the knowledge-base chunks (those with a location).
func kbOnly(sources []driver.RetrievalResult) []driver.RetrievalResult {
	var out []driver.RetrievalResult

	for _, s := range sources {
		if s.LocationType != "" {
			out = append(out, s)
		}
	}

	return out
}

// resultsText joins the bodies of the caller's returnControl results.
func resultsText(results []driver.InvocationResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, r.ResponseBody)
	}

	return strings.Join(parts, "\n")
}

func toGuardrail(g *guardrailRef) *driver.GuardrailRef {
	return &driver.GuardrailRef{ID: g.id, Version: g.version}
}

// aliasARN builds the ARN of an agent alias (used for TSTALIASID).
func (m *Mock) aliasARN(agentID, aliasID string) string {
	return idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "agent-alias/"+agentID+"/"+aliasID)
}
