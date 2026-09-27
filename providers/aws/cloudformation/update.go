package cloudformation

import (
	"context"
	"maps"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Error texts for UpdateStack and ContinueUpdateRollback.
const (
	msgNoUpdates         = "No updates are to be performed."
	msgPreviousAndBody   = "You cannot specify both usePreviousTemplate and Template Body/Template Url"
	msgContinueBadStatus = "ContinueUpdateRollback cannot be called from current stack status"
	msgSkipNotFailed     = "Resource [%s] is not in a failed state and cannot be skipped"
	msgStackCannotUpdate = "Stack:%s is in %s state and can not be updated."
	msgTypeChanged       = "Update of resource type is not permitted. " +
		"The new template modifies resource type of the following resources: [%s]"
	reasonSkippedRollback = "Resource skipped during rollback"
	reasonUserInitiated   = "User Initiated"
)

// updatableStatus reports the stack statuses UpdateStack accepts.
func updatableStatus(status string) bool {
	switch status {
	case cfn.StatusCreateComplete, cfn.StatusUpdateComplete, cfn.StatusUpdateRollbackComplete:
		return true
	default:
		return false
	}
}

// priorState captures the stack metadata an update overwrites, so a failed
// update can revert it during rollback.
type priorState struct {
	templateBody     string
	params           []cfn.Parameter
	description      string
	outputs          []cfn.Output
	notificationARNs []string
}

// updatePlan is everything UpdateStack works out before it changes anything.
// newT and oldT have their conditions applied.
type updatePlan struct {
	body             string
	params           []cfn.Parameter
	description      string
	notificationARNs []string
	newT, oldT       *cfn.Template
	newRes, oldRes   *cfn.Resolver
	prior            priorState
}

// UpdateStack brings the stack to a new template. Each resource is created,
// updated in place, replaced, or left alone, decided from its resolved
// properties, so a changed parameter value, including one read from Parameter
// Store, reaches the resources that use it. Resources the template drops are
// deleted. An update that changes nothing is a ValidationError, "No updates
// are to be performed.". A failure rolls the stack back to its previous
// template. If the rollback also fails the stack ends UPDATE_ROLLBACK_FAILED
// until ContinueUpdateRollback.
func (m *Mock) UpdateStack(ctx context.Context, in *cfn.UpdateStackInput) (*cfn.Stack, error) {
	sd, err := m.activeStack(in.StackName)
	if err != nil {
		return nil, err
	}

	if uerr := checkUpdatable(sd); uerr != nil {
		return nil, uerr
	}

	plan, err := m.planUpdate(ctx, sd, in)
	if err != nil {
		return nil, err
	}

	if m.noChanges(sd, plan, in) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgNoUpdates)
	}

	if berr := m.beginOperation(sd, updatableStatus, cfn.StatusUpdateInProgress); berr != nil {
		return nil, berr
	}

	m.applyStackMeta(sd, in, plan)

	forward := convergeOpts{stopOnFailure: true, cleanupStatus: cfn.StatusUpdateCompleteCleanupInProgress}
	if failures, replaced := m.converge(ctx, sd, plan.newT, plan.newRes, forward); len(failures) > 0 {
		m.rollbackUpdate(ctx, sd, plan, failureSummary(failures), replaced)
	} else {
		m.emitStackEvent(sd, cfn.StatusUpdateComplete, "")
	}

	out := sd.snapshotStack()

	return &out, nil
}

// checkUpdatable rejects an update of a stack in a state that does not allow
// one, such as ROLLBACK_COMPLETE or UPDATE_ROLLBACK_FAILED.
func checkUpdatable(sd *stackData) error {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if updatableStatus(sd.stack.Status) {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument, msgStackCannotUpdate, sd.stack.ID, sd.stack.Status)
}

// beginOperation moves the stack to status, recording a "User Initiated"
// event, if its current status passes allowed. The check and the move are
// one step, so two concurrent operations cannot both start.
func (m *Mock) beginOperation(sd *stackData, allowed func(string) bool, status string) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !allowed(sd.stack.Status) {
		return cerrors.Newf(cerrors.InvalidArgument, msgStackCannotUpdate, sd.stack.ID, sd.stack.Status)
	}

	sd.stack.Status = status
	sd.stack.StatusReason = reasonUserInitiated
	sd.stack.Events = append(sd.stack.Events,
		m.event(sd, sd.stack.Name, sd.stack.ID, stackResourceType, status, reasonUserInitiated))

	return nil
}

