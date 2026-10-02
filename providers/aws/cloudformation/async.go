package cloudformation

import (
	"context"
	"fmt"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// stackEventStep is the spacing of the events of an operation that runs
// under AsyncSettle, so a client polling DescribeStackEvents sees them
// arrive one after another.
const stackEventStep = 250 * time.Millisecond

// Kinds of the last phase of a stack operation.
const (
	opCreate         = "create"
	opUpdate         = "update"
	opRollback       = "rollback"
	opRollbackCreate = "rollbackCreate"
	opContinue       = "continueRollback"
	opDelete         = "delete"
)

// Reasons and error texts of CancelUpdateStack. The reasons keep the AWS
// spelling.
const (
	reasonUpdateCancelled   = "Stack update cancelled"      //nolint:misspell // the AWS status reason
	reasonResourceCancelled = "Resource update cancelled"   //nolint:misspell // the AWS status reason
	reasonCreateCancelled   = "Resource creation cancelled" //nolint:misspell // the AWS status reason
	msgCancelBadStatus      = "CancelUpdateStack cannot be called from current stack status"
	msgDeleteInProgress     = "Stack [%s] cannot be deleted while in status %s"
	reasonInternalFailure   = "Internal Failure"
)

// pendingOp is the last phase of a stack operation. Without AsyncSettle it
// runs as soon as the operation has done its work. Under AsyncSettle it
// waits until ReadyAt, and the stack stays in its *_IN_PROGRESS status
// until then. It is stored with the stack, so a snapshot taken while an
// operation runs can finish it after a restore.
type pendingOp struct {
	Kind    string    `json:"kind"`
	ReadyAt time.Time `json:"readyAt"`
	// OnFailure is ROLLBACK, DO_NOTHING or DELETE.
	OnFailure string `json:"onFailure,omitempty"`
	// Failure is the status reason of a failed operation, "" when it
	// succeeded. A rollback keeps it as the stack's reason.
	Failure     string `json:"failure,omitempty"`
	ChangeSetID string `json:"changeSetId,omitempty"`
	// Prior is the stack before the update, which a rollback restores.
	Prior *storedPrior `json:"prior,omitempty"`
	// Replaced are the old resources of the update's replacements.
	Replaced []retainedResource `json:"replaced,omitempty"`
	// Imports are the exports the updated template imports.
	Imports []string `json:"imports,omitempty"`
	// HeldCleanup marks an update whose cleanup phase has not run.
	HeldCleanup bool `json:"heldCleanup,omitempty"`
	// Skip names the resources ContinueUpdateRollback leaves as they are.
	Skip []string `json:"skip,omitempty"`
	// Retain and Force are DeleteStack's RetainResources and whether a
	// resource that fails to delete is kept instead.
	Retain []string `json:"retain,omitempty"`
	Force  bool     `json:"force,omitempty"`

	// plan is the update's plan. It is lost in a snapshot, and a rollback
	// then prepares the previous template again from Prior.
	plan *updatePlan
}

// storedPrior is priorState in a form a snapshot can hold.
type storedPrior struct {
	TemplateBody     string          `json:"templateBody"`
	Params           []cfn.Parameter `json:"params,omitempty"`
	Description      string          `json:"description,omitempty"`
	Outputs          []cfn.Output    `json:"outputs,omitempty"`
	NotificationARNs []string        `json:"notificationArns,omitempty"`
}

func storePrior(p *priorState) *storedPrior {
	return &storedPrior{
		TemplateBody: p.templateBody, Params: p.params, Description: p.description,
		Outputs: p.outputs, NotificationARNs: p.notificationARNs,
	}
}

func (s *storedPrior) state() priorState {
	return priorState{
		templateBody: s.TemplateBody, params: s.Params, description: s.Description,
		outputs: s.Outputs, notificationARNs: s.NotificationARNs,
	}
}

// toRetained stores replacements in their snapshot form.
func toRetained(replaced []replacement) []retainedResource {
	out := make([]retainedResource, 0, len(replaced))

	for i := range replaced {
		r := &replaced[i]
		out = append(out, retainedResource{
			LogicalID: r.id, Type: r.old.typ, Resolved: r.old.resolved, Props: r.old.props,
			DeleteID: r.old.deleteID, ReplacePolicy: r.policy, Reclaimed: r.reclaimed,
		})
	}

	return out
}

func replacements(stored []retainedResource) []replacement {
	out := make([]replacement, 0, len(stored))
	for i := range stored {
		out = append(out, stored[i].replacement())
	}

	return out
}

// finish runs the last phase of an operation, at once or, under
// AsyncSettle, once the settle window and the operation's events have
// passed.
func (m *Mock) finish(ctx context.Context, sd *stackData, op *pendingOp) {
	if m.settleWindow <= 0 {
		m.runPhase(ctx, sd, op)
		return
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	ready := m.clock.Now().Add(m.settleWindow)
	if sd.cursor.After(ready) {
		ready = sd.cursor
	}

	op.ReadyAt = ready
	sd.pending = op
	sd.cursor = time.Time{}
}

func (m *Mock) complete(ctx context.Context, sd *stackData, op *pendingOp) {
	switch op.Kind {
	case opCreate:
		m.completeCreate(ctx, sd, op)
	case opUpdate:
		m.completeUpdate(ctx, sd, op)
	case opRollback:
		m.doRollback(ctx, sd, op)
	case opRollbackCreate:
		m.completeRollbackCreate(ctx, sd)
	case opContinue:
		m.completeContinue(ctx, sd, op)
	case opDelete:
		m.finishDelete(ctx, sd, teardownOpts{retain: nameSet(op.Retain), force: op.Force})
	}
}

func nameSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}

	return out
}

