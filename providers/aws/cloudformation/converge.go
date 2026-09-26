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
)

// convergeOpts tunes one converge pass.
type convergeOpts struct {
	// stopOnFailure ends the pass at the first failed resource. A forward
	// create or update stops. A rollback keeps going.
	stopOnFailure bool
	// skip names resources the pass leaves as they are.
	skip map[string]bool
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

// converge brings the stack's resources to template t. It walks t in
// dependency order and, per resource, creates it, updates it in place,
// replaces it, or leaves it alone, depending on how its resolved properties
// differ from what it was last applied with. Resources not in t are deleted
// afterwards. Outputs are resolved when every resource succeeded.
func (m *Mock) converge(
	ctx context.Context, sd *stackData, t *cfn.Template, res *cfn.Resolver, o convergeOpts,
) []applyFailure {
	seedResolver(sd, t, res)

	order, err := cfn.OrderResources(t)
	if err != nil {
		return []applyFailure{{verb: verbCreate, err: err}}
	}

	var failures []applyFailure

	for _, id := range order {
		if o.skip[id] {
			continue
		}

		if f := m.applyOne(ctx, sd, res, id, t.Resources[id]); f != nil {
			failures = append(failures, *f)

			if o.stopOnFailure {
				return failures
			}
		}
	}

	m.cleanup(ctx, sd, t, o.skip)

	if len(failures) > 0 {
		return failures
	}

	outputs, err := resolveOutputs(res, t)
	if err != nil {
		return []applyFailure{{verb: verbUpdate, err: err}}
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
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef,
) *applyFailure {
	live, exists := sd.live(id)
	if !exists {
		return m.createOne(ctx, sd, res, id, rdef)
	}

	props, err := resolveProps(res, rdef.Properties)
	if err != nil {
		m.emitResourceEvent(sd, id, live.resolved.RefValue, live.typ, cfn.ResourceUpdateInProgress, "")
		m.emitResourceEvent(sd, id, live.resolved.RefValue, live.typ, cfn.ResourceUpdateFailed, cerrors.Message(err))

		return &applyFailure{logicalID: id, verb: verbUpdate, err: err}
	}

	prov, ok := m.registry[rdef.Type]
	if !ok || live.typ != rdef.Type {
		return m.replaceOne(ctx, sd, res, id, rdef, &live)
	}

	switch cfn.PlanResourceUpdate(prov, live.props, props) {
	case cfn.UpdateNone:
		return nil
	case cfn.UpdateInPlace:
		return m.updateOne(ctx, sd, res, id, rdef, &live, props)
	case cfn.UpdateReplace:
		return m.replaceOne(ctx, sd, res, id, rdef, &live)
	}

	return nil
}

// replaceOne deletes the live resource and creates a new one in its place.
func (m *Mock) replaceOne(
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef, live *liveResource,
) *applyFailure {
	if err := m.deleteOne(ctx, sd, id, live); err != nil {
		return &applyFailure{logicalID: id, verb: verbUpdate, err: err}
	}

	delete(res.Resources, id)

	return m.createOne(ctx, sd, res, id, rdef)
}

// createOne resolves one resource's properties and creates it through the
// registered provisioner, recording the resource, its mapping, and its events.
func (m *Mock) createOne(
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef,
) *applyFailure {
	m.emitResourceEvent(sd, id, "", rdef.Type, cfn.ResourceCreateInProgress, "")

	fail := func(err error) *applyFailure {
		m.emitResourceEvent(sd, id, "", rdef.Type, cfn.ResourceCreateFailed, cerrors.Message(err))
		return &applyFailure{logicalID: id, verb: verbCreate, err: err}
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

	deleteID := out.DeleteID
	if deleteID == "" {
		deleteID = out.PhysicalID
	}

	m.record(sd, res, id, cfn.ResolvedResource{RefValue: out.PhysicalID, Attributes: out.Attributes}, props, deleteID)
	m.upsertResource(sd, &cfn.StackResource{
		LogicalID: id, PhysicalID: out.PhysicalID, Type: rdef.Type,
		Status: cfn.ResourceCreateComplete, Timestamp: m.clock.Now(),
	})
	m.emitResourceEvent(sd, id, out.PhysicalID, rdef.Type, cfn.ResourceCreateComplete, "")

	return nil
}

// updateOne changes a live resource in place through its Updater. The
// physical id stays the same.
func (m *Mock) updateOne(
	ctx context.Context, sd *stackData, res *cfn.Resolver, id string, rdef cfn.ResourceDef,
	live *liveResource, props map[string]any,
) *applyFailure {
	physicalID := live.resolved.RefValue
	m.emitResourceEvent(sd, id, physicalID, rdef.Type, cfn.ResourceUpdateInProgress, "")

	u, _ := m.registry[rdef.Type].(cfn.Updater)

	out, err := u.Update(ctx, physicalID, live.props, m.resourceRequest(res, id, rdef.Type, props))
	if err != nil {
		m.emitResourceEvent(sd, id, physicalID, rdef.Type, cfn.ResourceUpdateFailed, cerrors.Message(err))
		return &applyFailure{logicalID: id, verb: verbUpdate, err: err}
	}

	rr := live.resolved
	if out != nil && out.Attributes != nil {
		rr.Attributes = out.Attributes
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

// cleanup deletes, newest first, the live resources t no longer declares.
// A failed delete is reported in the events and the resource is kept, so a
// later update retries it.
func (m *Mock) cleanup(ctx context.Context, sd *stackData, t *cfn.Template, skip map[string]bool) {
	sd.mu.RLock()
	order := append([]string(nil), sd.provisionOrder...)
	sd.mu.RUnlock()

	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		if _, keep := t.Resources[id]; keep || skip[id] {
			continue
		}

		if live, ok := sd.live(id); ok {
			_ = m.deleteOne(ctx, sd, id, &live)
		}
	}
}

// teardown deletes every provisioned resource in reverse creation order.
func (m *Mock) teardown(ctx context.Context, sd *stackData) {
	sd.mu.RLock()
	order := append([]string(nil), sd.provisionOrder...)
	sd.mu.RUnlock()

	for i := len(order) - 1; i >= 0; i-- {
		if live, ok := sd.live(order[i]); ok {
			_ = m.deleteOne(ctx, sd, order[i], &live)
		}
	}

	m.forgetAll(sd)
}

// deleteOne deletes one live resource. A resource that is already gone counts
// as deleted, as in CloudFormation.
func (m *Mock) deleteOne(ctx context.Context, sd *stackData, id string, live *liveResource) error {
	physicalID := live.resolved.RefValue

	prov, ok := m.registry[live.typ]
	if !ok {
		m.forget(sd, id)
		return nil
	}

	m.emitResourceEvent(sd, id, physicalID, live.typ, cfn.ResourceDeleteInProgress, "")

	if err := prov.Delete(ctx, live.deleteID, nil); err != nil && !cerrors.IsNotFound(err) {
		m.emitResourceEvent(sd, id, physicalID, live.typ, cfn.ResourceDeleteFailed, cerrors.Message(err))
		return err
	}

	m.emitResourceEvent(sd, id, physicalID, live.typ, cfn.ResourceDeleteComplete, "")
	m.forget(sd, id)

	return nil
}

// forget drops one resource's row and bookkeeping.
func (m *Mock) forget(sd *stackData, id string) {
	m.removeResource(sd, id)

	sd.mu.Lock()
	defer sd.mu.Unlock()

	delete(sd.resolved, id)
	delete(sd.deleteIDs, id)
	delete(sd.props, id)
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

	for _, verb := range []string{verbCreate, verbUpdate} {
		if ids := byVerb[verb]; len(ids) > 0 {
			parts = append(parts, "The following resource(s) failed to "+verb+": ["+strings.Join(ids, ", ")+"].")
		}
	}

	if len(parts) == 0 && len(failures) > 0 {
		return cerrors.Message(failures[0].err)
	}

	return strings.Join(parts, " ")
}