// planUpdate parses and checks the new template and applies the conditions of
// both templates. It changes no stack state.
func (m *Mock) planUpdate(ctx context.Context, sd *stackData, in *cfn.UpdateStackInput) (*updatePlan, error) {
	prior := sd.priorState()

	body, err := m.updateTemplateBody(ctx, in, prior.templateBody)
	if err != nil {
		return nil, err
	}

	newT, err := cfn.ParseTemplate(body)
	if err != nil {
		return nil, err
	}

	provided, err := previousValues(in.Parameters, prior.params)
	if err != nil {
		return nil, err
	}

	params, paramValues, err := m.mergeParameters(ctx, newT, provided)
	if err != nil {
		return nil, err
	}

	p := &updatePlan{body: body, params: params, description: newT.Description, prior: prior}

	p.notificationARNs = in.NotificationARNs
	if p.notificationARNs == nil {
		p.notificationARNs = p.prior.notificationARNs
	}

	name, id := sd.identity()

	p.newRes = m.newResolver(name, id, paramValues, p.notificationARNs)
	if p.newT, err = p.newRes.Prepare(newT); err != nil {
		return nil, err
	}

	if cerr := cfn.CheckCapabilities(newT, in.Capabilities); cerr != nil {
		return nil, cerr
	}

	if terr := checkTypesKept(sd, p.newT); terr != nil {
		return nil, terr
	}

	if p.oldT, p.oldRes, err = m.preparedTemplate(sd, &prior); err != nil {
		return nil, err
	}

	backfillProps(sd, p.oldT, p.oldRes)

	return p, nil
}

// checkTypesKept rejects a template that gives a live logical ID a new type.
func checkTypesKept(sd *stackData, t *cfn.Template) error {
	var changed []string

	for id, rdef := range t.Resources {
		if rtype := sd.rowType(id); rtype != "" && rtype != rdef.Type {
			changed = append(changed, id)
		}
	}

	if len(changed) == 0 {
		return nil
	}

	slices.Sort(changed)

	return cerrors.Newf(cerrors.InvalidArgument, msgTypeChanged, strings.Join(changed, ", "))
}

// updateTemplateBody returns the template an update names: the request's body
// or URL, or the current one for UsePreviousTemplate.
func (m *Mock) updateTemplateBody(ctx context.Context, in *cfn.UpdateStackInput, current string) (string, error) {
	if !in.UsePreviousTemplate {
		return m.templateBody(ctx, in.TemplateBody, in.TemplateURL)
	}

	if in.TemplateBody != "" || in.TemplateURL != "" {
		return "", cerrors.New(cerrors.InvalidArgument, msgPreviousAndBody)
	}

	return current, nil
}

// preparedTemplate parses the stack's stored template and applies its
// conditions with the stored parameter values.
func (m *Mock) preparedTemplate(sd *stackData, st *priorState) (*cfn.Template, *cfn.Resolver, error) {
	t, err := cfn.ParseTemplate(st.templateBody)
	if err != nil {
		return nil, nil, err
	}

	name, id := sd.identity()
	res := m.newResolver(name, id, paramValuesFrom(st.params), st.notificationARNs)

	prepared, err := res.Prepare(t)
	if err != nil {
		return nil, nil, err
	}

	return prepared, res, nil
}

// backfillProps records the applied properties of live resources that have
// none, such as those restored from an older snapshot, by resolving them from
// the template they were created with.
func backfillProps(sd *stackData, t *cfn.Template, res *cfn.Resolver) {
	seedResolver(sd, t, res)

	for id, rdef := range t.Resources {
		live, ok := sd.live(id)
		if !ok || live.props != nil || live.typ != rdef.Type {
			continue
		}

		if props, err := resolveProps(res, rdef.Properties); err == nil {
			sd.mu.Lock()
			sd.props[id] = props
			sd.mu.Unlock()
		}
	}
}

