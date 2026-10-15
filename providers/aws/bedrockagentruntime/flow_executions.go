package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Flow runtime constants from the service model.
const (
	flowNodeInput        = "Input"
	flowNodeOutput       = "Output"
	flowNodeCondition    = "Condition"
	flowOutputNodeType   = "FlowOutputNode"
	completionSuccess    = "SUCCESS"
	defaultNodeField     = "document"
	executionNamePattern = `^[a-zA-Z0-9-]{1,36}$`
	executionMarker      = "/execution/"
	eventTypeNode        = "Node"
	eventTypeFlow        = "Flow"
	evFlowInput          = "flowInputEvent"
	evFlowOutput         = "flowOutputEvent"
	evNodeInput          = "nodeInputEvent"
	evNodeOutput         = "nodeOutputEvent"
	traceNodeInput       = "nodeInputTrace"
	traceNodeOutput      = "nodeOutputTrace"
	connConditional      = "Conditional"
	conditionDefault     = "default"
	flowTestAlias        = "TSTALIASID"
)

var executionNameRE = regexp.MustCompile(executionNamePattern)

// execution is a stored asynchronous flow execution.
type execution struct {
	ARN         string                      `json:"arn"`
	Name        string                      `json:"name"`
	FlowID      string                      `json:"flowId"`
	AliasID     string                      `json:"aliasId"`
	FlowVersion string                      `json:"flowVersion"`
	Status      string                      `json:"status"`
	StartedAt   string                      `json:"startedAt"`
	EndedAt     string                      `json:"endedAt,omitempty"`
	Events      []driver.FlowExecutionEvent `json:"events,omitempty"`
	Snapshot    driver.FlowSnapshot         `json:"snapshot"`
}

// flowDef is the part of a flow definition the runtime walks.
type flowDef struct {
	Nodes []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Outputs []struct {
			Name string `json:"name"`
		} `json:"outputs"`
	} `json:"nodes"`
	Connections []struct {
		Source        string `json:"source"`
		Target        string `json:"target"`
		Type          string `json:"type"`
		Configuration struct {
			Data *struct {
				SourceOutput string `json:"sourceOutput"`
				TargetInput  string `json:"targetInput"`
			} `json:"data"`
			Conditional *struct {
				Condition string `json:"condition"`
			} `json:"conditional"`
		} `json:"configuration"`
	} `json:"connections"`
}

// resolvedFlow is the flow version an alias routes to.
type resolvedFlow struct {
	flow    *badriver.Flow
	flowID  string
	aliasID string
	version string
}

// InvokeFlow runs a flow through an alias with one input and returns the
// documents its Output nodes produced. Nodes other than Input, Output and
// Condition pass their input document through unchanged (no model, Lambda or
// knowledge-base calls); a Condition node follows its "default" connection.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) InvokeFlow(ctx context.Context, in driver.FlowInvocationInput) (*driver.FlowInvocationResult, error) {
	rf, err := m.resolveFlow(ctx, in.FlowID, in.AliasID)
	if err != nil {
		return nil, err
	}

	run, err := runFlow(rf.flow, in.Inputs, m.nowString())
	if err != nil {
		return nil, err
	}

	execID := in.ExecutionID
	if execID == "" {
		if execID, err = newUUID(); err != nil {
			return nil, err
		}
	}

	out := &driver.FlowInvocationResult{ExecutionID: execID, Outputs: run.outputs, CompletionReason: completionSuccess}
	if in.EnableTrace {
		out.Traces = run.traces
	}

	return out, nil
}