// settle finishes the operations whose settle window has passed. Every API
// call settles first, so reads never need the serve ticker.
func (m *Mock) settle(ctx context.Context) {
	m.settleAt(ctx, m.clock.Now())
}

// Tick finishes the stack operations due at now. The serve background
// ticker calls it, so an operation's last phase, such as deleting the
// resources of a deleted stack, runs even when no one reads the stack. It
// reports whether any operation finished.
func (m *Mock) Tick(now time.Time) bool {
	return m.settleAt(context.Background(), now)
}

func (m *Mock) settleAt(ctx context.Context, now time.Time) bool {
	changed := false

	for _, sd := range m.sortedStacks() {
		if m.settleStack(ctx, sd, now) {
			changed = true
		}
	}

	return changed
}

// settleStack runs the stack's pending phase once it is due. It holds
// opMu and keeps the stack busy until the phase has returned, so an
// operation that starts meanwhile, such as a DeleteStack, waits for it or
// is refused instead of interleaving with it.
func (m *Mock) settleStack(ctx context.Context, sd *stackData, now time.Time) bool {
	sd.opMu.Lock()
	defer sd.opMu.Unlock()

	op := sd.takeDue(now)
	if op == nil {
		return false
	}

	defer sd.setBusy(false)

	m.runPhase(ctx, sd, op)

	return true
}

// runPhase runs a last phase. A panic in it, such as from a provisioner,
// is recovered and fails the stack, so it is never left in an
// *_IN_PROGRESS status that nothing will finish.
func (m *Mock) runPhase(ctx context.Context, sd *stackData, op *pendingOp) {
	defer func() {
		if r := recover(); r != nil {
			m.failStranded(sd, fmt.Sprintf("%s: %v", reasonInternalFailure, r))
		}
	}()

	m.complete(ctx, sd, op)
}

// strandedOutcome is the status a stack ends in when the operation behind
// its *_IN_PROGRESS status can no longer finish. A finished update whose
// cleanup stopped keeps its outcome, as a cleanup failure does in AWS.
func strandedOutcome(status string) (string, bool) {
	switch status {
	case cfn.StatusCreateInProgress:
		return cfn.StatusCreateFailed, true
	case cfn.StatusRollbackInProgress:
		return cfn.StatusRollbackFailed, true
	case cfn.StatusUpdateInProgress, cfn.StatusUpdateRollbackInProgress:
		return cfn.StatusUpdateRollbackFailed, true
	case cfn.StatusUpdateCompleteCleanupInProgress:
		return cfn.StatusUpdateComplete, true
	case cfn.StatusUpdateRollbackCompleteCleanupInProgress:
		return cfn.StatusUpdateRollbackComplete, true
	case cfn.StatusDeleteInProgress:
		return cfn.StatusDeleteFailed, true
	}

	return "", false
}

// failStranded moves a stack whose operation stopped to its failure
// status and records reason.
func (m *Mock) failStranded(sd *stackData, reason string) {
	sd.mu.Lock()
	sd.pending = nil
	next, ok := strandedOutcome(sd.stack.Status)
	sd.mu.Unlock()

	if ok {
		m.emitStackEvent(sd, next, reason)
	}
}

// normalizeStranded fixes a restored stack left in an *_IN_PROGRESS status
// with no operation to finish it, such as one saved by an older snapshot.
func normalizeStranded(sd *stackData) {
	if sd.pending != nil {
		return
	}

	if next, ok := strandedOutcome(sd.stack.Status); ok {
		sd.stack.Status = next
		sd.stack.StatusReason = reasonInternalFailure
	}
}

func (sd *stackData) setBusy(busy bool) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.busy = busy
}

// takeDue removes and returns the stack's pending phase once it is due. The
// stack stays busy until the caller clears it.
func (sd *stackData) takeDue(now time.Time) *pendingOp {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.pending == nil || now.Before(sd.pending.ReadyAt) {
		return nil
	}

	op := sd.pending
	sd.pending = nil
	sd.busy = true

	return op
}

