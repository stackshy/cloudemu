package cloudformation

import (
	"context"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// stackResourceType is the pseudo resource type CloudFormation uses for the
// stack-level events in the stream.
const stackResourceType = "AWS::CloudFormation::Stack"

// CreateStack validates the template, records the stack as CREATE_IN_PROGRESS,
// then provisions its resources in dependency order. A provisioning failure
// rolls the stack back (deleting what was created) and leaves it
// ROLLBACK_COMPLETE, reported through the stack status and events, not as an
// API error, mirroring CloudFormation's asynchronous create. A template that
// needs an unacknowledged capability is refused before anything is created.
func (m *Mock) CreateStack(ctx context.Context, in *cfn.CreateStackInput) (*cfn.Stack, error) {
	if in.StackName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "stack name is required")
	}

	body, err := m.templateBody(ctx, in.TemplateBody, in.TemplateURL)
	if err != nil {
		return nil, err
	}

	t, err := cfn.ParseTemplate(body)
	if err != nil {
		return nil, err
	}

	params, paramValues, err := m.mergeParameters(ctx, t, in.Parameters)
	if err != nil {
		return nil, err
	}

	stackID := m.newStackID(in.StackName)
	resolver := m.newResolver(in.StackName, stackID, paramValues, in.NotificationARNs)

	effective, err := resolver.Prepare(t)
	if err != nil {
		return nil, err
	}

	if cerr := cfn.CheckCapabilities(t, in.Capabilities); cerr != nil {
		return nil, cerr
	}

	now := m.clock.Now()
	sd := &stackData{
		resolved:  map[string]cfn.ResolvedResource{},
		deleteIDs: map[string]string{},
		props:     map[string]map[string]any{},
		stack: cfn.Stack{
			ID: stackID, Name: in.StackName, Status: cfn.StatusCreateInProgress,
			Description: t.Description, Parameters: params, Tags: in.Tags,
			Capabilities: in.Capabilities, TemplateBody: body,
			CreationTime: now, LastUpdated: now, NotificationARNs: in.NotificationARNs,
		},
	}

	if !m.claimStackSlot(in.StackName, sd) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Stack [%s] already exists", in.StackName)
	}

	m.emitStackEvent(sd, cfn.StatusCreateInProgress, "User Initiated")

	m.provision(ctx, sd, effective, resolver, cfn.OnStackFailureRollback)

	out := sd.snapshotStack()

	return &out, nil
}

// provision creates the resources of a new stack. It reports whether every
// resource was created. On a failure onFailure decides what happens next:
// ROLLBACK deletes what was created and leaves ROLLBACK_COMPLETE,
// DO_NOTHING keeps it and leaves CREATE_FAILED, and DELETE deletes the stack.
func (m *Mock) provision(ctx context.Context, sd *stackData, t *cfn.Template, res *cfn.Resolver, onFailure string) bool {
	failures, _ := m.converge(ctx, sd, t, res, convergeOpts{stopOnFailure: true})
	if len(failures) == 0 {
		m.emitStackEvent(sd, cfn.StatusCreateComplete, "")
		return true
	}

	reason := failureSummary(failures)

	switch onFailure {
	case cfn.OnStackFailureDoNothing:
		m.emitStackEvent(sd, cfn.StatusCreateFailed, reason)
	case cfn.OnStackFailureDelete:
		m.emitStackEvent(sd, cfn.StatusDeleteInProgress, reason+" Delete requested by user.")
		m.finishDelete(ctx, sd)
	default:
		reason += " Rollback requested by user."
		m.emitStackEvent(sd, cfn.StatusRollbackInProgress, reason)
		m.teardown(ctx, sd)
		m.emitTerminalEvent(sd, cfn.StatusRollbackComplete, reason)
	}

	return false
}

// claimStackSlot atomically inserts sd for name, or replaces a prior
// DELETE_COMPLETE stack, returning false when an active stack already holds the
// name. The atomicity (SetIfAbsent, otherwise a store-locked Update) closes the
// concurrent-CreateStack race in which two callers both create the same name
// and the second Set orphans the first's already-provisioned resources.
func (m *Mock) claimStackSlot(name string, sd *stackData) bool {
	if m.stacks.SetIfAbsent(name, sd) {
		return true
	}

	claimed := false

	m.stacks.Update(name, func(existing *stackData) *stackData {
		if existing.status() == cfn.StatusDeleteComplete {
			claimed = true
			return sd
		}

		return existing
	})

	return claimed
}