// StartFlowExecution runs a flow asynchronously: the execution is Running
// until it settles (at once, or after a short window under async settling) and
// then Succeeded, with its events and the definition it ran.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) StartFlowExecution(ctx context.Context, in driver.FlowInvocationInput) (*driver.FlowExecution, error) {
	var v violations

	v.pattern("flowExecutionName", in.ExecutionName, executionNameRE, executionNamePattern)

	if err := v.err(); err != nil {
		return nil, err
	}

	rf, err := m.resolveFlow(ctx, in.FlowID, in.AliasID)
	if err != nil {
		return nil, err
	}

	run, err := runFlow(rf.flow, in.Inputs, m.nowString())
	if err != nil {
		return nil, err
	}

	name := in.ExecutionName
	if name == "" {
		if name, err = newUUID(); err != nil {
			return nil, err
		}
	}

	start := m.now()
	ex := &execution{
		ARN: idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID,
			"flow/"+rf.flowID+"/alias/"+rf.aliasID+executionMarker+name),
		Name: name, FlowID: rf.flowID, AliasID: rf.aliasID, FlowVersion: rf.version,
		Status: driver.FlowExecutionSucceeded, StartedAt: start.Format(time.RFC3339Nano), Events: run.events,
		Snapshot: snapshotOf(rf),
	}

	d := m.opts.SettleDuration(settle.DefaultBedrockFlowExecutionSettle)
	ex.EndedAt = start.Add(d).Format(time.RFC3339Nano)

	if !m.executions.SetIfAbsent(ex.ARN, ex) {
		return nil, errors.Newf(errors.AlreadyExists, "flow execution %s already exists", name)
	}

	m.execSettle.Begin(ex.ARN, driver.FlowExecutionRunning, start, d)

	result := m.observeExecution(ex)

	return &result, nil
}

// GetFlowExecution returns an execution.
func (m *Mock) GetFlowExecution(_ context.Context, flowID, aliasID, executionID string) (*driver.FlowExecution, error) {
	ex, err := m.findExecution(flowID, aliasID, executionID)
	if err != nil {
		return nil, err
	}

	result := m.observeExecution(ex)

	return &result, nil
}

// ListFlowExecutions lists one page of a flow's executions (of one alias when
// aliasID is set), newest first.
func (m *Mock) ListFlowExecutions(ctx context.Context, flowID, aliasID string, page driver.Page) (
	[]driver.FlowExecution, string, error,
) {
	flowID = flowResourceID(flowID)

	if m.cp != nil {
		if _, err := m.cp.GetFlow(ctx, flowID); err != nil {
			return nil, "", err
		}
	}

	aliasID = aliasResourceID(aliasID)

	var out []driver.FlowExecution

	for _, ex := range m.executions.SortedValues() {
		if ex.FlowID == flowID && (aliasID == "" || ex.AliasID == aliasID) {
			out = append(out, m.observeExecution(ex))
		}
	}

	sortByTimeDesc(out, func(e driver.FlowExecution) string { return e.StartedAt + e.ARN })

	return paginate(out, page, defaultListPage)
}

// StopFlowExecution aborts a running execution; a finished one can't be
// stopped (ConflictException).
func (m *Mock) StopFlowExecution(_ context.Context, flowID, aliasID, executionID string) (*driver.FlowExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ex, err := m.findExecution(flowID, aliasID, executionID)
	if err != nil {
		return nil, err
	}

	if status := m.observeExecution(ex).Status; status != driver.FlowExecutionRunning {
		return nil, errors.Newf(errors.FailedPrecondition, "Flow execution %s is %s and can't be stopped.", ex.Name, status)
	}

	stopped := *ex
	stopped.Status = driver.FlowExecutionAborted
	stopped.EndedAt = m.nowString()
	m.executions.Set(ex.ARN, &stopped)
	m.execSettle.Clear(ex.ARN)

	result := m.observeExecution(&stopped)

	return &result, nil
}

// ListFlowExecutionEvents lists one page of an execution's Flow events (flow
// inputs and outputs) or Node events (node inputs and outputs). Until the
// execution has succeeded only its flow input is listed: a running or aborted
// execution has produced no outputs.
func (m *Mock) ListFlowExecutionEvents(_ context.Context, flowID, aliasID, executionID, eventType string, page driver.Page) (
	[]driver.FlowExecutionEvent, string, error,
) {
	var v violations

	v.required("eventType", eventType == "")
	v.enum("eventType", eventType, eventTypeNode, eventTypeFlow)

	if err := v.err(); err != nil {
		return nil, "", err
	}

	ex, err := m.findExecution(flowID, aliasID, executionID)
	if err != nil {
		return nil, "", err
	}

	done := m.observeExecution(ex).Status == driver.FlowExecutionSucceeded

	var out []driver.FlowExecutionEvent

	for _, e := range ex.Events {
		isFlow := e.Kind == evFlowInput || e.Kind == evFlowOutput
		if isFlow == (eventType == eventTypeFlow) && (done || e.Kind == evFlowInput) {
			out = append(out, e)
		}
	}

	return paginate(out, page, defaultListPage)
}

