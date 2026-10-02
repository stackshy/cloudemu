package cloudformation

import (
	"context"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Verbs a resource failure is reported under in the stack status reason.
const (
	verbCreate = "create"
	verbUpdate = "update"
	verbDelete = "delete"
)

// convergeOpts tunes one converge pass.
type convergeOpts struct {
	// stopOnFailure ends the pass at the first failed resource. A forward
	// create or update stops. A rollback keeps going.
	stopOnFailure bool
	// skip names resources the pass leaves as they are.
	skip map[string]bool
	// cleanupStatus is the stack status recorded before the cleanup phase,
	// or "" to record none.
	cleanupStatus string
	// cleanRetained has the cleanup phase also delete the old resources
	// retained from earlier replacements.
	cleanRetained bool
	// rollback marks a pass that rolls an update back. Its cleanup deletes
	// the resources the update created, and it does not check exports.
	rollback bool
}

// applyFailure is one resource a converge pass could not bring to its target.
type applyFailure struct {
	logicalID string
	verb      string
	err       error
}

// liveResource is the recorded state of one provisioned resource.
type liveResource struct {
	typ      string
	resolved cfn.ResolvedResource
	props    map[string]any
	deleteID string
}

// replacement is a resource an update replaced. old is the previous physical
// resource, which is kept until the cleanup phase.
type replacement struct {
	id  string
	old liveResource
	// reclaimed marks a replacement whose new resource was taken back from
	// the retained old resources. A rollback retains it again instead of
	// deleting it.
	reclaimed bool
	// policy is the UpdateReplacePolicy the cleanup applies to old.
	policy string
}

// Resource status reasons CloudFormation records on a replacement.
const (
	reasonReplacing  = "Requested update requires the creation of a new physical resource; hence creating one."
	msgCustomNameFmt = "CloudFormation cannot update a stack when a custom-named resource requires replacing. " +
		"Rename %s and update the stack again."
)

// converge brings the stack's resources to template t. It walks t in
// dependency order and, per resource, creates it, updates it in place,
// replaces it, or leaves it alone, depending on how its resolved properties
// differ from what it was last applied with. A replacement creates the new
// resource first. The old one, and the resources t no longer declares, are
// deleted in the cleanup phase at the end. A forward pass that fails stops
// before cleanup and returns its replacements, so the rollback can put the
// old resources back untouched.
func (m *Mock) converge(
	ctx context.Context, sd *stackData, t *cfn.Template, res *cfn.Resolver, o convergeOpts,
) ([]applyFailure, []replacement) {
	seedResolver(sd, t, res)

	order, err := cfn.OrderResources(t)
	if err != nil {
		return []applyFailure{{verb: verbCreate, err: err}}, nil
	}

	failures, replaced := m.applyAll(ctx, sd, t, res, order, o)
	if len(failures) > 0 && o.stopOnFailure {
		return failures, replaced
	}

	if len(failures) == 0 {
		failures = m.setOutputs(sd, res, t, !o.rollback)
		if len(failures) > 0 && o.stopOnFailure {
			return failures, replaced
		}
	}

	if len(failures) == 0 && o.cleanupStatus != "" {
		m.emitStackEvent(sd, o.cleanupStatus, "")
	}

	var retained []replacement
	if len(failures) == 0 && o.cleanRetained {
		retained = sd.drainRetained()
	}

	m.cleanup(ctx, sd, t, &o, append(retained, replaced...))

	return failures, nil
}

// applyAll applies each resource of t in order. A forward pass stops at the
// first failure.
func (m *Mock) applyAll(
	ctx context.Context, sd *stackData, t *cfn.Template, res *cfn.Resolver, order []string, o convergeOpts,
) ([]applyFailure, []replacement) {
	var (
		failures []applyFailure
		replaced []replacement
	)

	for _, id := range order {
		if o.skip[id] {
			continue
		}

		rdef := t.Resources[id]

		if f := m.applyOne(ctx, sd, res, id, rdef, &replaced); f != nil {
			failures = append(failures, *f)

			if o.stopOnFailure {
				break
			}

			continue
		}

		sd.setPolicy(id, policyOf(&rdef))
	}

	return failures, replaced
}

// setOutputs resolves and stores the template outputs. With checkExports
// set, an export another stack owns, or a change to an export another stack
// imports, fails instead.
func (m *Mock) setOutputs(sd *stackData, res *cfn.Resolver, t *cfn.Template, checkExports bool) []applyFailure {
	outputs, err := resolveOutputs(res, t)
	if err != nil {
		return []applyFailure{{verb: verbUpdate, err: err}}
	}

	m.exportMu.Lock()
	defer m.exportMu.Unlock()

	if checkExports {
		if cerr := m.checkExports(sd, outputs); cerr != nil {
			return []applyFailure{{verb: verbUpdate, err: cerr}}
		}
	}

	sd.mu.Lock()
	sd.stack.Outputs = outputs
	sd.mu.Unlock()

	return nil
}

// seedResolver resets the resolver's resources to the live ones t still
// declares with the same type, so references resolve to current values.
func seedResolver(sd *stackData, t *cfn.Template, res *cfn.Resolver) {
	res.Resources = map[string]cfn.ResolvedResource{}

	for id, rdef := range t.Resources {
		if live, ok := sd.live(id); ok && live.typ == rdef.Type {
			res.Resources[id] = live.resolved
		}
	}
}

// applyOne brings one resource to its definition in the target template.
func (m *Mock) applyOne(
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef, replaced *[]replacement,
) *applyFailure {
	live, exists := sd.live(id)
	if !exists {
		return m.createOne(ctx, sd, res, id, rdef, createEvents())
	}

	props, err := resolveProps(res, rdef.Properties)
	if err != nil {
		return m.updateFailed(sd, id, &live, err)
	}

	prov, ok := m.registry[rdef.Type]
	if !ok || live.typ != rdef.Type {
		return m.replaceOne(ctx, sd, res, id, rdef, &live, replaced)
	}

	switch cfn.PlanResourceUpdate(prov, live.props, props) {
	case cfn.UpdateNone:
		return nil
	case cfn.UpdateInPlace:
		return m.updateOne(ctx, sd, res, id, rdef, &live, props)
	case cfn.UpdateReplace:
		if name, kept := customNameKept(prov, live.props, props); kept {
			return m.updateFailed(sd, id, &live, cerrors.Newf(cerrors.InvalidArgument, msgCustomNameFmt, name))
		}

		return m.replaceOne(ctx, sd, res, id, rdef, &live, replaced)
	}

	return nil
}

// customNameKept reports a replacement that keeps a user-set physical name.
// The new resource would collide with the old one, so CloudFormation refuses.
func customNameKept(prov cfn.Provisioner, previous, next map[string]any) (string, bool) {
	named, ok := prov.(cfn.NamedResource)
	if !ok {
		return "", false
	}

	prop := named.NameProperty()
	name := cfn.PropString(next, prop)

	return name, name != "" && name == cfn.PropString(previous, prop)
}

// updateFailed records a failed update of a live resource.
func (m *Mock) updateFailed(sd *stackData, id string, live *liveResource, err error) *applyFailure {
	m.emitResourceEvent(sd, id, live.resolved.RefValue, live.typ, cfn.ResourceUpdateInProgress, "")
	m.emitResourceEvent(sd, id, live.resolved.RefValue, live.typ, cfn.ResourceUpdateFailed, cerrors.Message(err))

	return &applyFailure{logicalID: id, verb: verbUpdate, err: err}
}

// replaceOne creates a new physical resource for id. The old one is left in
// place and queued for the cleanup phase.
func (m *Mock) replaceOne(
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef,
	live *liveResource, replaced *[]replacement,
) *applyFailure {
	m.emitResourceEvent(sd, id, live.resolved.RefValue, live.typ, cfn.ResourceUpdateInProgress, reasonReplacing)

	if f, handled := m.reclaimRetained(ctx, sd, res, id, rdef, live, replaced); handled {
		return f
	}

	if f := m.createOne(ctx, sd, res, id, rdef, replaceEvents()); f != nil {
		return f
	}

	*replaced = append(*replaced, replacement{id: id, old: *live, policy: rdef.EffectiveReplacePolicy()})

	return nil
}

// resourceEvents are the statuses one create step records.
type resourceEvents struct {
	inProgress, complete, failed, verb string
}

func createEvents() resourceEvents {
	return resourceEvents{
		inProgress: cfn.ResourceCreateInProgress, complete: cfn.ResourceCreateComplete,
		failed: cfn.ResourceCreateFailed, verb: verbCreate,
	}
}

// replaceEvents are the statuses of a replacement, whose UPDATE_IN_PROGRESS
// was already recorded with its reason.
func replaceEvents() resourceEvents {
	return resourceEvents{complete: cfn.ResourceUpdateComplete, failed: cfn.ResourceUpdateFailed, verb: verbUpdate}
}

// createOne resolves one resource's properties and creates it through the
// registered provisioner, recording the resource, its mapping, and its events.
func (m *Mock) createOne(
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef, ev resourceEvents,
) *applyFailure {
	if ev.inProgress != "" {
		m.emitResourceEvent(sd, id, "", rdef.Type, ev.inProgress, "")
	}

	fail := func(err error) *applyFailure {
		m.emitResourceEvent(sd, id, "", rdef.Type, ev.failed, cerrors.Message(err))
		return &applyFailure{logicalID: id, verb: ev.verb, err: err}
	}

	prov, ok := m.registry[rdef.Type]
	if !ok {
		return fail(cerrors.New(cerrors.InvalidArgument, "Resource type "+rdef.Type+" is not supported."))
	}

	props, err := resolveProps(res, rdef.Properties)
	if err != nil {
		return fail(err)
	}

	out, err := prov.Create(ctx, m.resourceRequest(res, id, rdef.Type, props))
	if err != nil {
		return fail(err)
	}

	m.recordCreated(sd, res, id, rdef.Type, out, props, ev.complete)

	return nil
}

// recordCreated records a resource a provisioner just created.
func (m *Mock) recordCreated(
	sd *stackData, res *cfn.Resolver, id, rtype string, out *cfn.ProvisionedResource, props map[string]any, status string,
) {
	deleteID := out.DeleteID
	if deleteID == "" {
		deleteID = out.PhysicalID
	}

	m.record(sd, res, id, cfn.ResolvedResource{RefValue: out.PhysicalID, Attributes: out.Attributes}, props, deleteID)
	m.upsertResource(sd, &cfn.StackResource{
		LogicalID: id, PhysicalID: out.PhysicalID, Type: rtype, Status: status, Timestamp: m.clock.Now(),
	})
	m.emitResourceEvent(sd, id, out.PhysicalID, rtype, status, "")
}

// reclaimRetained handles a replacement onto the custom name of an old
// resource the stack retained from a failed update. The name is created
// again when the old resource is gone. When it still exists it is taken
// back and updated in place. The resource being replaced is queued for
// cleanup either way. handled is false when no retained resource has the
// name.
func (m *Mock) reclaimRetained(
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef,
	live *liveResource, replaced *[]replacement,
) (f *applyFailure, handled bool) {
	prov := m.registry[rdef.Type]

	named, ok := prov.(cfn.NamedResource)
	if !ok {
		return nil, false
	}

	props, err := resolveProps(res, rdef.Properties)
	if err != nil {
		return nil, false
	}

	old, ok := sd.takeRetained(id, rdef.Type, named.NameProperty(), cfn.PropString(props, named.NameProperty()))
	if !ok {
		return nil, false
	}

	out, err := prov.Create(ctx, m.resourceRequest(res, id, rdef.Type, props))

	switch {
	case err == nil:
		m.recordCreated(sd, res, id, rdef.Type, out, props, cfn.ResourceUpdateComplete)
	case cerrors.IsAlreadyExists(err):
		m.record(sd, res, id, old.resolved, old.props, old.deleteID)
		*replaced = append(*replaced, replacement{id: id, old: *live, reclaimed: true, policy: rdef.EffectiveReplacePolicy()})

		return m.updateOne(ctx, sd, res, id, rdef, &old, props), true
	default:
		m.emitResourceEvent(sd, id, "", rdef.Type, cfn.ResourceUpdateFailed, cerrors.Message(err))
		sd.retain([]replacement{{id: id, old: old}})

		return &applyFailure{logicalID: id, verb: verbUpdate, err: err}, true
	}

	*replaced = append(*replaced, replacement{id: id, old: *live, policy: rdef.EffectiveReplacePolicy()})

	return nil, true
}

// updateOne changes a live resource in place. The physical id stays the same.
func (m *Mock) updateOne(
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef,
	live *liveResource, props map[string]any,
) *applyFailure {
	physicalID := live.resolved.RefValue
	m.emitResourceEvent(sd, id, physicalID, rdef.Type, cfn.ResourceUpdateInProgress, "")

	rr := live.resolved

	// Without an Updater the new properties are only recorded, and the
	// backend resource is kept as it is.
	if u, ok := m.registry[rdef.Type].(cfn.Updater); ok {
		out, err := u.Update(ctx, physicalID, live.props, m.resourceRequest(res, id, rdef.Type, props))
		if err != nil {
			m.emitResourceEvent(sd, id, physicalID, rdef.Type, cfn.ResourceUpdateFailed, cerrors.Message(err))
			return &applyFailure{logicalID: id, verb: verbUpdate, err: err}
		}

		if out != nil && out.Attributes != nil {
			rr.Attributes = out.Attributes
		}
	}

	m.record(sd, res, id, rr, props, live.deleteID)
	m.upsertResource(sd, &cfn.StackResource{
		LogicalID: id, PhysicalID: physicalID, Type: rdef.Type,
		Status: cfn.ResourceUpdateComplete, Timestamp: m.clock.Now(),
	})
	m.emitResourceEvent(sd, id, physicalID, rdef.Type, cfn.ResourceUpdateComplete, "")

	return nil
}

func (m *Mock) resourceRequest(res *cfn.Resolver, id, rtype string, props map[string]any) cfn.ResourceRequest {
	return cfn.ResourceRequest{
		LogicalID: id, Type: rtype, Properties: props,
		StackName: res.StackName, StackID: res.StackID,
		Region: m.region, AccountID: m.accountID,
	}
}

// record stores a resource's mapping and applied properties.
func (*Mock) record(
	sd *stackData, res *cfn.Resolver, id string, rr cfn.ResolvedResource, props map[string]any, deleteID string,
) {
	res.Resources[id] = rr

	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.resolved[id] = rr
	sd.deleteIDs[id] = deleteID
	sd.props[id] = props

	if !slices.Contains(sd.provisionOrder, id) {
		sd.provisionOrder = append(sd.provisionOrder, id)
	}
}

// cleanup deletes the old resources of replacements, then, newest first,
// the live resources t no longer declares. A failed delete is reported in the
// events. A dropped resource whose delete fails is kept, so a later update
// retries it. UpdateReplacePolicy Retain keeps an old resource, and
// DeletionPolicy decides whether a dropped one is deleted or only removed
// from the stack.
func (m *Mock) cleanup(
	ctx context.Context, sd *stackData, t *cfn.Template, o *convergeOpts, replaced []replacement,
) {
	for i := len(replaced) - 1; i >= 0; i-- {
		m.dropReplaced(ctx, sd, &replaced[i])
	}

	flag := sd.retainExceptOnCreate()

	sd.mu.RLock()
	order := append([]string(nil), sd.provisionOrder...)
	sd.mu.RUnlock()

	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		if _, keep := t.Resources[id]; keep || o.skip[id] {
			continue
		}

		live, ok := sd.live(id)
		if !ok {
			continue
		}

		if cfn.KeepsOnDelete(sd.policy(id).Deletion, o.rollback, flag) {
			m.skipDelete(sd, id, &live)
			continue
		}

		_ = m.deleteOne(ctx, sd, id, &live)
	}
}