// mergeParameters resolves each template parameter to a value (supplied, else
// its default). It rejects undeclared keys, missing values and values that
// break a constraint. An SSM parameter type is resolved from Parameter Store.
// The returned map holds the values Ref sees.
func (m *Mock) mergeParameters(
	ctx context.Context, t *cfn.Template, provided []cfn.Parameter,
) ([]cfn.Parameter, map[string]string, error) {
	given := make(map[string]string, len(provided))

	var undeclared []string

	for _, p := range provided {
		given[p.Key] = p.Value

		if _, ok := t.Parameters[p.Key]; !ok {
			undeclared = append(undeclared, p.Key)
		}
	}

	if len(undeclared) > 0 {
		sort.Strings(undeclared)

		return nil, nil, cerrors.Newf(cerrors.InvalidArgument,
			"Parameters: [%s] do not exist in the template", strings.Join(undeclared, ", "))
	}

	out, err := parameterValues(t, given)
	if err != nil {
		return nil, nil, err
	}

	values := make(map[string]string, len(out))

	for i := range out {
		if cerr := m.checkParameter(ctx, t, &out[i]); cerr != nil {
			return nil, nil, cerr
		}

		values[out[i].Key] = effectiveValue(&out[i])
	}

	return out, values, nil
}

// parameterValues picks each parameter's value, supplied or default, and
// reports the ones with neither.
func parameterValues(t *cfn.Template, given map[string]string) ([]cfn.Parameter, error) {
	names := make([]string, 0, len(t.Parameters))
	for name := range t.Parameters {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]cfn.Parameter, 0, len(names))

	var missing []string

	for _, name := range names {
		def := t.Parameters[name]

		value, ok := given[name]
		if !ok && def.Default != nil {
			value, ok = cfn.Stringify(def.Default), true
		}

		if !ok {
			missing = append(missing, name)
			continue
		}

		out = append(out, cfn.Parameter{Key: name, Value: value, NoEcho: def.NoEcho})
	}

	if len(missing) > 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"Parameters: [%s] must have values", strings.Join(missing, ", "))
	}

	return out, nil
}

// checkParameter checks one value against its constraints and resolves an
// SSM parameter type.
func (m *Mock) checkParameter(ctx context.Context, t *cfn.Template, p *cfn.Parameter) error {
	def := t.Parameters[p.Key]

	if err := cfn.CheckParameterValue(p.Key, &def, p.Value); err != nil {
		return err
	}

	if _, isSSM := cfn.SSMValueType(def.Type); !isSSM {
		return nil
	}

	resolved, err := m.resolveSSMParameter(ctx, p.Value)
	if err != nil {
		return err
	}

	p.ResolvedValue = resolved

	return nil
}

// effectiveValue is the value Ref returns for a stored parameter.
func effectiveValue(p *cfn.Parameter) string {
	if p.ResolvedValue != "" {
		return p.ResolvedValue
	}

	return p.Value
}

func resolveProps(resolver *cfn.Resolver, raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}

	resolved, err := resolver.Resolve(raw)
	if err != nil {
		return nil, err
	}

	out, ok := resolved.(map[string]any)
	if !ok {
		// Properties was an Fn::If that chose AWS::NoValue.
		out = map[string]any{}
	}

	return out, nil
}

func resolveOutputs(resolver *cfn.Resolver, t *cfn.Template) ([]cfn.Output, error) {
	if len(t.Outputs) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(t.Outputs))
	for name := range t.Outputs {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]cfn.Output, 0, len(names))

	for _, name := range names {
		od := t.Outputs[name]

		val, err := resolver.ResolveString(od.Value)
		if err != nil {
			return nil, err
		}

		o := cfn.Output{Key: name, Value: val, Description: od.Description}
		if od.Export != nil {
			o.ExportName, _ = resolver.ResolveString(od.Export.Name)
		}

		out = append(out, o)
	}

	return out, nil
}

func (m *Mock) newResolver(name, id string, paramValues map[string]string, notificationARNs []string) *cfn.Resolver {
	return &cfn.Resolver{
		Params:           paramValues,
		Resources:        map[string]cfn.ResolvedResource{},
		Region:           m.region,
		AccountID:        m.accountID,
		StackName:        name,
		StackID:          id,
		NotificationARNs: notificationARNs,
	}
}

// --- locked stackData mutations ---

func (m *Mock) emitStackEvent(sd *stackData, status, reason string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stack.Status = status
	sd.stack.StatusReason = reason
	sd.stack.Events = append(sd.stack.Events, m.event(sd, sd.stack.Name, sd.stack.ID, stackResourceType, status, reason))
}

