package cloudformation

import (
	"context"
	"reflect"
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
// ROLLBACK_COMPLETE — reported through the stack status and events, not as an
// API error, mirroring CloudFormation's asynchronous create.
func (m *Mock) CreateStack(ctx context.Context, in *cfn.CreateStackInput) (*cfn.Stack, error) {
	if in.StackName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "stack name is required")
	}

	t, err := cfn.ParseTemplate(in.TemplateBody)
	if err != nil {
		return nil, err
	}

	params, paramValues, err := mergeParameters(t, in.Parameters)
	if err != nil {
		return nil, err
	}

	now := m.clock.Now()
	stackID := m.newStackID(in.StackName)
	sd := &stackData{
		resolved:  map[string]cfn.ResolvedResource{},
		deleteIDs: map[string]string{},
		stack: cfn.Stack{
			ID: stackID, Name: in.StackName, Status: cfn.StatusCreateInProgress,
			Description: t.Description, Parameters: params, Tags: in.Tags,
			Capabilities: in.Capabilities, TemplateBody: in.TemplateBody,
			CreationTime: now, LastUpdated: now,
		},
	}

	if !m.claimStackSlot(in.StackName, sd) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Stack [%s] already exists", in.StackName)
	}

	m.emitStackEvent(sd, cfn.StatusCreateInProgress, "User Initiated")

	resolver := m.newResolver(sd, paramValues)
	if err := m.provision(ctx, sd, t, resolver, nil); err != nil {
		m.rollback(ctx, sd, cfn.StatusRollbackInProgress, cfn.StatusRollbackComplete, err.Error())
	} else {
		m.emitStackEvent(sd, cfn.StatusCreateComplete, "")
	}

	out := sd.snapshotStack()

	return &out, nil
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

// priorState captures the stack metadata an update overwrites, so a failed
// update can revert it during rollback.
type priorState struct {
	templateBody string
	params       []cfn.Parameter
	description  string
	outputs      []cfn.Output
}

// UpdateStack reconciles the stack to a new template: resources whose type or
// properties are unchanged are kept (same physical id), changed ones — and any
// resource that references a changed one — are replaced (delete + create), added
// ones are created, and removed ones are deleted. A failure rolls the stack back
// to its pre-update state (deleting what the update created, restoring what it
// deleted) and marks it UPDATE_ROLLBACK_COMPLETE.
func (m *Mock) UpdateStack(ctx context.Context, in *cfn.UpdateStackInput) (*cfn.Stack, error) {
	sd, err := m.activeStack(in.StackName)
	if err != nil {
		return nil, err
	}

	newT, err := cfn.ParseTemplate(in.TemplateBody)
	if err != nil {
		return nil, err
	}

	params, paramValues, err := mergeParameters(newT, in.Parameters)
	if err != nil {
		return nil, err
	}

	prior := sd.priorState()

	oldT, err := cfn.ParseTemplate(prior.templateBody)
	if err != nil {
		return nil, err
	}

	keep, create, remove, err := diffResources(oldT, newT)
	if err != nil {
		return nil, err
	}

	m.emitStackEvent(sd, cfn.StatusUpdateInProgress, "User Initiated")
	m.applyStackMeta(sd, in, params, newT.Description)

	if rerr := m.reconcile(ctx, sd, newT, paramValues, keep, create, remove); rerr != nil {
		m.rollbackUpdate(ctx, sd, oldT, &prior, create, remove, rerr.Error())
	} else {
		m.emitStackEvent(sd, cfn.StatusUpdateComplete, "")
	}

	out := sd.snapshotStack()

	return &out, nil
}

// rollbackUpdate reverses a failed update: it deletes the resources the update
// created and re-provisions (from the previous template) the resources the
// update deleted or replaced, then reverts the stack metadata and outputs to
// their pre-update values.
func (m *Mock) rollbackUpdate(
	ctx context.Context, sd *stackData, oldT *cfn.Template, prior *priorState, created, removed map[string]bool, reason string,
) {
	m.emitStackEvent(sd, cfn.StatusUpdateRollbackInProgress, reason)

	m.teardown(ctx, sd, created)

	resolver := m.newResolver(sd, paramValuesFrom(prior.params))

	sd.mu.RLock()
	for id, rr := range sd.resolved { // survivors kept through the update
		resolver.Resources[id] = rr
	}
	sd.mu.RUnlock()

	// Best-effort restore of the deleted/replaced resources from the old
	// template; a restore failure still lands the stack in a terminal state.
	_ = m.provision(ctx, sd, oldT, resolver, removed)

	m.revertStackMeta(sd, prior)
	m.emitStackEvent(sd, cfn.StatusUpdateRollbackComplete, "")
}