// dropReplaced deletes the old resource of a replacement, or with
// UpdateReplacePolicy Retain leaves it in place outside the stack.
func (m *Mock) dropReplaced(ctx context.Context, sd *stackData, r *replacement) {
	if r.policy == cfn.PolicyValueRetain {
		m.emitResourceEvent(sd, r.id, r.old.resolved.RefValue, r.old.typ, cfn.ResourceDeleteSkipped, "")
		return
	}

	_ = m.deletePhysical(ctx, sd, r.id, &r.old)
}

// skipDelete removes a resource from the stack without deleting it.
func (m *Mock) skipDelete(sd *stackData, id string, live *liveResource) {
	m.emitResourceEvent(sd, id, live.resolved.RefValue, live.typ, cfn.ResourceDeleteSkipped, "")
	m.forget(sd, id)
}

// teardownOpts tunes one teardown.
type teardownOpts struct {
	// rollbackOfCreate marks the rollback of the create that made the
	// resources, where RetainExceptOnCreate deletes.
	rollbackOfCreate bool
	// retain names resources DeleteStack's RetainResources keeps.
	retain map[string]bool
	// force keeps a resource whose delete fails instead of failing the
	// stack, as DeletionMode FORCE_DELETE_STACK does.
	force bool
}

// teardown deletes every provisioned resource in reverse creation order,
// then any old resources retained from replacements. A resource its
// DeletionPolicy or RetainResources keeps is removed from the stack without
// being deleted. It returns the resources that failed to delete, which stay
// in the stack as DELETE_FAILED.
func (m *Mock) teardown(ctx context.Context, sd *stackData, o teardownOpts) []applyFailure {
	defer func() {
		retained := sd.drainRetained()
		for i := len(retained) - 1; i >= 0; i-- {
			m.dropReplaced(ctx, sd, &retained[i])
		}
	}()

	sd.mu.RLock()
	order := append([]string(nil), sd.provisionOrder...)
	sd.mu.RUnlock()

	flag := sd.retainExceptOnCreate()

	var failures []applyFailure

	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]

		live, ok := sd.live(id)
		if !ok {
			continue
		}

		if o.retain[id] || cfn.KeepsOnDelete(sd.policy(id).Deletion, o.rollbackOfCreate, flag) {
			m.skipDelete(sd, id, &live)
			continue
		}

		err := m.deleteOne(ctx, sd, id, &live)

		switch {
		case err == nil:
		case o.force:
			m.skipDelete(sd, id, &live)
		default:
			failures = append(failures, applyFailure{logicalID: id, verb: verbDelete, err: err})
			m.upsertResource(sd, &cfn.StackResource{
				LogicalID: id, PhysicalID: live.resolved.RefValue, Type: live.typ,
				Status: cfn.ResourceDeleteFailed, StatusReason: cerrors.Message(err), Timestamp: m.clock.Now(),
			})
		}
	}

	if len(failures) == 0 {
		m.forgetAll(sd)
	}

	return failures
}