// startCursor makes the events of the operation that starts arrive one
// after another under AsyncSettle. The caller holds sd.mu.
func (m *Mock) startCursor(sd *stackData) {
	if m.settleWindow > 0 {
		sd.cursor = m.clock.Now()
	}
}

// eventTime is the time the next event of the stack is stamped with. The
// caller holds sd.mu.
func (m *Mock) eventTime(sd *stackData) time.Time {
	now := m.clock.Now()
	if sd.cursor.IsZero() {
		return now
	}

	t := sd.cursor
	if now.After(t) {
		t = now
	}

	sd.cursor = t.Add(stackEventStep)

	return t
}

// visibleResources returns the stack's resources as the events seen by now
// report them. A resource whose events have not all arrived shows the
// status of its newest arrived event, and one with no arrived event is not
// listed yet.
func (sd *stackData) visibleResources(now time.Time) []cfn.StackResource {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	latest := map[string]*cfn.StackEvent{}
	waiting := map[string]bool{}

	for i := range sd.stack.Events {
		e := &sd.stack.Events[i]
		if e.ResourceType == stackResourceType && e.LogicalID == sd.stack.Name {
			continue
		}

		if e.Timestamp.After(now) {
			waiting[e.LogicalID] = true
		} else {
			latest[e.LogicalID] = e
		}
	}

	out := make([]cfn.StackResource, 0, len(sd.stack.Resources))

	for _, r := range sd.stack.Resources {
		if waiting[r.LogicalID] {
			e := latest[r.LogicalID]
			if e == nil {
				continue
			}

			r.Status, r.StatusReason, r.Timestamp = e.Status, e.StatusReason, e.Timestamp
			if e.PhysicalID != "" {
				r.PhysicalID = e.PhysicalID
			}
		}

		out = append(out, r)
	}

	return out
}

// CancelUpdateStack stops an update that is still UPDATE_IN_PROGRESS and
// rolls it back, ending UPDATE_ROLLBACK_COMPLETE. Only an update running
// under AsyncSettle can be caught in progress. Otherwise the update has
// already finished, and the call is a ValidationError as in AWS.
func (m *Mock) CancelUpdateStack(ctx context.Context, in *cfn.CancelUpdateStackInput) error {
	m.settle(ctx)

	sd, err := m.activeStack(in.StackName)
	if err != nil {
		return err
	}

	retry, err := sd.checkToken(in.ClientRequestToken, actionCancelUpdateStack)
	if err != nil || retry {
		return err
	}

	sd.opMu.Lock()
	defer sd.opMu.Unlock()

	op := m.takeCancellable(sd, in.ClientRequestToken)
	if op == nil {
		return cerrors.New(cerrors.InvalidArgument, msgCancelBadStatus)
	}

	m.markCancelled(sd)
	sd.finishChangeSet(op.ChangeSetID, false)
	m.startRollback(ctx, sd, op, reasonUpdateCancelled)

	return nil
}

// takeCancellable takes the pending phase of an update in progress. The
// update's events that have not arrived yet are dropped: the cancel stops
// the update before them, and the rollback undoes what it did.
func (m *Mock) takeCancellable(sd *stackData, token string) *pendingOp {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	op := sd.pending
	if op == nil || op.Kind != opUpdate || sd.stack.Status != cfn.StatusUpdateInProgress {
		return nil
	}

	sd.pending = nil
	sd.recordToken(token, actionCancelUpdateStack)

	sd.dropUnarrivedEvents(m.clock.Now())

	return op
}

// dropUnarrivedEvents forgets the events stamped after now, the part of an
// operation a cancel stopped before. The caller holds sd.mu.
func (sd *stackData) dropUnarrivedEvents(now time.Time) {
	kept := sd.stack.Events[:0]

	for i := range sd.stack.Events {
		if !sd.stack.Events[i].Timestamp.After(now) {
			kept = append(kept, sd.stack.Events[i])
		}
	}

	sd.stack.Events = kept
}

// markCancelled fails the resources a cancel caught in progress.
func (m *Mock) markCancelled(sd *stackData) {
	sd.mu.RLock()

	var order []string

	last := map[string]cfn.StackEvent{}

	for i := range sd.stack.Events {
		e := &sd.stack.Events[i]
		if e.ResourceType == stackResourceType && e.LogicalID == sd.stack.Name {
			continue
		}

		if _, seen := last[e.LogicalID]; !seen {
			order = append(order, e.LogicalID)
		}

		last[e.LogicalID] = *e
	}
	sd.mu.RUnlock()

	for _, id := range order {
		e := last[id]

		switch e.Status {
		case cfn.ResourceCreateInProgress:
			m.emitResourceEvent(sd, id, e.PhysicalID, e.ResourceType, cfn.ResourceCreateFailed, reasonCreateCancelled)
		case cfn.ResourceUpdateInProgress:
			m.emitResourceEvent(sd, id, e.PhysicalID, e.ResourceType, cfn.ResourceUpdateFailed, reasonResourceCancelled)
		}
	}
}