// DeleteStack tears down the stack's resources in reverse creation order and
// marks it DELETE_COMPLETE. Deleting an absent or already-deleted stack is a
// no-op success, matching CloudFormation's idempotent delete.
func (m *Mock) DeleteStack(ctx context.Context, name string) error {
	sd, ok := m.stacks.Get(name)
	if !ok || sd.status() == cfn.StatusDeleteComplete {
		return nil
	}

	m.emitStackEvent(sd, cfn.StatusDeleteInProgress, "User Initiated")
	m.teardown(ctx, sd, nil)

	sd.mu.Lock()
	sd.stack.DeletionTime = m.clock.Now()
	sd.mu.Unlock()

	m.emitStackEvent(sd, cfn.StatusDeleteComplete, "")

	return nil
}

// reconcile applies a precomputed update diff: it deletes replaced/removed
// resources, then creates added/replaced ones, seeding the resolver with the
// resources kept unchanged so their references still resolve.
func (m *Mock) reconcile(
	ctx context.Context, sd *stackData, newT *cfn.Template,
	paramValues map[string]string, keep, create, remove map[string]bool,
) error {
	m.teardown(ctx, sd, remove)

	resolver := m.newResolver(sd, paramValues)

	sd.mu.RLock()
	for id := range keep {
		if rr, ok := sd.resolved[id]; ok {
			resolver.Resources[id] = rr
		}
	}
	sd.mu.RUnlock()

	return m.provision(ctx, sd, newT, resolver, create)
}

// provision creates resources in dependency order. When only is non-nil, only
// its members are provisioned (the rest are assumed already present in the
// resolver); nil provisions every resource. Outputs are resolved last, against
// the fully-populated resolver.
func (m *Mock) provision(
	ctx context.Context, sd *stackData, t *cfn.Template, resolver *cfn.Resolver, only map[string]bool,
) error {
	order, err := cfn.OrderResources(t)
	if err != nil {
		return err
	}

	for _, id := range order {
		if only != nil && !only[id] {
			continue
		}

		if perr := m.provisionOne(ctx, sd, resolver, id, t.Resources[id]); perr != nil {
			return perr
		}
	}

	outputs, err := resolveOutputs(resolver, t)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	sd.stack.Outputs = outputs
	sd.mu.Unlock()

	return nil
}

// provisionOne resolves one resource's properties and creates it through the
// registered provisioner, recording the resource, its mapping, and its events.
func (m *Mock) provisionOne(
	ctx context.Context, sd *stackData, resolver *cfn.Resolver, id string, rdef cfn.ResourceDef,
) error {
	m.emitResourceEvent(sd, id, "", rdef.Type, cfn.ResourceCreateInProgress, "")

	prov, ok := m.registry[rdef.Type]
	if !ok {
		reason := "Resource type " + rdef.Type + " is not supported."
		m.emitResourceEvent(sd, id, "", rdef.Type, cfn.ResourceCreateFailed, reason)

		return cerrors.New(cerrors.InvalidArgument, reason)
	}

	props, err := resolveProps(resolver, rdef.Properties)
	if err != nil {
		m.emitResourceEvent(sd, id, "", rdef.Type, cfn.ResourceCreateFailed, err.Error())
		return err
	}

	res, err := prov.Create(ctx, cfn.ResourceRequest{
		LogicalID: id, Type: rdef.Type, Properties: props,
		StackName: resolver.StackName, StackID: resolver.StackID,
		Region: m.region, AccountID: m.accountID,
	})
	if err != nil {
		m.emitResourceEvent(sd, id, "", rdef.Type, cfn.ResourceCreateFailed, err.Error())
		return err
	}

	rr := cfn.ResolvedResource{RefValue: res.PhysicalID, Attributes: res.Attributes}
	resolver.Resources[id] = rr

	deleteID := res.DeleteID
	if deleteID == "" {
		deleteID = res.PhysicalID
	}

	sd.mu.Lock()
	sd.resolved[id] = rr
	sd.deleteIDs[id] = deleteID
	sd.provisionOrder = append(sd.provisionOrder, id)
	sd.mu.Unlock()

	m.upsertResource(sd, &cfn.StackResource{
		LogicalID: id, PhysicalID: res.PhysicalID, Type: rdef.Type,
		Status: cfn.ResourceCreateComplete, Timestamp: m.clock.Now(),
	})
	m.emitResourceEvent(sd, id, res.PhysicalID, rdef.Type, cfn.ResourceCreateComplete, "")

	return nil
}