// deleteOne deletes one live resource and drops its bookkeeping.
func (m *Mock) deleteOne(ctx context.Context, sd *stackData, id string, live *liveResource) error {
	if err := m.deletePhysical(ctx, sd, id, live); err != nil {
		return err
	}

	m.forget(sd, id)

	return nil
}

// deletePhysical deletes the physical resource live describes and records
// its events. It leaves the stack's bookkeeping alone. A resource that is
// already gone counts as deleted, as in CloudFormation.
func (m *Mock) deletePhysical(ctx context.Context, sd *stackData, id string, live *liveResource) error {
	prov, ok := m.registry[live.typ]
	if !ok {
		return nil
	}

	physicalID := live.resolved.RefValue
	m.emitResourceEvent(sd, id, physicalID, live.typ, cfn.ResourceDeleteInProgress, "")

	if err := prov.Delete(ctx, live.deleteID, nil); err != nil && !cerrors.IsNotFound(err) {
		m.emitResourceEvent(sd, id, physicalID, live.typ, cfn.ResourceDeleteFailed, cerrors.Message(err))
		return err
	}

	m.emitResourceEvent(sd, id, physicalID, live.typ, cfn.ResourceDeleteComplete, "")

	return nil
}

// restoreReplaced undoes a replacement after a failed update: it deletes the
// new physical resource and points the stack back at the old one, which was
// never touched.
func (m *Mock) restoreReplaced(ctx context.Context, sd *stackData, replaced []replacement) {
	for i := len(replaced) - 1; i >= 0; i-- {
		r := &replaced[i]

		if cur, ok := sd.live(r.id); ok && r.reclaimed {
			sd.retain([]replacement{{id: r.id, old: cur}})
		} else if ok {
			_ = m.deletePhysical(ctx, sd, r.id, &cur)
		}

		sd.mu.Lock()
		sd.resolved[r.id] = r.old.resolved
		sd.deleteIDs[r.id] = r.old.deleteID
		sd.props[r.id] = r.old.props
		sd.mu.Unlock()

		m.upsertResource(sd, &cfn.StackResource{
			LogicalID: r.id, PhysicalID: r.old.resolved.RefValue, Type: r.old.typ,
			Status: cfn.ResourceUpdateComplete, Timestamp: m.clock.Now(),
		})
	}
}