// GetExecutionFlowSnapshot returns the flow definition an execution ran.
func (m *Mock) GetExecutionFlowSnapshot(_ context.Context, flowID, aliasID, executionID string) (*driver.FlowSnapshot, error) {
	ex, err := m.findExecution(flowID, aliasID, executionID)
	if err != nil {
		return nil, err
	}

	snap := ex.Snapshot

	return &snap, nil
}

// resolveFlow loads the flow version an alias routes to. TSTALIASID routes to
// DRAFT, which must be prepared.
func (m *Mock) resolveFlow(ctx context.Context, flowID, aliasID string) (*resolvedFlow, error) {
	if m.cp == nil {
		return nil, errors.New(errors.InvalidArgument, "flow invocation needs the bedrock-agent control plane")
	}

	rf := &resolvedFlow{flowID: flowResourceID(flowID), aliasID: aliasResourceID(aliasID), version: badriver.DraftVersion}

	draft, err := m.cp.GetFlow(ctx, rf.flowID)
	if err != nil {
		return nil, err
	}

	if rf.aliasID != flowTestAlias {
		alias, aerr := m.cp.GetFlowAlias(ctx, rf.flowID, rf.aliasID)
		if aerr != nil {
			return nil, aerr
		}

		if len(alias.RoutingConfiguration) > 0 {
			rf.version = alias.RoutingConfiguration[0].FlowVersion
		}
	}

	if rf.version == badriver.DraftVersion {
		if draft.Status != badriver.FlowPrepared {
			return nil, errors.Newf(errors.InvalidArgument,
				"Flow %s is not prepared. Call PrepareFlow before you invoke its working draft.", rf.flowID)
		}

		rf.flow = draft

		return rf, nil
	}

	if rf.flow, err = m.cp.GetFlowVersion(ctx, rf.flowID, rf.version); err != nil {
		return nil, err
	}

	return rf, nil
}

// findExecution resolves an execution name or ARN under a flow and alias.
func (m *Mock) findExecution(flowID, aliasID, executionID string) (*execution, error) {
	var v violations

	v.required("executionIdentifier", executionID == "")

	if err := v.err(); err != nil {
		return nil, err
	}

	flowID, aliasID = flowResourceID(flowID), aliasResourceID(aliasID)

	arn := executionID
	if !strings.HasPrefix(executionID, "arn:") {
		arn = idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID,
			"flow/"+flowID+"/alias/"+aliasID+executionMarker+executionID)
	}

	ex, ok := m.executions.Get(arn)
	if !ok || ex.FlowID != flowID || ex.AliasID != aliasID {
		return nil, errors.Newf(errors.NotFound, "flow execution %q not found", executionID)
	}

	return ex, nil
}

// observeExecution returns the execution as a reader sees it now.
func (m *Mock) observeExecution(ex *execution) driver.FlowExecution {
	out := driver.FlowExecution{
		ARN: ex.ARN, FlowID: ex.FlowID, AliasID: ex.AliasID, FlowVersion: ex.FlowVersion,
		Status: m.execSettle.State(ex.ARN, m.opts.Clock.Now(), ex.Status), StartedAt: ex.StartedAt, EndedAt: ex.EndedAt,
	}

	if out.Status == driver.FlowExecutionRunning {
		out.EndedAt = ""
	}

	return out
}

func snapshotOf(rf *resolvedFlow) driver.FlowSnapshot {
	return driver.FlowSnapshot{
		FlowID: rf.flowID, AliasID: rf.aliasID, FlowVersion: rf.version, Definition: string(rf.flow.Definition),
		ExecutionRoleARN: rf.flow.ExecutionRoleArn, CustomerEncryptionKeyARN: rf.flow.CustomerEncryptionKeyArn,
	}
}

// flowRun is the outcome of walking a flow definition.
type flowRun struct {
	outputs []driver.FlowOutput
	events  []driver.FlowExecutionEvent
	traces  []driver.FlowTrace
}