// noChanges reports an update that would change nothing: the same resources
// with the same resolved properties, the same outputs, parameter values,
// description, notification topics and tags.
func (*Mock) noChanges(sd *stackData, p *updatePlan, in *cfn.UpdateStackInput) bool {
	if !sameStackMeta(sd, p, in) {
		return false
	}

	seedResolver(sd, p.newT, p.newRes)

	if len(p.newRes.Resources) != len(p.newT.Resources) || sd.liveCount() != len(p.newT.Resources) {
		return false
	}

	for id, rdef := range p.newT.Resources {
		live, _ := sd.live(id)

		props, err := resolveProps(p.newRes, rdef.Properties)
		if err != nil || !cfn.SameProperties(live.props, props) {
			return false
		}
	}

	outputs, err := resolveOutputs(p.newRes, p.newT)

	return err == nil && slices.Equal(outputs, p.prior.outputs)
}

// sameStackMeta reports an update that keeps the parameter values,
// description, notification topics and tags.
func sameStackMeta(sd *stackData, p *updatePlan, in *cfn.UpdateStackInput) bool {
	return sameParameters(p.params, p.prior.params) && p.description == p.prior.description &&
		slices.Equal(p.notificationARNs, p.prior.notificationARNs) && sameTags(sd, in.Tags)
}

func sameParameters(a, b []cfn.Parameter) bool {
	return slices.EqualFunc(a, b, func(x, y cfn.Parameter) bool { return x.Key == y.Key && x.Value == y.Value })
}