// forget drops one resource's row and bookkeeping.
func (m *Mock) forget(sd *stackData, id string) {
	m.removeResource(sd, id)

	sd.mu.Lock()
	defer sd.mu.Unlock()

	delete(sd.resolved, id)
	delete(sd.deleteIDs, id)
	delete(sd.props, id)
	delete(sd.policies, id)
	sd.provisionOrder = slices.DeleteFunc(sd.provisionOrder, func(s string) bool { return s == id })
}

// live returns the recorded state of a provisioned resource.
func (sd *stackData) live(id string) (liveResource, bool) {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	rr, ok := sd.resolved[id]
	if !ok {
		return liveResource{}, false
	}

	out := liveResource{resolved: rr, props: sd.props[id], deleteID: sd.deleteIDs[id]}

	for i := range sd.stack.Resources {
		if sd.stack.Resources[i].LogicalID == id {
			out.typ = sd.stack.Resources[i].Type
		}
	}

	return out, true
}

// failureSummary is the stack status reason CloudFormation gives for failed
// resources, such as "The following resource(s) failed to create: [A, B].".
func failureSummary(failures []applyFailure) string {
	byVerb := map[string][]string{}

	for _, f := range failures {
		if f.logicalID != "" && !slices.Contains(byVerb[f.verb], f.logicalID) {
			byVerb[f.verb] = append(byVerb[f.verb], f.logicalID)
		}
	}

	var parts []string

	for _, verb := range []string{verbCreate, verbUpdate, verbDelete} {
		if ids := byVerb[verb]; len(ids) > 0 {
			parts = append(parts, "The following resource(s) failed to "+verb+": ["+strings.Join(ids, ", ")+"].")
		}
	}

	if len(parts) == 0 && len(failures) > 0 {
		return cerrors.Message(failures[0].err)
	}

	return strings.Join(parts, " ")
}