// rollback records the in-progress status, tears every provisioned resource
// down, and records the terminal status.
func (m *Mock) rollback(ctx context.Context, sd *stackData, inProgress, complete, reason string) {
	m.emitStackEvent(sd, inProgress, reason)
	m.teardown(ctx, sd, nil)
	m.emitStackEvent(sd, complete, "")
}

// teardown deletes provisioned resources in reverse creation order. When only is
// non-nil, only its members are deleted; nil deletes all.
func (m *Mock) teardown(ctx context.Context, sd *stackData, only map[string]bool) {
	sd.mu.RLock()
	order := append([]string(nil), sd.provisionOrder...)
	resolved := make(map[string]cfn.ResolvedResource, len(sd.resolved))
	deleteIDs := make(map[string]string, len(sd.deleteIDs))

	for k, v := range sd.resolved {
		resolved[k] = v
	}

	for k, v := range sd.deleteIDs {
		deleteIDs[k] = v
	}
	sd.mu.RUnlock()

	typeByID := m.resourceTypes(sd)

	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		if only != nil && !only[id] {
			continue
		}

		m.deleteOne(ctx, sd, id, typeByID[id], resolved[id].RefValue, deleteIDs[id])
	}

	m.forgetResources(sd, only)
}

func (m *Mock) deleteOne(ctx context.Context, sd *stackData, id, rtype, physicalID, deleteID string) {
	prov, ok := m.registry[rtype]
	if !ok {
		return
	}

	m.emitResourceEvent(sd, id, physicalID, rtype, cfn.ResourceDeleteInProgress, "")

	if err := prov.Delete(ctx, deleteID, nil); err != nil {
		m.emitResourceEvent(sd, id, physicalID, rtype, cfn.ResourceDeleteFailed, err.Error())
		return
	}

	m.emitResourceEvent(sd, id, physicalID, rtype, cfn.ResourceDeleteComplete, "")
	m.removeResource(sd, id)
}

// diffResources classifies the update: keep (unchanged), create (added or
// changed), remove (deleted or changed). A resource is directly changed iff its
// type or decoded properties differ; the change then PROPAGATES — any resource
// that references a changed one (Ref/Fn::GetAtt/Fn::Sub) is itself re-provisioned
// so its resolved references pick up the replacement's new physical ids.
func diffResources(oldT, newT *cfn.Template) (keep, create, remove map[string]bool, err error) {
	changed := directlyChanged(oldT, newT)

	// Processing in dependency order means each resource's dependencies are
	// already marked before it is examined, so one pass reaches the fixpoint.
	order, err := cfn.OrderResources(newT)
	if err != nil {
		return nil, nil, nil, err
	}

	propagateChanges(newT, order, changed)

	keep, create, remove = classifyChanges(oldT, newT, changed)

	return keep, create, remove, nil
}

// directlyChanged marks the resources whose type or decoded properties differ
// from the previous template (added resources count as changed).
func directlyChanged(oldT, newT *cfn.Template) map[string]bool {
	changed := map[string]bool{}

	for id, nr := range newT.Resources {
		or, existed := oldT.Resources[id]
		if !existed || or.Type != nr.Type || !reflect.DeepEqual(or.Properties, nr.Properties) {
			changed[id] = true
		}
	}

	return changed
}