// runFlow walks a definition from the Input node the input names, passing the
// document along Data connections, and collects what Output nodes receive.
func runFlow(flow *badriver.Flow, inputs []driver.FlowInput, now string) (*flowRun, error) {
	if len(inputs) != 1 {
		var v violations

		v.addf("Value at 'inputs' failed to satisfy constraint: Member must have length between 1 and 1")

		return nil, v.err()
	}

	var def flowDef
	if err := json.Unmarshal(flow.Definition, &def); err != nil || len(def.Nodes) == 0 {
		return nil, errors.Newf(errors.InvalidArgument, "Flow %s has no definition to run.", flow.ID)
	}

	in := inputs[0]
	types := map[string]string{}

	for _, n := range def.Nodes {
		types[n.Name] = n.Type
	}

	if types[in.NodeName] != flowNodeInput {
		return nil, errors.Newf(errors.InvalidArgument, "Node %q is not an Input node of flow %s.", in.NodeName, flow.ID)
	}

	run := &flowRun{}
	run.event(evFlowInput, in.NodeName, orField(in.NodeOutputName), in.Content, now)
	walkFlow(&def, types, in.NodeName, in.Content, run, now)

	return run, nil
}

// walkFlow visits nodes breadth-first from start, each once.
func walkFlow(def *flowDef, types map[string]string, start string, doc json.RawMessage, run *flowRun, now string) {
	type visit struct {
		node string
		doc  json.RawMessage
	}

	seen := map[string]bool{start: true}
	queue := []visit{{start, doc}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for i := range def.Connections {
			c := &def.Connections[i]
			if c.Source != cur.node || seen[c.Target] || !takesConnection(def, types, cur.node, i) {
				continue
			}

			seen[c.Target] = true
			field := defaultNodeField

			if c.Configuration.Data != nil && c.Configuration.Data.TargetInput != "" {
				field = c.Configuration.Data.TargetInput
			}

			run.event(evNodeInput, c.Target, field, cur.doc, now)
			run.trace(traceNodeInput, c.Target, field, cur.doc, now)

			if types[c.Target] == flowNodeOutput {
				run.outputs = append(run.outputs, driver.FlowOutput{NodeName: c.Target, NodeType: flowOutputNodeType, Content: cur.doc})
				run.event(evFlowOutput, c.Target, field, cur.doc, now)

				continue
			}

			run.event(evNodeOutput, c.Target, defaultNodeField, cur.doc, now)
			run.trace(traceNodeOutput, c.Target, defaultNodeField, cur.doc, now)
			queue = append(queue, visit{c.Target, cur.doc})
		}
	}
}

// takesConnection reports whether the walk follows connection i out of node:
// every Data connection, and for a Condition node only its "default"
// conditional connection (or its first, when none is named default).
func takesConnection(def *flowDef, types map[string]string, node string, i int) bool {
	c := &def.Connections[i]
	if types[node] != flowNodeCondition || c.Type != connConditional {
		return true
	}

	first := -1

	for j := range def.Connections {
		o := &def.Connections[j]
		if o.Source != node || o.Type != connConditional {
			continue
		}

		if o.Configuration.Conditional != nil && o.Configuration.Conditional.Condition == conditionDefault {
			return j == i
		}

		if first < 0 {
			first = j
		}
	}

	return first == i
}

func (r *flowRun) event(kind, node, field string, doc json.RawMessage, now string) {
	r.events = append(r.events, driver.FlowExecutionEvent{
		Kind: kind, NodeName: node, Timestamp: now, Fields: []driver.FlowField{{Name: field, Content: doc}},
	})
}

func (r *flowRun) trace(kind, node, field string, doc json.RawMessage, now string) {
	r.traces = append(r.traces, driver.FlowTrace{Kind: kind, NodeName: node, FieldName: field, Content: doc, Timestamp: now})
}

func orField(name string) string {
	if name == "" {
		return defaultNodeField
	}

	return name
}

// flowResourceID returns the id of a flow id or ARN.
func flowResourceID(identifier string) string {
	if _, rest, ok := strings.Cut(identifier, ":flow/"); ok {
		id, _, _ := strings.Cut(rest, "/")

		return id
	}

	return identifier
}

// aliasResourceID returns the id of a flow alias id or ARN.
func aliasResourceID(identifier string) string {
	if i := strings.LastIndex(identifier, "/alias/"); i >= 0 {
		return identifier[i+len("/alias/"):]
	}

	return identifier
}