// emitTerminalEvent records a terminal status whose event row has no reason,
// while the stack keeps stackReason, the cause a reader of DescribeStacks sees.
func (m *Mock) emitTerminalEvent(sd *stackData, status, stackReason string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stack.Status = status
	sd.stack.StatusReason = stackReason
	sd.stack.Events = append(sd.stack.Events, m.event(sd, sd.stack.Name, sd.stack.ID, stackResourceType, status, ""))
}

func (m *Mock) emitResourceEvent(sd *stackData, logicalID, physicalID, rtype, status, reason string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stack.Events = append(sd.stack.Events, m.event(sd, logicalID, physicalID, rtype, status, reason))
}

// event builds a StackEvent stamped with a fresh id and the current clock time.
func (m *Mock) event(sd *stackData, logicalID, physicalID, rtype, status, reason string) cfn.StackEvent {
	return cfn.StackEvent{
		EventID: idgen.UUID(), StackID: sd.stack.ID, StackName: sd.stack.Name,
		LogicalID: logicalID, PhysicalID: physicalID, ResourceType: rtype,
		Status: status, StatusReason: reason, Timestamp: m.tick(),
	}
}

// tick returns the clock time; a monotonic real clock keeps events ordered even
// under a FakeClock that returns a fixed instant (equal timestamps are still
// ordered by append position, which DescribeStackEvents preserves).
func (m *Mock) tick() time.Time {
	return m.clock.Now()
}

func (m *Mock) applyStackMeta(sd *stackData, in *cfn.UpdateStackInput, p *updatePlan) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stack.TemplateBody = p.body
	sd.stack.Parameters = p.params
	sd.stack.Description = p.description
	sd.stack.NotificationARNs = p.notificationARNs
	sd.stack.LastUpdated = m.clock.Now()

	if in.Tags != nil {
		sd.stack.Tags = in.Tags
	}

	if in.Capabilities != nil {
		sd.stack.Capabilities = in.Capabilities
	}
}

// priorState snapshots the metadata an update is about to overwrite, so a
// failed update can revert to it.
func (sd *stackData) priorState() priorState {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return priorState{
		templateBody:     sd.stack.TemplateBody,
		params:           append([]cfn.Parameter(nil), sd.stack.Parameters...),
		description:      sd.stack.Description,
		outputs:          append([]cfn.Output(nil), sd.stack.Outputs...),
		notificationARNs: append([]string(nil), sd.stack.NotificationARNs...),
	}
}

// identity returns the stack's name and id.
func (sd *stackData) identity() (name, id string) {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return sd.stack.Name, sd.stack.ID
}

// revertStackMeta restores the metadata and outputs captured before an update
// that later failed.
func (m *Mock) revertStackMeta(sd *stackData, prior *priorState) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stack.TemplateBody = prior.templateBody
	sd.stack.Parameters = prior.params
	sd.stack.Description = prior.description
	sd.stack.Outputs = prior.outputs
	sd.stack.NotificationARNs = prior.notificationARNs
	sd.stack.LastUpdated = m.clock.Now()
}

// paramValuesFrom projects stored stack parameters into the name→value map
// the resolver consumes.
func paramValuesFrom(params []cfn.Parameter) map[string]string {
	out := make(map[string]string, len(params))
	for i := range params {
		out[params[i].Key] = effectiveValue(&params[i])
	}

	return out
}

func (*Mock) upsertResource(sd *stackData, res *cfn.StackResource) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	for i := range sd.stack.Resources {
		if sd.stack.Resources[i].LogicalID == res.LogicalID {
			sd.stack.Resources[i] = *res
			return
		}
	}

	sd.stack.Resources = append(sd.stack.Resources, *res)
}

func (*Mock) removeResource(sd *stackData, logicalID string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	out := sd.stack.Resources[:0]

	for _, r := range sd.stack.Resources {
		if r.LogicalID != logicalID {
			out = append(out, r)
		}
	}

	sd.stack.Resources = out
}

// forgetAll drops the resolved/order bookkeeping after a full teardown.
func (*Mock) forgetAll(sd *stackData) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.provisionOrder = nil
	sd.resolved = map[string]cfn.ResolvedResource{}
	sd.deleteIDs = map[string]string{}
	sd.props = map[string]map[string]any{}
}

func (*Mock) resourceTypes(sd *stackData) map[string]string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := make(map[string]string, len(sd.stack.Resources))
	for _, r := range sd.stack.Resources {
		out[r.LogicalID] = r.Type
	}

	return out
}