// sameTags reports whether an update's tags leave the stack's tags as they
// are. Nil tags keep them.
func sameTags(sd *stackData, tags map[string]string) bool {
	if tags == nil {
		return true
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return maps.Equal(tags, sd.stack.Tags)
}

func (sd *stackData) liveCount() int {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return len(sd.resolved)
}

// previousValues replaces each UsePreviousValue parameter with the value the
// stack holds now. An SSM parameter keeps its Parameter Store name, so it is
// fetched again, as CloudFormation does on every update.
func previousValues(in, stored []cfn.Parameter) ([]cfn.Parameter, error) {
	current := make(map[string]string, len(stored))
	for _, p := range stored {
		current[p.Key] = p.Value
	}

	out := make([]cfn.Parameter, len(in))

	for i, p := range in {
		if p.UsePreviousValue {
			v, ok := current[p.Key]
			if !ok {
				return nil, cerrors.Newf(cerrors.InvalidArgument, "Invalid input for parameter key %s. "+
					"Cannot specify usePreviousValue as true for a parameter key not in the previous template", p.Key)
			}

			p.Value = v
			p.UsePreviousValue = false
		}

		out[i] = p
	}

	return out, nil
}

// rollbackUpdate brings the stack back to its previous template after a
// failed update, restoring what the update changed or deleted and deleting
// what it created.
func (m *Mock) rollbackUpdate(
	ctx context.Context, sd *stackData, p *updatePlan, reason string, replaced []replacement,
) {
	m.emitStackEvent(sd, cfn.StatusUpdateRollbackInProgress, reason)
	m.restoreReplaced(ctx, sd, replaced)
	m.revertStackMeta(sd, &p.prior)
	m.finishRollback(ctx, sd, p.oldT, p.oldRes, nil, reason)
}

// finishRollback converges to the previous template t. If a resource cannot
// be restored the stack ends UPDATE_ROLLBACK_FAILED, with the failed
// resources recorded for ContinueUpdateRollback. Otherwise it ends
// UPDATE_ROLLBACK_COMPLETE and keeps reason as its status reason.
func (m *Mock) finishRollback(
	ctx context.Context, sd *stackData, t *cfn.Template, res *cfn.Resolver, skip map[string]bool, reason string,
) {
	failures, _ := m.converge(ctx, sd, t, res, convergeOpts{
		skip: skip, cleanupStatus: cfn.StatusUpdateRollbackCompleteCleanupInProgress,
	})
	if len(failures) == 0 {
		sd.setRollbackFailed(nil)
		m.emitTerminalEvent(sd, cfn.StatusUpdateRollbackComplete, reason)

		return
	}

	ids := make([]string, 0, len(failures))

	for _, f := range failures {
		if f.logicalID != "" {
			ids = append(ids, f.logicalID)
		}
	}

	sd.setRollbackFailed(ids)
	m.emitStackEvent(sd, cfn.StatusUpdateRollbackFailed,
		failureSummary(failures)+" "+cerrors.Message(failures[0].err))
}

func (sd *stackData) setRollbackFailed(ids []string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.rollbackFailed = ids
}

// ContinueUpdateRollback retries the rollback of a stack in
// UPDATE_ROLLBACK_FAILED. Resources named in ResourcesToSkip must be among
// those that failed. They are marked UPDATE_COMPLETE and left as they are.
func (m *Mock) ContinueUpdateRollback(ctx context.Context, in *cfn.ContinueUpdateRollbackInput) error {
	sd, err := m.activeStack(in.StackName)
	if err != nil {
		return err
	}

	skip, err := sd.skipSet(in.ResourcesToSkip)
	if err != nil {
		return err
	}

	st := sd.priorState()

	t, res, err := m.preparedTemplate(sd, &st)
	if err != nil {
		return err
	}

	isFailed := func(s string) bool { return s == cfn.StatusUpdateRollbackFailed }
	if err = m.beginOperation(sd, isFailed, cfn.StatusUpdateRollbackInProgress); err != nil {
		return cerrors.New(cerrors.InvalidArgument, msgContinueBadStatus)
	}

	for _, id := range in.ResourcesToSkip {
		m.markSkipped(sd, t, id)
	}

	m.finishRollback(ctx, sd, t, res, skip, "")

	return nil
}

// skipSet checks ResourcesToSkip against the resources whose rollback failed.
func (sd *stackData) skipSet(ids []string) (map[string]bool, error) {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if sd.stack.Status != cfn.StatusUpdateRollbackFailed {
		return nil, cerrors.New(cerrors.InvalidArgument, msgContinueBadStatus)
	}

	skip := make(map[string]bool, len(ids))

	for _, id := range ids {
		if !slices.Contains(sd.rollbackFailed, id) {
			return nil, cerrors.Newf(cerrors.InvalidArgument, msgSkipNotFailed, id)
		}

		skip[id] = true
	}

	return skip, nil
}

// markSkipped records a failed resource the rollback leaves as it is. Its
// type comes from the template the rollback restores. A resource that is no
// longer live keeps the last physical id it had.
func (m *Mock) markSkipped(sd *stackData, t *cfn.Template, id string) {
	rtype := t.Resources[id].Type
	if rtype == "" {
		rtype = sd.rowType(id)
	}

	physicalID := sd.lastPhysicalID(id)

	m.upsertResource(sd, &cfn.StackResource{
		LogicalID: id, PhysicalID: physicalID, Type: rtype,
		Status: cfn.ResourceUpdateComplete, StatusReason: reasonSkippedRollback, Timestamp: m.clock.Now(),
	})
	m.emitResourceEvent(sd, id, physicalID, rtype, cfn.ResourceUpdateComplete, reasonSkippedRollback)
}

// lastPhysicalID returns the live physical id of a resource, or else the
// newest one its events recorded.
func (sd *stackData) lastPhysicalID(id string) string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if rr, ok := sd.resolved[id]; ok {
		return rr.RefValue
	}

	for i := len(sd.stack.Events) - 1; i >= 0; i-- {
		if e := sd.stack.Events[i]; e.LogicalID == id && e.PhysicalID != "" {
			return e.PhysicalID
		}
	}

	return ""
}

// rowType returns the type on a resource's stack row, or "" without one.
func (sd *stackData) rowType(id string) string {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	for i := range sd.stack.Resources {
		if sd.stack.Resources[i].LogicalID == id {
			return sd.stack.Resources[i].Type
		}
	}

	return ""
}

// DeleteStack tears down the stack's resources in reverse creation order and
// marks it DELETE_COMPLETE. Deleting an absent or already-deleted stack is a
// no-op success, matching CloudFormation's idempotent delete. A stack in
// UPDATE_ROLLBACK_FAILED can be deleted.
func (m *Mock) DeleteStack(ctx context.Context, name string) error {
	sd, _, ok := m.findStack(name)
	if !ok || sd.status() == cfn.StatusDeleteComplete {
		return nil
	}

	m.emitStackEvent(sd, cfn.StatusDeleteInProgress, reasonUserInitiated)
	m.teardown(ctx, sd)

	sd.mu.Lock()
	sd.stack.DeletionTime = m.clock.Now()
	sd.rollbackFailed = nil
	sd.mu.Unlock()

	m.emitStackEvent(sd, cfn.StatusDeleteComplete, "")

	return nil
}