// propagateChanges marks any resource that references a changed one as changed
// too, walking the dependency graph in creation order.
func propagateChanges(newT *cfn.Template, order []string, changed map[string]bool) {
	deps := cfn.Dependencies(newT)

	for _, id := range order {
		for _, d := range deps[id] {
			if changed[d] {
				changed[id] = true
				break
			}
		}
	}
}

// classifyChanges splits the resources into keep / create / remove sets from the
// changed set and the old→new membership.
func classifyChanges(oldT, newT *cfn.Template, changed map[string]bool) (keep, create, remove map[string]bool) {
	keep, create, remove = map[string]bool{}, map[string]bool{}, map[string]bool{}

	for id := range newT.Resources {
		if !changed[id] {
			keep[id] = true
			continue
		}

		create[id] = true

		if _, existed := oldT.Resources[id]; existed {
			remove[id] = true
		}
	}

	for id := range oldT.Resources {
		if _, ok := newT.Resources[id]; !ok {
			remove[id] = true
		}
	}

	return keep, create, remove
}

// mergeParameters resolves each template parameter to a value (supplied, else
// its default) and rejects a stack whose required parameters were not given.
func mergeParameters(t *cfn.Template, provided []cfn.Parameter) ([]cfn.Parameter, map[string]string, error) {
	given := make(map[string]string, len(provided))
	for _, p := range provided {
		given[p.Key] = p.Value
	}

	names := make([]string, 0, len(t.Parameters))
	for name := range t.Parameters {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]cfn.Parameter, 0, len(names))
	values := make(map[string]string, len(names))

	var missing []string

	for _, name := range names {
		def := t.Parameters[name]

		switch {
		case given[name] != "" || hasKey(given, name):
			values[name] = given[name]
		case def.Default != nil:
			values[name] = cfn.Stringify(def.Default)
		default:
			missing = append(missing, name)
			continue
		}

		out = append(out, cfn.Parameter{Key: name, Value: values[name]})
	}

	if len(missing) > 0 {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument,
			"Parameters: [%s] must have values", strings.Join(missing, ", "))
	}

	return out, values, nil
}

func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

func resolveProps(resolver *cfn.Resolver, raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}

	resolved, err := resolver.Resolve(raw)
	if err != nil {
		return nil, err
	}

	out, _ := resolved.(map[string]any)

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

func (m *Mock) newResolver(sd *stackData, paramValues map[string]string) *cfn.Resolver {
	return &cfn.Resolver{
		Params:    paramValues,
		Resources: map[string]cfn.ResolvedResource{},
		Region:    m.region,
		AccountID: m.accountID,
		StackName: sd.stack.Name,
		StackID:   sd.stack.ID,
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

func (m *Mock) applyStackMeta(sd *stackData, in *cfn.UpdateStackInput, params []cfn.Parameter, desc string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stack.TemplateBody = in.TemplateBody
	sd.stack.Parameters = params
	sd.stack.Description = desc
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
		templateBody: sd.stack.TemplateBody,
		params:       append([]cfn.Parameter(nil), sd.stack.Parameters...),
		description:  sd.stack.Description,
		outputs:      append([]cfn.Output(nil), sd.stack.Outputs...),
	}
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
	sd.stack.LastUpdated = m.clock.Now()
}

// paramValuesFrom projects resolved stack parameters into the name→value map
// the resolver consumes.
func paramValuesFrom(params []cfn.Parameter) map[string]string {
	out := make(map[string]string, len(params))
	for _, p := range params {
		out[p.Key] = p.Value
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

// forgetResources drops the resolved/order bookkeeping for deleted resources.
func (*Mock) forgetResources(sd *stackData, only map[string]bool) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if only == nil {
		sd.provisionOrder = nil
		sd.resolved = map[string]cfn.ResolvedResource{}
		sd.deleteIDs = map[string]string{}

		return
	}

	kept := sd.provisionOrder[:0]

	for _, id := range sd.provisionOrder {
		if only[id] {
			delete(sd.resolved, id)
			delete(sd.deleteIDs, id)

			continue
		}

		kept = append(kept, id)
	}

	sd.provisionOrder = kept
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
